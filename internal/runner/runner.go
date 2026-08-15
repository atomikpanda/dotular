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
	"github.com/atomikpanda/dotular/internal/snapshot"
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

// ModuleResult holds the outcome counts for a single applied module.
type ModuleResult struct {
	Applied    int
	Skipped    int
	Unresolved int
	Failed     int
	Err        error
}

// Runner orchestrates applying config modules on the current platform.
type Runner struct {
	Config            config.Config
	DryRun            bool
	Verbose           bool
	Atomic            bool // snapshot-and-rollback per module (default true)
	OS                string
	MachineTags       []string
	IgnoreTags        bool
	Out               io.Writer
	UI                *ui.UI
	AgeKey            *ageutil.Key
	Command           string // "apply" | "push" | "pull" | "sync" | "verify" — for audit log
	DirectionOverride string // when set, overrides direction on all non-link file items
}

// New creates a Runner for the current platform, resolving age credentials and
// machine tags automatically.
func New(cfg config.Config, dryRun, verbose, atomic bool) *Runner {
	r := &Runner{
		Config:  cfg,
		DryRun:  dryRun,
		Verbose: verbose,
		Atomic:  atomic,
		OS:      platform.Current(),
		Out:     os.Stdout,
		Command: "apply",
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
	var firstErr error

	defer func() {
		r.UI.Summary(totalApplied, totalSkipped, totalUnresolved, totalFailed, time.Since(start))
	}()

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
		if result.Err != nil {
			firstErr = result.Err
			break
		}
	}
	return firstErr
}

// ApplyModule applies a single module with hooks, snapshot/rollback, and audit.
func (r *Runner) ApplyModule(ctx context.Context, mod config.Module) ModuleResult {
	r.UI.Header(mod.Name)

	if err := r.runHook(ctx, mod.Hooks.BeforeApply, "module", mod.Name, "before_apply"); err != nil {
		return ModuleResult{Err: err}
	}

	var snap *snapshot.Snapshot
	if r.Atomic && !r.DryRun {
		var err error
		snap, err = snapshot.New()
		if err != nil {
			return ModuleResult{Err: fmt.Errorf("module %q: create snapshot: %w", mod.Name, err)}
		}
	}

	applied, skipped, unresolved, failed, applyErr := r.applyItems(ctx, mod, snap)

	if applyErr != nil && snap != nil {
		r.UI.Warn(fmt.Sprintf("[rollback] restoring snapshot after failure in %q", mod.Name))
		if restoreErr := snap.Restore(); restoreErr != nil {
			r.UI.Warn(fmt.Sprintf("[rollback] restore error: %v", restoreErr))
		}
		snap.Discard()
		r.UI.ModuleSummary(applied, skipped, unresolved, failed)
		return ModuleResult{Applied: applied, Skipped: skipped, Unresolved: unresolved, Failed: failed, Err: applyErr}
	}
	if snap != nil {
		snap.Discard()
	}

	if applyErr != nil {
		r.UI.ModuleSummary(applied, skipped, unresolved, failed)
		return ModuleResult{Applied: applied, Skipped: skipped, Unresolved: unresolved, Failed: failed, Err: applyErr}
	}

	if err := r.runHook(ctx, mod.Hooks.AfterApply, "module", mod.Name, "after_apply"); err != nil {
		r.UI.ModuleSummary(applied, skipped, unresolved, failed)
		return ModuleResult{Applied: applied, Skipped: skipped, Unresolved: unresolved, Failed: failed, Err: err}
	}

	r.UI.ModuleSummary(applied, skipped, unresolved, failed)
	return ModuleResult{Applied: applied, Skipped: skipped, Unresolved: unresolved, Failed: failed}
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
		verifyErr := shell.Run(ctx, item.Verify)
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

// --- internal apply flow -----------------------------------------------------

// applyItems applies every item in the module, firing sync hooks around sync items.
func (r *Runner) applyItems(ctx context.Context, mod config.Module, snap *snapshot.Snapshot) (applied, skipped, unresolved, failed int, err error) {
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
		outcome, itemErr := r.applyItem(ctx, mod, item, snap)
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

func (r *Runner) applyItem(ctx context.Context, mod config.Module, item config.Item, snap *snapshot.Snapshot) (itemOutcome, error) {
	action, skipReason, err := r.buildAction(item, mod.Name)
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

	// --- snapshot the paths the action will write, before it writes them ---
	if snap != nil {
		if w, ok := action.(actions.PathWriter); ok {
			for _, path := range w.WritePaths() {
				if err := snap.Record(path); err != nil {
					return outcomeFailed, fmt.Errorf("module %q: snapshot %s: %w", mod.Name, path, err)
				}
			}
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
		if err := shell.Run(ctx, item.Verify); err != nil {
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
	if err := shell.Run(ctx, cmd); err != nil {
		return fmt.Errorf("hook %s failed on %s %q: %w", hookName, scope, name, err)
	}
	return nil
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
