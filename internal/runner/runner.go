// Package runner orchestrates applying config modules, integrating idempotency,
// hooks, atomic rollback, verification, audit logging, and machine tagging.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atomikpanda/dotular/internal/actions"
	"github.com/atomikpanda/dotular/internal/ageutil"
	"github.com/atomikpanda/dotular/internal/audit"
	"github.com/atomikpanda/dotular/internal/config"
	"github.com/atomikpanda/dotular/internal/platform"
	"github.com/atomikpanda/dotular/internal/shell"
	"github.com/atomikpanda/dotular/internal/tags"
	"github.com/atomikpanda/dotular/internal/ui"
)

type itemOutcome int

const (
	outcomeApplied itemOutcome = iota
	outcomeSkipped
	outcomeUnresolved
	outcomeFailed
)

// DefaultRollbackTimeout bounds compensation for callers that do not override it.
const DefaultRollbackTimeout = 2 * time.Minute

type preparedItem struct {
	item              config.Item
	action            actions.Action
	skipReason        string
	alreadyApplied    bool
	isSync            bool
	compensation      actions.Compensation
	fallback          actions.Compensation
	unavailableReason string
	filesystemBacked  bool
	warningsEmitted   bool
}

type preparedModule struct {
	items       []preparedItem
	hasSyncItem bool
	warnings    []string
}

type commandCompensation struct {
	command     string
	description string
	run         func(context.Context, string) error
}

func (c commandCompensation) Describe() string {
	return c.description
}

func (c commandCompensation) Run(ctx context.Context) error {
	return c.run(ctx, c.command)
}

// ModuleResult holds the outcome counts for a single applied module.
type ModuleResult struct {
	Applied        int
	Skipped        int
	Unresolved     int
	Failed         int
	RolledBack     int
	RollbackFailed int
	Uncompensated  int
	Err            error
}

// Runner orchestrates applying config modules on the current platform.
type Runner struct {
	Config            config.Config
	DryRun            bool
	Verbose           bool
	Atomic            bool // snapshot-and-rollback per module (default true)
	RollbackTimeout   time.Duration
	OS                string
	MachineTags       []string
	IgnoreTags        bool
	Out               io.Writer
	UI                *ui.UI
	AgeKey            *ageutil.Key
	Command           string // "apply" | "push" | "pull" | "sync" | "verify" — for audit log
	DirectionOverride string // when set, overrides direction on all non-link file items

	actionBuilder func(config.Item, string) (actions.Action, string, error)
	shellRun      func(context.Context, string) error
}

// New creates a Runner for the current platform, resolving age credentials and
// machine tags automatically.
func New(cfg config.Config, dryRun, verbose, atomic bool) *Runner {
	r := &Runner{
		Config:          cfg,
		DryRun:          dryRun,
		Verbose:         verbose,
		Atomic:          atomic,
		RollbackTimeout: DefaultRollbackTimeout,
		OS:              platform.Current(),
		Out:             os.Stdout,
		Command:         "apply",
	}
	r.UI = ui.New(r.Out, os.Stderr)

	r.AgeKey = resolveAgeKey(cfg.Age)
	r.MachineTags = loadMachineTags()
	return r
}

// --- public apply API --------------------------------------------------------

// ApplyAll applies every module in order, respecting tag filters.
func (r *Runner) ApplyAll(ctx context.Context) error {
	start := time.Now()
	var totalApplied, totalSkipped, totalUnresolved, totalFailed int
	var totalRolledBack, totalRollbackFailed, totalUncompensated int
	var firstErr error
	returnedNormally := false

	defer func() {
		r.UI.Summary(
			totalApplied,
			totalSkipped,
			totalUnresolved,
			totalFailed,
			totalRolledBack,
			totalRollbackFailed,
			totalUncompensated,
			firstErr != nil || !returnedNormally,
			time.Since(start),
		)
	}()

	if err := r.Config.Validate(); err != nil {
		firstErr = fmt.Errorf("validate config before apply: %w", err)
		return firstErr
	}

	for _, mod := range r.Config.Modules {
		if !r.matchesTags(mod) {
			if r.Verbose {
				r.UI.SkipHeader(mod.Name, "tag mismatch")
			}
			continue
		}
		result := r.ApplyModule(ctx, mod)
		totalApplied += result.Applied
		totalSkipped += result.Skipped
		totalUnresolved += result.Unresolved
		totalFailed += result.Failed
		totalRolledBack += result.RolledBack
		totalRollbackFailed += result.RollbackFailed
		totalUncompensated += result.Uncompensated
		if result.Err != nil {
			firstErr = result.Err
			break
		}
	}
	returnedNormally = true
	return firstErr
}

// ApplyModule applies a single module with hooks, snapshot/rollback, and audit.
func (r *Runner) ApplyModule(ctx context.Context, mod config.Module) ModuleResult {
	r.UI.Header(mod.Name)

	var result ModuleResult
	if err := (config.Config{Modules: []config.Module{mod}}).Validate(); err != nil {
		result.Err = fmt.Errorf("module %q: validate config: %w", mod.Name, err)
	} else if r.Atomic && !r.DryRun {
		result = r.applyAtomicModule(ctx, mod)
	} else {
		result = r.applyDirectModule(ctx, mod)
	}

	r.UI.ModuleSummary(
		result.Applied,
		result.Skipped,
		result.Unresolved,
		result.Failed,
		result.RolledBack,
		result.RollbackFailed,
		result.Uncompensated,
		result.Err != nil,
	)
	return result
}

func (r *Runner) applyDirectModule(ctx context.Context, mod config.Module) ModuleResult {
	if err := r.runHook(ctx, mod.Hooks.BeforeApply, "module", mod.Name, "before_apply"); err != nil {
		return ModuleResult{Err: err}
	}

	applied, skipped, unresolved, failed, applyErr := r.applyItems(ctx, mod)
	result := ModuleResult{
		Applied:    applied,
		Skipped:    skipped,
		Unresolved: unresolved,
		Failed:     failed,
		Err:        applyErr,
	}
	if applyErr != nil {
		return result
	}
	if err := r.runHook(ctx, mod.Hooks.AfterApply, "module", mod.Name, "after_apply"); err != nil {
		result.Err = err
	}
	return result
}

// --- public verify API -------------------------------------------------------

// VerifyAll runs verify checks for all modules, returning an error if any fail.
func (r *Runner) VerifyAll(ctx context.Context) (allPassed bool, err error) {
	allPassed = true
	for _, mod := range r.Config.Modules {
		if !r.matchesTags(mod) {
			continue
		}
		passed, err := r.VerifyModule(ctx, mod)
		if err != nil {
			return false, err
		}
		if !passed {
			allPassed = false
		}
	}
	return allPassed, nil
}

// VerifyModule runs verify commands for every item in the module that defines one.
// It reports pass/fail per item without modifying any state.
// Returns (false, nil) when checks ran but some failed.
func (r *Runner) VerifyModule(ctx context.Context, mod config.Module) (allPassed bool, err error) {
	r.UI.Header(mod.Name)
	allPassed = true

	for _, item := range mod.Items {
		if item.Verify == "" {
			if r.Verbose {
				r.UI.Skip("no verify", item.Type())
			}
			continue
		}

		action, skipReason, buildErr := r.buildAction(item, mod.Name)
		if buildErr != nil || skipReason != "" {
			continue
		}

		start := time.Now()
		verifyErr := r.runShell(ctx, item.Verify)
		dur := time.Since(start)
		outcome := "success"
		if verifyErr != nil {
			outcome = "failure"
			allPassed = false
			r.UI.ItemResult(action.Describe(), dur, verifyErr)
		} else {
			r.UI.ItemResult(action.Describe(), dur, nil)
		}

		audit.Log(audit.Entry{
			Command: r.Command,
			Module:  mod.Name,
			Item:    action.Describe(),
			Outcome: outcome,
		})
	}
	return allPassed, nil
}

func (r *Runner) applyAtomicModule(ctx context.Context, mod config.Module) (result ModuleResult) {
	prepared, err := r.prepareAtomicModule(ctx, mod)
	if err != nil {
		result.Err = err
		return result
	}

	transaction, recorder, err := captureModuleTransaction()
	if err != nil {
		result.Err = fmt.Errorf("module %q: create snapshot: %w", mod.Name, err)
		return result
	}
	finished := false
	defer func() {
		panicValue := recover()
		if panicValue == nil {
			return
		}
		if !finished {
			report := r.rollbackTransaction(ctx, transaction, nil)
			r.reportRollback(mod.Name, report, &result)
		}
		panic(panicValue)
	}()

	if err := r.capturePreparedModule(ctx, mod, &prepared, recorder); err != nil {
		cleanupErr := discardPreflightSnapshot(transaction)
		finished = true
		result.Err = errors.Join(err, cleanupErr)
		return result
	}
	for _, warning := range prepared.warnings {
		r.UI.Warn(warning)
	}

	if err := r.runAtomicHook(
		ctx,
		transaction,
		mod.Hooks.BeforeApply,
		mod.Hooks.Rollback.BeforeApply,
		operationIdentity{scope: "module", target: mod.Name, operation: "hook before_apply"},
	); err != nil {
		finished = true
		return r.failAtomicModule(ctx, mod.Name, transaction, result, err)
	}
	if prepared.hasSyncItem {
		if err := r.runAtomicHook(
			ctx,
			transaction,
			mod.Hooks.BeforeSync,
			mod.Hooks.Rollback.BeforeSync,
			operationIdentity{scope: "module", target: mod.Name, operation: "hook before_sync"},
		); err != nil {
			finished = true
			return r.failAtomicModule(ctx, mod.Name, transaction, result, err)
		}
	}

	result.Applied, result.Skipped, result.Unresolved, result.Failed, err =
		r.applyPreparedItems(ctx, mod, prepared, transaction)
	if err != nil {
		finished = true
		return r.failAtomicModule(ctx, mod.Name, transaction, result, err)
	}

	if prepared.hasSyncItem {
		if err := r.runAtomicHook(
			ctx,
			transaction,
			mod.Hooks.AfterSync,
			mod.Hooks.Rollback.AfterSync,
			operationIdentity{scope: "module", target: mod.Name, operation: "hook after_sync"},
		); err != nil {
			finished = true
			return r.failAtomicModule(ctx, mod.Name, transaction, result, err)
		}
	}
	if err := r.runAtomicHook(
		ctx,
		transaction,
		mod.Hooks.AfterApply,
		mod.Hooks.Rollback.AfterApply,
		operationIdentity{scope: "module", target: mod.Name, operation: "hook after_apply"},
	); err != nil {
		finished = true
		return r.failAtomicModule(ctx, mod.Name, transaction, result, err)
	}
	if err := ctx.Err(); err != nil {
		finished = true
		return r.failAtomicModule(ctx, mod.Name, transaction, result, err)
	}

	if err := transaction.commit(); err != nil {
		r.UI.Warn(fmt.Sprintf("[rollback] committed module %q cleanup failed: %v", mod.Name, err))
		result.Err = err
	}
	finished = true
	return result
}

func (r *Runner) prepareAtomicModule(ctx context.Context, mod config.Module) (preparedModule, error) {
	if err := ctx.Err(); err != nil {
		return preparedModule{}, err
	}
	prepared := preparedModule{items: make([]preparedItem, 0, len(mod.Items))}
	for _, item := range mod.Items {
		action, skipReason, err := r.actionFor(item, mod.Name)
		if err != nil {
			return preparedModule{}, fmt.Errorf("module %q: %w", mod.Name, err)
		}
		entry := preparedItem{
			item:       item,
			action:     action,
			skipReason: skipReason,
			isSync: (item.Type() == "file" || item.Type() == "directory") &&
				r.fileDirection(item) == "sync",
		}
		if err := ctx.Err(); err != nil {
			return preparedModule{}, err
		}
		prepared.items = append(prepared.items, entry)
	}

	for _, pair := range []struct {
		command  string
		identity string
		enabled  bool
	}{
		{mod.Hooks.Rollback.BeforeApply, "module hook before_apply", true},
		{mod.Hooks.Rollback.AfterApply, "module hook after_apply", true},
		{mod.Hooks.Rollback.BeforeSync, "module hook before_sync", prepared.hasApplicableSyncItem()},
		{mod.Hooks.Rollback.AfterSync, "module hook after_sync", prepared.hasApplicableSyncItem()},
	} {
		if pair.enabled {
			if err := validateRollbackCommand(ctx, pair.command, pair.identity); err != nil {
				return preparedModule{}, fmt.Errorf("module %q: %w", mod.Name, err)
			}
		}
	}
	for _, entry := range prepared.items {
		if entry.skipReason != "" {
			continue
		}
		for _, pair := range []struct {
			command  string
			identity string
			enabled  bool
		}{
			{entry.item.Rollback, "item rollback", true},
			{entry.item.Hooks.Rollback.BeforeApply, "item hook before_apply", true},
			{entry.item.Hooks.Rollback.AfterApply, "item hook after_apply", true},
			{entry.item.Hooks.Rollback.BeforeSync, "item hook before_sync", entry.isSync},
			{entry.item.Hooks.Rollback.AfterSync, "item hook after_sync", entry.isSync},
		} {
			if pair.enabled {
				if err := validateRollbackCommand(ctx, pair.command, pair.identity); err != nil {
					return preparedModule{}, fmt.Errorf(
						"module %q item %q: %w",
						mod.Name,
						entry.action.Describe(),
						err,
					)
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return preparedModule{}, err
	}
	return prepared, nil
}

func (p preparedModule) hasApplicableSyncItem() bool {
	for _, entry := range p.items {
		if entry.skipReason == "" && !entry.alreadyApplied && entry.isSync {
			return true
		}
	}
	return false
}

func validateRollbackCommand(ctx context.Context, command, identity string) error {
	if command == "" {
		return nil
	}
	if err := shell.Validate(ctx, command); err != nil {
		return fmt.Errorf("validate %s rollback: %w", identity, err)
	}
	return nil
}

func (r *Runner) capturePreparedModule(
	ctx context.Context,
	mod config.Module,
	prepared *preparedModule,
	recorder snapshotRecorder,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for index := range prepared.items {
		entry := &prepared.items[index]
		if entry.skipReason != "" {
			continue
		}
		if entry.item.SkipIf != "" {
			exitsZero, err := shell.Eval(ctx, entry.item.SkipIf)
			if err != nil {
				return fmt.Errorf("module %q: skip_if eval failed: %w", mod.Name, err)
			}
			if exitsZero {
				entry.skipReason = "skip_if"
				continue
			}
		}
		writer, ok := entry.action.(actions.PathWriter)
		if !ok {
			continue
		}
		paths := writer.WritePaths()
		entry.filesystemBacked = len(paths) != 0
		for _, path := range paths {
			if err := recorder.Record(path); err != nil {
				return fmt.Errorf("module %q: snapshot %s: %w", mod.Name, path, err)
			}
		}
	}

	for index := range prepared.items {
		entry := &prepared.items[index]
		if entry.skipReason != "" {
			continue
		}
		if preparer, ok := entry.action.(actions.CompensationPreparer); ok {
			preparation, err := preparer.PrepareCompensation(ctx)
			if err != nil {
				return fmt.Errorf(
					"module %q: prepare compensation for %q: %w",
					mod.Name,
					entry.action.Describe(),
					err,
				)
			}
			if preparation.AlreadyApplied {
				entry.alreadyApplied = true
				continue
			}
			entry.compensation = preparation.Compensation
			entry.unavailableReason = preparation.UnavailableReason
		}
		if entry.item.Rollback != "" {
			entry.fallback = commandCompensation{
				command:     entry.item.Rollback,
				description: "run explicit rollback for " + entry.action.Describe(),
				run:         r.runShell,
			}
		}
		if entry.compensation == nil && entry.fallback == nil && !entry.filesystemBacked &&
			entry.unavailableReason == "" {
			entry.unavailableReason = "no automatic compensation or explicit rollback"
		}
		if entry.skipReason == "" && !entry.alreadyApplied {
			prepared.warnings = append(prepared.warnings, preparedItemWarnings(*entry)...)
			entry.warningsEmitted = true
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	prepared.hasSyncItem = prepared.hasApplicableSyncItem()
	addHookWarning := func(scope, target, hookName, forward, rollback string) {
		if forward == "" || rollback != "" {
			return
		}
		prepared.warnings = append(prepared.warnings, fmt.Sprintf(
			"[rollback] %s %q hook %s will be uncompensated: no rollback hook declared",
			scope,
			target,
			hookName,
		))
	}
	addHookWarning("module", mod.Name, "before_apply", mod.Hooks.BeforeApply, mod.Hooks.Rollback.BeforeApply)
	addHookWarning("module", mod.Name, "after_apply", mod.Hooks.AfterApply, mod.Hooks.Rollback.AfterApply)
	if prepared.hasSyncItem {
		addHookWarning("module", mod.Name, "before_sync", mod.Hooks.BeforeSync, mod.Hooks.Rollback.BeforeSync)
		addHookWarning("module", mod.Name, "after_sync", mod.Hooks.AfterSync, mod.Hooks.Rollback.AfterSync)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func discardPreflightSnapshot(transaction *moduleTransaction) error {
	if transaction == nil || transaction.snapshot == nil {
		return nil
	}
	if err := discardModuleSnapshot(transaction.snapshot); err != nil {
		return fmt.Errorf("discard preflight filesystem snapshot: %w", err)
	}
	return nil
}

func (r *Runner) applyPreparedItems(
	ctx context.Context,
	mod config.Module,
	prepared preparedModule,
	transaction *moduleTransaction,
) (applied, skipped, unresolved, failed int, err error) {
	for _, entry := range prepared.items {
		outcome, itemErr := r.applyPreparedItem(ctx, mod, entry, transaction)
		switch outcome {
		case outcomeApplied:
			applied++
		case outcomeSkipped:
			skipped++
		case outcomeUnresolved:
			unresolved++
		case outcomeFailed:
			failed++
		}
		if itemErr != nil {
			return applied, skipped, unresolved, failed, itemErr
		}
	}
	return applied, skipped, unresolved, failed, nil
}

func (r *Runner) applyPreparedItem(
	ctx context.Context,
	mod config.Module,
	prepared preparedItem,
	transaction *moduleTransaction,
) (itemOutcome, error) {
	skipReason := prepared.skipReason
	if skipReason == "" && prepared.item.SkipIf != "" {
		exitsZero, err := shell.Eval(ctx, prepared.item.SkipIf)
		if err != nil {
			return outcomeFailed, fmt.Errorf("module %q: skip_if eval failed: %w", mod.Name, err)
		}
		if exitsZero {
			skipReason = "skip_if"
		}
	}
	if skipReason == "" && prepared.alreadyApplied {
		skipReason = "already applied"
	}
	if skipReason == "" {
		if idempotent, ok := prepared.action.(actions.Idempotent); ok {
			alreadyApplied, err := idempotent.IsApplied(ctx)
			if err != nil {
				return outcomeFailed, fmt.Errorf("module %q: idempotency check: %w", mod.Name, err)
			}
			if alreadyApplied {
				skipReason = "already applied"
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return outcomeFailed, err
	}
	if skipReason != "" {
		if r.Verbose {
			r.UI.Skip(skipReason, prepared.action.Describe())
		}
		audit.Log(audit.Entry{
			Command: r.Command,
			Module:  mod.Name,
			Item:    prepared.action.Describe(),
			Outcome: "skipped",
			Reason:  skipReason,
		})
		return outcomeSkipped, nil
	}
	if !prepared.warningsEmitted {
		for _, warning := range preparedItemWarnings(prepared) {
			r.UI.Warn(warning)
		}
	}

	if err := r.runAtomicHook(
		ctx,
		transaction,
		prepared.item.Hooks.BeforeApply,
		prepared.item.Hooks.Rollback.BeforeApply,
		operationIdentity{scope: "item", target: prepared.action.Describe(), operation: "hook before_apply"},
	); err != nil {
		return outcomeFailed, fmt.Errorf("module %q: %w", mod.Name, err)
	}
	if prepared.isSync {
		if err := r.runAtomicHook(
			ctx,
			transaction,
			prepared.item.Hooks.BeforeSync,
			prepared.item.Hooks.Rollback.BeforeSync,
			operationIdentity{scope: "item", target: prepared.action.Describe(), operation: "hook before_sync"},
		); err != nil {
			return outcomeFailed, fmt.Errorf("module %q: %w", mod.Name, err)
		}
	}

	actionIdentity := operationIdentity{
		scope:     "item",
		target:    prepared.action.Describe(),
		operation: "action",
	}
	var filesystemMarker *rollbackItemMarker
	if prepared.filesystemBacked {
		var err error
		filesystemMarker, err = transaction.activateFilesystemItem(actionIdentity)
		if err != nil {
			return outcomeFailed, fmt.Errorf("module %q: activate filesystem rollback accounting: %w", mod.Name, err)
		}
	}

	var actionEntry *journalEntry
	if prepared.compensation != nil || prepared.fallback != nil || !prepared.filesystemBacked {
		accountingSource := rollbackAccountingItem
		if prepared.filesystemBacked {
			accountingSource = rollbackAccountingNone
		}
		var err error
		actionEntry, err = transaction.activate(journalEntry{
			identity:          actionIdentity,
			compensation:      prepared.compensation,
			fallback:          prepared.fallback,
			unavailableReason: prepared.unavailableReason,
			accountingSource:  accountingSource,
		})
		if err != nil {
			return outcomeFailed, fmt.Errorf("module %q: activate action rollback: %w", mod.Name, err)
		}
	}

	if fileAction, ok := prepared.action.(*actions.FileAction); ok && fileAction.Permissions != "" {
		if status := fileAction.PermissionsStatus(); status != "" {
			r.UI.Info("     " + status)
		}
	}
	start := time.Now()
	runErr := prepared.action.Run(ctx, false)
	if runErr == nil {
		runErr = ctx.Err()
	}
	if runErr != nil && errors.Is(runErr, actions.ErrSkipped) {
		if actionEntry != nil {
			if err := transaction.deactivate(actionEntry); err != nil {
				return outcomeFailed, fmt.Errorf("module %q: deactivate skipped action rollback: %w", mod.Name, err)
			}
		}
		if filesystemMarker != nil {
			if err := transaction.deactivateFilesystemItem(filesystemMarker); err != nil {
				return outcomeFailed, fmt.Errorf("module %q: deactivate skipped filesystem rollback accounting: %w", mod.Name, err)
			}
		}
		message := strings.TrimSuffix(runErr.Error(), ": "+actions.ErrSkipped.Error())
		r.UI.Skip(message, prepared.action.Describe())
		audit.Log(audit.Entry{
			Command: r.Command,
			Module:  mod.Name,
			Item:    prepared.action.Describe(),
			Outcome: "skipped",
			Reason:  message,
		})
		return outcomeSkipped, nil
	}

	r.UI.ItemResult(prepared.action.Describe(), time.Since(start), runErr)
	outcome, errorText := "applied", ""
	if runErr != nil {
		outcome = "failure"
		errorText = runErr.Error()
	}
	audit.Log(audit.Entry{
		Command: r.Command,
		Module:  mod.Name,
		Item:    prepared.action.Describe(),
		Outcome: outcome,
		Error:   errorText,
	})
	if runErr != nil {
		return outcomeFailed, fmt.Errorf("module %q: %w", mod.Name, runErr)
	}

	if prepared.item.Verify != "" {
		if err := r.runShell(ctx, prepared.item.Verify); err != nil {
			return outcomeFailed, fmt.Errorf(
				"module %q: verify failed for %q: %w",
				mod.Name,
				prepared.action.Describe(),
				err,
			)
		}
		if err := ctx.Err(); err != nil {
			return outcomeFailed, err
		}
	}
	if prepared.isSync {
		if err := r.runAtomicHook(
			ctx,
			transaction,
			prepared.item.Hooks.AfterSync,
			prepared.item.Hooks.Rollback.AfterSync,
			operationIdentity{scope: "item", target: prepared.action.Describe(), operation: "hook after_sync"},
		); err != nil {
			return outcomeFailed, fmt.Errorf("module %q: %w", mod.Name, err)
		}
	}
	if err := r.runAtomicHook(
		ctx,
		transaction,
		prepared.item.Hooks.AfterApply,
		prepared.item.Hooks.Rollback.AfterApply,
		operationIdentity{scope: "item", target: prepared.action.Describe(), operation: "hook after_apply"},
	); err != nil {
		return outcomeFailed, fmt.Errorf("module %q: %w", mod.Name, err)
	}
	return outcomeApplied, nil
}

func preparedItemWarnings(prepared preparedItem) []string {
	target := prepared.action.Describe()
	var warnings []string
	if prepared.compensation == nil && prepared.fallback == nil && !prepared.filesystemBacked {
		warnings = append(warnings, fmt.Sprintf(
			"[rollback] item %q will be uncompensated: %s",
			target,
			prepared.unavailableReason,
		))
	}
	addHookWarning := func(hookName, forward, rollback string) {
		if forward == "" || rollback != "" {
			return
		}
		warnings = append(warnings, fmt.Sprintf(
			"[rollback] item %q hook %s will be uncompensated: no rollback hook declared",
			target,
			hookName,
		))
	}
	addHookWarning("before_apply", prepared.item.Hooks.BeforeApply, prepared.item.Hooks.Rollback.BeforeApply)
	addHookWarning("after_apply", prepared.item.Hooks.AfterApply, prepared.item.Hooks.Rollback.AfterApply)
	if prepared.isSync {
		addHookWarning("before_sync", prepared.item.Hooks.BeforeSync, prepared.item.Hooks.Rollback.BeforeSync)
		addHookWarning("after_sync", prepared.item.Hooks.AfterSync, prepared.item.Hooks.Rollback.AfterSync)
	}
	return warnings
}

func (r *Runner) runAtomicHook(
	ctx context.Context,
	transaction *moduleTransaction,
	command, rollbackCommand string,
	identity operationIdentity,
) error {
	if command == "" {
		return nil
	}
	entry := journalEntry{
		identity: identity,
	}
	if rollbackCommand == "" {
		entry.unavailableReason = "no rollback hook declared"
	} else {
		entry.compensation = commandCompensation{
			command:     rollbackCommand,
			description: "run " + identity.operation + " rollback",
			run:         r.runShell,
		}
	}
	if _, err := transaction.activate(entry); err != nil {
		return fmt.Errorf("activate %s rollback: %w", identity.operation, err)
	}

	if r.Verbose {
		r.UI.Info("  " + fmt.Sprintf("%s (%s %q)", identity.operation, identity.scope, identity.target))
	}
	if err := r.runShell(ctx, command); err != nil {
		return fmt.Errorf("%s failed on %s %q: %w", identity.operation, identity.scope, identity.target, err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s failed on %s %q: %w", identity.operation, identity.scope, identity.target, err)
	}
	return nil
}

func (r *Runner) failAtomicModule(
	ctx context.Context,
	module string,
	transaction *moduleTransaction,
	result ModuleResult,
	cause error,
) ModuleResult {
	result.Applied = 0
	result.Failed = 0
	report := r.rollbackTransaction(ctx, transaction, cause)
	r.reportRollback(module, report, &result)
	result.Err = report.err
	return result
}

func (r *Runner) rollbackTransaction(
	ctx context.Context,
	transaction *moduleTransaction,
	cause error,
) rollbackReport {
	timeout := r.RollbackTimeout
	if timeout <= 0 {
		timeout = DefaultRollbackTimeout
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	return transaction.rollback(cleanupCtx, cause)
}

func (r *Runner) reportRollback(module string, report rollbackReport, result *ModuleResult) {
	for _, rollback := range report.results {
		detail := rollback.reason
		if rollback.err != nil {
			detail = rollback.err.Error()
		}
		r.UI.RollbackResult(
			module,
			rollback.identity.scope,
			rollback.identity.target,
			rollback.identity.operation,
			rollback.outcome,
			detail,
		)
		entry := audit.Entry{
			Command: r.Command,
			Module:  module,
			Item: fmt.Sprintf(
				"%s [%s]",
				rollback.identity.target,
				rollback.identity.operation,
			),
			Phase:   "rollback",
			Scope:   rollback.identity.scope,
			Outcome: rollback.outcome,
		}
		switch rollback.outcome {
		case rollbackOutcomeFailed:
			entry.Error = detail
		case rollbackOutcomeUncompensated:
			entry.Reason = detail
		}
		audit.Log(entry)
	}
	for _, itemOutcome := range report.itemOutcomes {
		switch itemOutcome.outcome {
		case rollbackOutcomeRolledBack:
			result.RolledBack++
		case rollbackOutcomeFailed:
			result.RollbackFailed++
		case rollbackOutcomeUncompensated:
			result.Uncompensated++
		}
	}
	if report.cleanupErr != nil {
		r.UI.Warn(fmt.Sprintf("[rollback] module %q cleanup failed: %v", module, report.cleanupErr))
	}
}

// --- internal apply flow -----------------------------------------------------

// applyItems applies every item in the module, firing sync hooks around sync items.
func (r *Runner) applyItems(ctx context.Context, mod config.Module) (applied, skipped, unresolved, failed int, err error) {
	hasSyncItem := false
	for _, item := range mod.Items {
		t := item.Type()
		if (t == "file" || t == "directory") && r.fileDirection(item) == "sync" {
			hasSyncItem = true
			break
		}
	}

	if hasSyncItem {
		if err := r.runHook(ctx, mod.Hooks.BeforeSync, "module", mod.Name, "before_sync"); err != nil {
			return applied, skipped, unresolved, failed, err
		}
	}

	for _, item := range mod.Items {
		outcome, itemErr := r.applyItem(ctx, mod, item)
		switch outcome {
		case outcomeApplied:
			applied++
		case outcomeSkipped:
			skipped++
		case outcomeUnresolved:
			unresolved++
		case outcomeFailed:
			failed++
		}
		if itemErr != nil {
			return applied, skipped, unresolved, failed, itemErr
		}
	}

	if hasSyncItem {
		if err := r.runHook(ctx, mod.Hooks.AfterSync, "module", mod.Name, "after_sync"); err != nil {
			return applied, skipped, unresolved, failed, err
		}
	}
	return applied, skipped, unresolved, failed, nil
}

func (r *Runner) applyItem(ctx context.Context, mod config.Module, item config.Item) (itemOutcome, error) {
	action, skipReason, err := r.actionFor(item, mod.Name)
	if err != nil {
		return outcomeFailed, fmt.Errorf("module %q: %w", mod.Name, err)
	}
	if skipReason != "" {
		if r.Verbose {
			r.UI.Skip(skipReason, action.Describe())
		}
		audit.Log(audit.Entry{Command: r.Command, Module: mod.Name, Item: action.Describe(), Outcome: "skipped", Reason: skipReason})
		return outcomeSkipped, nil
	}
	if r.DryRun && item.SkipIf != "" {
		r.UI.DryRun(action.Describe() + " [skip_if not evaluated]")
		return outcomeUnresolved, nil
	}

	// --- skip_if ---
	if item.SkipIf != "" {
		exitsZero, err := shell.Eval(ctx, item.SkipIf)
		if err != nil {
			return outcomeFailed, fmt.Errorf("module %q: skip_if eval failed: %w", mod.Name, err)
		}
		if exitsZero {
			if r.Verbose {
				r.UI.Skip("skip_if", action.Describe())
			}
			audit.Log(audit.Entry{Command: r.Command, Module: mod.Name, Item: action.Describe(), Outcome: "skipped", Reason: "skip_if"})
			return outcomeSkipped, nil
		}
	}

	// --- auto-idempotency ---
	if idem, ok := action.(actions.Idempotent); ok {
		applied, err := idem.IsApplied(ctx)
		if err != nil {
			return outcomeFailed, fmt.Errorf("module %q: idempotency check: %w", mod.Name, err)
		}
		if applied {
			if r.Verbose {
				r.UI.Skip("already applied", action.Describe())
			}
			audit.Log(audit.Entry{Command: r.Command, Module: mod.Name, Item: action.Describe(), Outcome: "skipped", Reason: "already applied"})
			return outcomeSkipped, nil
		}
	}

	// --- item hooks: before ---
	itemType := item.Type()
	isSync := (itemType == "file" || itemType == "directory") && r.fileDirection(item) == "sync"
	if err := r.runHook(ctx, item.Hooks.BeforeApply, "item", action.Describe(), "before_apply"); err != nil {
		return outcomeFailed, fmt.Errorf("module %q: %w", mod.Name, err)
	}
	if isSync {
		if err := r.runHook(ctx, item.Hooks.BeforeSync, "item", action.Describe(), "before_sync"); err != nil {
			return outcomeFailed, fmt.Errorf("module %q: %w", mod.Name, err)
		}
	}

	// --- run ---
	if r.DryRun {
		r.UI.DryRun(action.Describe())
		return outcomeApplied, nil
	}

	if fa, ok := action.(*actions.FileAction); ok && fa.Permissions != "" {
		if ps := fa.PermissionsStatus(); ps != "" {
			r.UI.Info("     " + ps)
		}
	}

	start := time.Now()
	runErr := action.Run(ctx, false)

	if runErr != nil && errors.Is(runErr, actions.ErrSkipped) {
		msg := strings.TrimSuffix(runErr.Error(), ": "+actions.ErrSkipped.Error())
		r.UI.Skip(msg, action.Describe())
		audit.Log(audit.Entry{Command: r.Command, Module: mod.Name, Item: action.Describe(), Outcome: "skipped", Reason: msg})
		return outcomeSkipped, nil
	}

	r.UI.ItemResult(action.Describe(), time.Since(start), runErr)

	outcome, errMsg := "success", ""
	if runErr != nil {
		outcome, errMsg = "failure", runErr.Error()
	}
	audit.Log(audit.Entry{Command: r.Command, Module: mod.Name, Item: action.Describe(), Outcome: outcome, Error: errMsg})

	if runErr != nil {
		return outcomeFailed, fmt.Errorf("module %q: %w", mod.Name, runErr)
	}

	// --- verify ---
	if item.Verify != "" {
		if err := r.runShell(ctx, item.Verify); err != nil {
			return outcomeFailed, fmt.Errorf("module %q: verify failed for %q: %w", mod.Name, action.Describe(), err)
		}
	}

	// --- item hooks: after ---
	if isSync {
		if err := r.runHook(ctx, item.Hooks.AfterSync, "item", action.Describe(), "after_sync"); err != nil {
			return outcomeFailed, fmt.Errorf("module %q: %w", mod.Name, err)
		}
	}
	if err := r.runHook(ctx, item.Hooks.AfterApply, "item", action.Describe(), "after_apply"); err != nil {
		return outcomeFailed, fmt.Errorf("module %q: %w", mod.Name, err)
	}

	return outcomeApplied, nil
}

// --- action builder ----------------------------------------------------------

// fileDirection returns the effective direction for a file item, applying any
// DirectionOverride. Link items are always push and are never overridden.
func (r *Runner) fileDirection(item config.Item) string {
	if r.DirectionOverride != "" && !item.Link {
		return r.DirectionOverride
	}
	return item.EffectiveDirection()
}

func (r *Runner) actionFor(item config.Item, moduleName string) (actions.Action, string, error) {
	if r.actionBuilder != nil {
		return r.actionBuilder(item, moduleName)
	}
	return r.buildAction(item, moduleName)
}

// buildAction turns a config item into an executable action. A non-empty
// skipReason means the item must not be run; the action is still returned, so
// callers can name the item with the action's own Describe rather than
// reconstructing a description the action already owns. Only an unrecognised
// item type yields a nil action, because there is nothing to build.
//
// Each case therefore builds the action before deciding whether to skip it. The
// constructors are plain struct literals, so building one that is then discarded
// costs nothing.
func (r *Runner) buildAction(item config.Item, moduleName ...string) (act actions.Action, skipReason string, err error) {
	// sourcePrefix prepends the module name directory to a repo-side path.
	sourcePrefix := func(name string) string {
		if len(moduleName) > 0 && moduleName[0] != "" {
			return filepath.Join(moduleName[0], name)
		}
		return name
	}
	// notApplicable is the reason shared by every case the current platform has
	// no way to carry out.
	notApplicable := item.Type() + " not applicable on " + r.OS

	switch item.Type() {
	case "package":
		act = &actions.PackageAction{Package: item.Package, Manager: item.Via}
		if r.skipManager(item.Via) {
			return act, notApplicable, nil
		}

	case "script":
		act = &actions.ScriptAction{Script: item.Script, Via: item.Via}

	case "file":
		dest := item.Destination.ForOS(r.OS)
		act = &actions.FileAction{
			Source:      sourcePrefix(item.File),
			Destination: dest,
			Direction:   r.fileDirection(item),
			Link:        item.Link,
			Permissions: item.Permissions,
			Encrypted:   item.Encrypted,
			AgeKey:      r.AgeKey,
		}
		if dest == "" {
			return act, notApplicable, nil
		}

	case "directory":
		dest := item.Destination.ForOS(r.OS)
		act = &actions.DirectoryAction{
			Source:      sourcePrefix(item.Directory),
			Destination: dest,
			Direction:   r.fileDirection(item),
			Link:        item.Link,
			Permissions: item.Permissions,
		}
		if dest == "" {
			return act, notApplicable, nil
		}

	case "binary":
		installTo := item.InstallTo
		if installTo == "" {
			installTo = "~/.local/bin"
		}
		sourceURL := item.Source.ForOS(r.OS)
		act = &actions.BinaryAction{
			Name:      item.Binary,
			Version:   item.Version,
			SourceURL: sourceURL,
			InstallTo: installTo,
		}
		if sourceURL == "" {
			return act, notApplicable, nil // no binary for this OS
		}

	case "run":
		act = &actions.RunAction{Command: item.Run, After: item.After}

	case "setting":
		act = &actions.SettingAction{
			Domain: item.Setting,
			Key:    item.Key,
			Value:  item.Value,
			OS:     r.OS,
		}
		if !actions.SettingsSupported(r.OS) {
			return act, notApplicable, nil // no settings mechanism on this OS
		}

	default:
		return nil, "", fmt.Errorf("item has no recognised type: %+v", item)
	}

	// pull and sync reconcile the repo against the machine, so only actions that
	// move data between the two have anything to do. The rest are skipped rather
	// than rejected: every real config mixes them with file items, so erroring
	// would make pull unusable. Gated once here rather than per case — the guard
	// this replaces covered "run" alone and silently missed package, script,
	// binary and setting.
	if r.DirectionOverride == "pull" || r.DirectionOverride == "sync" {
		if _, ok := act.(actions.DirectionAware); !ok {
			return act, "nothing to " + r.DirectionOverride + " for a " + item.Type() + " item", nil
		}
	}
	return act, "", nil
}

// --- helpers -----------------------------------------------------------------

func (r *Runner) matchesTags(mod config.Module) bool {
	return r.IgnoreTags || tags.Matches(r.MachineTags, mod.OnlyTags, mod.ExcludeTags)
}

func (r *Runner) skipManager(manager string) bool {
	targetOS := platform.PackageManagerOS(manager)
	return targetOS != "" && targetOS != r.OS
}

func (r *Runner) runHook(ctx context.Context, cmd, scope, name, hookName string) error {
	if cmd == "" {
		return nil
	}
	// Both lines identify the hook the same way so dry-run and verbose output
	// can be compared; only dry-run adds the command it would have run.
	where := fmt.Sprintf("hook %s (%s %q)", hookName, scope, name)
	if r.DryRun {
		r.UI.DryRun(fmt.Sprintf("%s: %s", where, cmd))
		return nil
	}
	if r.Verbose {
		r.UI.Info("  " + where)
	}
	if err := r.runShell(ctx, cmd); err != nil {
		return fmt.Errorf("hook %s failed on %s %q: %w", hookName, scope, name, err)
	}
	return nil
}

func (r *Runner) runShell(ctx context.Context, command string) error {
	if r.shellRun != nil {
		return r.shellRun(ctx, command)
	}
	return shell.Run(ctx, command)
}

func resolveAgeKey(cfg *config.AgeConfig) *ageutil.Key {
	// Config file takes precedence over env vars.
	if cfg != nil {
		passphrase := cfg.Passphrase
		if strings.HasPrefix(passphrase, "env:") {
			passphrase = os.Getenv(strings.TrimPrefix(passphrase, "env:"))
		}
		if cfg.Identity != "" || passphrase != "" {
			return &ageutil.Key{
				IdentityFile: platform.ExpandPath(cfg.Identity),
				Passphrase:   passphrase,
			}
		}
	}
	// Fallback: environment variables.
	if v := os.Getenv("DOTULAR_AGE_IDENTITY"); v != "" {
		return &ageutil.Key{IdentityFile: platform.ExpandPath(v)}
	}
	if v := os.Getenv("DOTULAR_AGE_PASSPHRASE"); v != "" {
		return &ageutil.Key{Passphrase: v}
	}
	return nil
}

func loadMachineTags() []string {
	cfg, err := tags.Load()
	if err != nil || cfg == nil {
		return nil
	}
	return cfg.Tags
}
