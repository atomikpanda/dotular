// Package runner orchestrates applying config modules, integrating idempotency,
// hooks, atomic rollback, verification, audit logging, and machine tagging.
package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/atomikpanda/dotular/internal/actions"
	"github.com/atomikpanda/dotular/internal/ageutil"
	"github.com/atomikpanda/dotular/internal/audit"
	"github.com/atomikpanda/dotular/internal/color"
	"github.com/atomikpanda/dotular/internal/config"
	"github.com/atomikpanda/dotular/internal/platform"
	"github.com/atomikpanda/dotular/internal/shell"
	"github.com/atomikpanda/dotular/internal/snapshot"
	"github.com/atomikpanda/dotular/internal/tags"
)

// Runner orchestrates applying config modules on the current platform.
type Runner struct {
	Config      config.Config
	DryRun      bool
	Verbose     bool
	Atomic      bool // snapshot-and-rollback per module (default true)
	OS          string
	MachineTags []string
	Out         io.Writer
	AgeKey           *ageutil.Key
	Command          string // "apply" | "push" | "pull" | "sync" | "verify" — for audit log
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

	r.AgeKey = resolveAgeKey(cfg.Age)
	r.MachineTags = loadMachineTags()
	return r
}

// --- public apply API --------------------------------------------------------

// ApplyAll applies every module in order, respecting tag filters.
func (r *Runner) ApplyAll(ctx context.Context) error {
	for _, mod := range r.Config.Modules {
		if !r.matchesTags(mod) {
			if r.Verbose {
				fmt.Fprintf(r.Out, "\n%s\n", color.Dim("==> "+mod.Name+"  [skip: tag mismatch]"))
			}
			continue
		}
		if err := r.ApplyModule(ctx, mod); err != nil {
			return err
		}
	}
	return nil
}

// ApplyModule applies a single module with hooks, snapshot/rollback, and audit.
func (r *Runner) ApplyModule(ctx context.Context, mod config.Module) error {
	fmt.Fprintf(r.Out, "\n%s\n", color.BoldCyan("==> "+mod.Name))

	if err := r.runHook(ctx, mod.Hooks.BeforeApply, "module", mod.Name, "before_apply"); err != nil {
		return err
	}

	var snap *snapshot.Snapshot
	if r.Atomic && !r.DryRun {
		var err error
		snap, err = snapshot.New()
		if err != nil {
			return fmt.Errorf("module %q: create snapshot: %w", mod.Name, err)
		}
	}

	applyErr := r.applyItems(ctx, mod, snap)

	if applyErr != nil && snap != nil {
		fmt.Fprintf(r.Out, "  %s\n", color.BoldYellow(fmt.Sprintf("[rollback] restoring snapshot after failure in %q", mod.Name)))
		if restoreErr := snap.Restore(); restoreErr != nil {
			fmt.Fprintf(r.Out, "  %s\n", color.BoldYellow(fmt.Sprintf("[rollback] restore error: %v", restoreErr)))
		}
		snap.Discard()
		return applyErr
	}
	if snap != nil {
		snap.Discard()
	}

	if applyErr != nil {
		return applyErr
	}

	if err := r.runHook(ctx, mod.Hooks.AfterApply, "module", mod.Name, "after_apply"); err != nil {
		return err
	}
	return nil
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
	fmt.Fprintf(r.Out, "\n%s\n", color.BoldCyan("==> "+mod.Name))
	allPassed = true

	for _, item := range mod.Items {
		if item.Verify == "" {
			if r.Verbose {
				fmt.Fprintf(r.Out, "  %s\n", color.Dim("----  "+item.Type()+"  [no verify]"))
			}
			continue
		}

		action, skip, buildErr := r.buildAction(item)
		if buildErr != nil || skip {
			continue
		}

		verifyErr := shell.Run(ctx, item.Verify)
		outcome := "success"
		if verifyErr != nil {
			outcome = "failure"
			allPassed = false
			fmt.Fprintf(r.Out, "  %s  %s\n", color.BoldRed("FAIL"), action.Describe())
		} else {
			fmt.Fprintf(r.Out, "  %s  %s\n", color.BoldGreen("PASS"), action.Describe())
		}

		audit.Log(audit.Entry{
			Command: "verify",
			Module:  mod.Name,
			Item:    action.Describe(),
			Outcome: outcome,
		})
	}
	return allPassed, nil
}

// --- internal apply flow -----------------------------------------------------

// applyItems applies every item in the module, firing sync hooks around sync items.
func (r *Runner) applyItems(ctx context.Context, mod config.Module, snap *snapshot.Snapshot) error {
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
			return err
		}
	}

	for _, item := range mod.Items {
		if err := r.applyItem(ctx, mod, item, snap); err != nil {
			return err
		}
	}

	if hasSyncItem {
		if err := r.runHook(ctx, mod.Hooks.AfterSync, "module", mod.Name, "after_sync"); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) applyItem(ctx context.Context, mod config.Module, item config.Item, snap *snapshot.Snapshot) error {
	action, skip, err := r.buildAction(item)
	if err != nil {
		return fmt.Errorf("module %q: %w", mod.Name, err)
	}
	if skip {
		if r.Verbose {
			fmt.Fprintf(r.Out, "  %s\n", color.Dim(fmt.Sprintf("skip (%s not applicable on %s)", item.Type(), r.OS)))
		}
		return nil
	}

	// --- skip_if ---
	if item.SkipIf != "" {
		exitsZero, err := shell.Eval(ctx, item.SkipIf)
		if err != nil {
			return fmt.Errorf("module %q: skip_if eval failed: %w", mod.Name, err)
		}
		if exitsZero {
			if r.Verbose {
				fmt.Fprintf(r.Out, "  %s\n", color.Dim("skip [skip_if] "+action.Describe()))
			}
			audit.Log(audit.Entry{Command: r.Command, Module: mod.Name, Item: action.Describe(), Outcome: "skipped"})
			return nil
		}
	}

	// --- auto-idempotency ---
	if idem, ok := action.(actions.Idempotent); ok {
		applied, err := idem.IsApplied(ctx)
		if err != nil {
			return fmt.Errorf("module %q: idempotency check: %w", mod.Name, err)
		}
		if applied {
			if r.Verbose {
				fmt.Fprintf(r.Out, "  %s\n", color.Dim("skip [already applied] "+action.Describe()))
			}
			audit.Log(audit.Entry{Command: r.Command, Module: mod.Name, Item: action.Describe(), Outcome: "skipped"})
			return nil
		}
	}

	// --- item hooks: before ---
	itemType := item.Type()
	isSync := (itemType == "file" || itemType == "directory") && r.fileDirection(item) == "sync"
	if err := r.runHook(ctx, item.Hooks.BeforeApply, "item", action.Describe(), "before_apply"); err != nil {
		return fmt.Errorf("module %q: %w", mod.Name, err)
	}
	if isSync {
		if err := r.runHook(ctx, item.Hooks.BeforeSync, "item", action.Describe(), "before_sync"); err != nil {
			return fmt.Errorf("module %q: %w", mod.Name, err)
		}
	}

	// --- snapshot destination before modification ---
	if snap != nil && (itemType == "file" || itemType == "directory") {
		dest := item.Destination.ForOS(r.OS)
		srcName := item.File
		if itemType == "directory" {
			srcName = item.Directory
		}
		if dest != "" && srcName != "" {
			destPath := filepath.Join(platform.ExpandPath(dest), filepath.Base(srcName))
			if err := snap.Record(destPath); err != nil {
				return fmt.Errorf("module %q: snapshot %s: %w", mod.Name, destPath, err)
			}
		}
	}

	// --- run ---
	fmt.Fprintf(r.Out, "  %s %s\n", color.Dim("->"), action.Describe())
	if fa, ok := action.(*actions.FileAction); ok && fa.Permissions != "" {
		if ps := fa.PermissionsStatus(); ps != "" {
			fmt.Fprintf(r.Out, "     %s\n", ps)
		}
	}

	runErr := action.Run(ctx, r.DryRun)

	outcome, errMsg := "success", ""
	if runErr != nil {
		outcome, errMsg = "failure", runErr.Error()
	}
	audit.Log(audit.Entry{Command: r.Command, Module: mod.Name, Item: action.Describe(), Outcome: outcome, Error: errMsg})

	if runErr != nil {
		return fmt.Errorf("module %q: %w", mod.Name, runErr)
	}

	// --- verify ---
	if item.Verify != "" && !r.DryRun {
		if err := shell.Run(ctx, item.Verify); err != nil {
			return fmt.Errorf("module %q: verify failed for %q: %w", mod.Name, action.Describe(), err)
		}
	}

	// --- item hooks: after ---
	if isSync {
		if err := r.runHook(ctx, item.Hooks.AfterSync, "item", action.Describe(), "after_sync"); err != nil {
			return fmt.Errorf("module %q: %w", mod.Name, err)
		}
	}
	if err := r.runHook(ctx, item.Hooks.AfterApply, "item", action.Describe(), "after_apply"); err != nil {
		return fmt.Errorf("module %q: %w", mod.Name, err)
	}

	return nil
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

func (r *Runner) buildAction(item config.Item) (actions.Action, bool, error) {
	switch item.Type() {
	case "package":
		if r.skipManager(item.Via) {
			return nil, true, nil
		}
		return &actions.PackageAction{Package: item.Package, Manager: item.Via}, false, nil

	case "script":
		return &actions.ScriptAction{Script: item.Script, Via: item.Via}, false, nil

	case "file":
		dest := item.Destination.ForOS(r.OS)
		if dest == "" {
			return nil, true, nil
		}
		return &actions.FileAction{
			Source:      item.File,
			Destination: dest,
			Direction:   r.fileDirection(item),
			Link:        item.Link,
			Permissions: item.Permissions,
			Encrypted:   item.Encrypted,
			AgeKey:      r.AgeKey,
		}, false, nil

	case "directory":
		dest := item.Destination.ForOS(r.OS)
		if dest == "" {
			return nil, true, nil
		}
		return &actions.DirectoryAction{
			Source:      item.Directory,
			Destination: dest,
			Direction:   r.fileDirection(item),
			Link:        item.Link,
			Permissions: item.Permissions,
		}, false, nil

	case "binary":
		sourceURL := item.Source.ForOS(r.OS)
		if sourceURL == "" {
			return nil, true, nil // no binary for this OS
		}
		installTo := item.InstallTo
		if installTo == "" {
			installTo = "~/.local/bin"
		}
		return &actions.BinaryAction{
			Name:      item.Binary,
			Version:   item.Version,
			SourceURL: sourceURL,
			InstallTo: installTo,
		}, false, nil

	case "run":
		return &actions.RunAction{Command: item.Run, After: item.After}, false, nil

	case "setting":
		return &actions.SettingAction{
			Domain: item.Setting,
			Key:    item.Key,
			Value:  item.Value,
		}, false, nil

	default:
		return nil, false, fmt.Errorf("item has no recognised type: %+v", item)
	}
}

// --- helpers -----------------------------------------------------------------

func (r *Runner) matchesTags(mod config.Module) bool {
	return tags.Matches(r.MachineTags, mod.OnlyTags, mod.ExcludeTags)
}

func (r *Runner) skipManager(manager string) bool {
	targetOS := platform.PackageManagerOS(manager)
	return targetOS != "" && targetOS != r.OS
}

func (r *Runner) runHook(ctx context.Context, cmd, scope, name, hookName string) error {
	if cmd == "" {
		return nil
	}
	if r.DryRun {
		fmt.Fprintf(r.Out, "  %s\n", color.Dim(fmt.Sprintf("[dry-run] hook %s.%s: %s", hookName, scope, cmd)))
		return nil
	}
	if r.Verbose {
		fmt.Fprintf(r.Out, "  %s\n", color.Dim(fmt.Sprintf("hook %s (%s %q)", hookName, scope, name)))
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
