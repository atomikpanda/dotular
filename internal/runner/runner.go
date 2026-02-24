package runner

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/atomikpanda/dotular/internal/actions"
	"github.com/atomikpanda/dotular/internal/config"
	"github.com/atomikpanda/dotular/internal/platform"
)

// Runner orchestrates applying config modules on the current platform.
type Runner struct {
	Config  config.Config
	DryRun  bool
	Verbose bool
	OS      string // runtime.GOOS value
	Out     io.Writer
}

// New creates a Runner for the current platform.
func New(cfg config.Config, dryRun, verbose bool) *Runner {
	return &Runner{
		Config:  cfg,
		DryRun:  dryRun,
		Verbose: verbose,
		OS:      platform.Current(),
		Out:     os.Stdout,
	}
}

// ApplyAll applies every module in order.
func (r *Runner) ApplyAll(ctx context.Context) error {
	for _, mod := range r.Config {
		if err := r.ApplyModule(ctx, mod); err != nil {
			return err
		}
	}
	return nil
}

// ApplyModule applies a single module.
func (r *Runner) ApplyModule(ctx context.Context, mod config.Module) error {
	fmt.Fprintf(r.Out, "\n==> %s\n", mod.Name)

	for _, item := range mod.Items {
		action, skip, err := r.buildAction(item)
		if err != nil {
			return fmt.Errorf("module %q: %w", mod.Name, err)
		}
		if skip {
			if r.Verbose {
				fmt.Fprintf(r.Out, "  skip (%s not applicable on %s)\n", item.Type(), r.OS)
			}
			continue
		}

		fmt.Fprintf(r.Out, "  -> %s\n", action.Describe())
		if err := action.Run(ctx, r.DryRun); err != nil {
			return fmt.Errorf("module %q: action failed: %w", mod.Name, err)
		}
	}
	return nil
}

// buildAction converts a config.Item into an Action.
// Returns (nil, true, nil) when the item should be skipped on the current OS.
func (r *Runner) buildAction(item config.Item) (actions.Action, bool, error) {
	switch item.Type() {
	case "package":
		if skip := r.skipManager(item.Via); skip {
			return nil, true, nil
		}
		return &actions.PackageAction{Package: item.Package, Manager: item.Via}, false, nil

	case "script":
		return &actions.ScriptAction{Script: item.Script, Via: item.Via}, false, nil

	case "file":
		dest := item.Destination.ForOS(r.OS)
		if dest == "" {
			return nil, true, nil // no destination for this OS
		}
		return &actions.FileAction{
			Source:      item.File,
			Destination: dest,
			Direction:   item.EffectiveDirection(),
			Link:        item.Link,
		}, false, nil

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

// skipManager returns true when the package manager is known to be unavailable
// on the current OS.
func (r *Runner) skipManager(manager string) bool {
	targetOS := platform.PackageManagerOS(manager)
	if targetOS == "" {
		return false // cross-platform manager, never skip
	}
	return targetOS != r.OS
}
