package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/atomikpanda/dotular/internal/actions"
	"github.com/atomikpanda/dotular/internal/ageutil"
	"github.com/atomikpanda/dotular/internal/audit"
	"github.com/atomikpanda/dotular/internal/color"
	"github.com/atomikpanda/dotular/internal/config"
	"github.com/atomikpanda/dotular/internal/fsutil"
	"github.com/atomikpanda/dotular/internal/platform"
	"github.com/atomikpanda/dotular/internal/registry"
	"github.com/atomikpanda/dotular/internal/runner"
	"github.com/atomikpanda/dotular/internal/scanner"
	"github.com/atomikpanda/dotular/internal/tags"
	"github.com/atomikpanda/dotular/internal/ui"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var (
	configFile string
	dryRun     bool
	verbose    bool
	noAtomic   bool
	noCache    bool
)

var (
	updateRegistryPins = registry.UpdatePins
	checkRegistryPins  = registry.CheckPins
)

// Exit statuses. 2 is the conventional status for a caller who invoked the CLI
// wrongly, so scripts can tell a typo from a genuine failure.
const (
	exitFailure = 1
	exitUsage   = 2
)

func main() {
	color.Init()
	root := buildRoot()
	if err := root.Execute(); err != nil {
		os.Exit(exitCode(err))
	}
}

// usageError marks a failure caused by how the command was invoked — an unknown
// subcommand, a bad flag value, a module name that isn't in the config — rather
// than by something going wrong while carrying the command out.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

func usageErrorf(format string, args ...any) error {
	return &usageError{err: fmt.Errorf(format, args...)}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ue *usageError
	if errors.As(err, &ue) {
		return exitUsage
	}
	return exitFailure
}

// requireSubcommand makes a parent that only hosts subcommands reject an
// unknown one. Cobra otherwise short-circuits a non-runnable command to
// flag.ErrHelp before Args validation ever runs, and ExecuteC turns ErrHelp
// into a nil error — so the bad argument is discarded and the process exits 0.
// The RunE is reached only once Args has passed, i.e. with no subcommand named.
func requireSubcommand(cmd *cobra.Command) *cobra.Command {
	cmd.Args = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}
		msg := fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())
		if s := cmd.SuggestionsFor(args[0]); len(s) > 0 {
			msg += fmt.Sprintf("\n\nDid you mean this?\n\t%s", strings.Join(s, "\n\t"))
		}
		return usageErrorf("%s", msg)
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	}
	return cmd
}

func buildRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "dotular",
		Short: "A modular, cross-platform dotfile manager",
		Long: `dotular manages dotfiles and system configuration across macOS, Windows,
and Linux using a single YAML file.`,
		SilenceUsage: true,
	}

	// Inherited by every subcommand via Command.FlagErrorFunc, so a bad flag
	// anywhere in the tree exits with the usage status.
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return &usageError{err: err}
	})

	root.PersistentFlags().StringVarP(&configFile, "config", "c", "dotular.yaml", "path to config file")
	root.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "print actions without executing them")
	root.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "show skipped items and extra output")
	root.PersistentFlags().BoolVar(&noAtomic, "no-atomic", false, "disable snapshot/rollback per module")
	root.PersistentFlags().BoolVar(&noCache, "no-cache", false, "re-fetch registry modules from the network")

	root.AddCommand(
		versionCmd(),
		initCmd(),
		addCmd(),
		applyCmd(),
		directionCmd("push", "Push repo files to the system (overrides direction on all file items)"),
		directionCmd("pull", "Pull system files back into the repo (overrides direction on all file items)"),
		directionCmd("sync", "Sync files bidirectionally, prompting on conflicts (overrides direction on all file items)"),
		listCmd(),
		statusCmd(),
		platformCmd(),
		verifyCmd(),
		encryptCmd(),
		decryptCmd(),
		tagCmd(),
		logCmd(),
		registryCmd(),
	)

	return requireSubcommand(root)
}

// --- version -----------------------------------------------------------------

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version of dotular",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("dotular %s (commit: %s, built: %s)\n", version, commit, date)
		},
	}
}

// loadConfig parses the raw config file without registry resolution.
func loadConfig() (config.Config, error) {
	cfg, err := config.Load(configFile)
	if err != nil {
		return config.Config{}, fmt.Errorf("load config %q: %w", configFile, err)
	}
	return cfg, nil
}

// taggedConfig retains the raw module order while carrying only modules that
// may be resolved for the current command.
type taggedConfig struct {
	raw         config.Config
	active      config.Config
	activeMask  []bool
	machineTags []string
}

func loadTaggedConfig(ignoreTags bool) (taggedConfig, error) {
	cfg, err := loadConfig()
	if err != nil {
		return taggedConfig{}, err
	}
	machine, err := tags.Load()
	if err != nil {
		return taggedConfig{}, err
	}

	selected := taggedConfig{
		raw:         cfg,
		active:      config.Config{Age: cfg.Age},
		activeMask:  make([]bool, len(cfg.Modules)),
		machineTags: append([]string(nil), machine.Tags...),
	}
	for i, mod := range cfg.Modules {
		active := ignoreTags || tags.Matches(machine.Tags, mod.OnlyTags, mod.ExcludeTags)
		selected.activeMask[i] = active
		if active {
			selected.active.Modules = append(selected.active.Modules, mod)
		}
	}
	return selected, nil
}

func resolveConfig(ctx context.Context, cfg config.Config) (config.Config, error) {
	u := ui.New(os.Stdout, os.Stderr)
	return registry.Resolve(ctx, cfg, configFile, registry.ResolveOptions{
		NoCache: noCache,
	}, u)
}

// loadAndResolveConfig resolves the complete config for commands that do not
// apply machine-tag policy.
func loadAndResolveConfig(ctx context.Context) (config.Config, error) {
	cfg, err := loadConfig()
	if err != nil {
		return config.Config{}, err
	}
	return resolveConfig(ctx, cfg)
}

func loadAndResolveTaggedConfig(
	ctx context.Context,
	ignoreTags bool,
	names []string,
) (config.Config, taggedConfig, error) {
	selected, err := loadTaggedConfig(ignoreTags)
	if err != nil {
		return config.Config{}, taggedConfig{}, err
	}
	if err := rejectInactiveNamedModules(selected, names); err != nil {
		return config.Config{}, taggedConfig{}, err
	}
	cfg, err := resolveConfig(ctx, selected.active)
	if err != nil {
		return config.Config{}, taggedConfig{}, err
	}
	return cfg, selected, nil
}

func newRunner(cfg config.Config) *runner.Runner {
	return runner.New(cfg, dryRun, verbose, !noAtomic)
}

func addIgnoreTagsFlag(cmd *cobra.Command, target *bool) {
	cmd.Flags().BoolVar(target, "ignore-tags", false, "run modules even when their tag filters do not match")
}

// --- add ---------------------------------------------------------------------

func addCmd() *cobra.Command {
	var link bool
	var direction string

	cmd := &cobra.Command{
		Use:   "add <path> [module]",
		Short: "Add a file or directory to a module",
		Long: `Adds a file or directory to a module. The path is the first argument;
the module name is optional — if omitted, dotular will try to infer it
from the registry or prompt you interactively. If the module doesn't exist
it is created. Copies (or symlinks with --link) the path into the module's
managed store and records it in the config YAML.`,
		Example: `  dotular add ~/.config/nvim nvim
  dotular add ~/.config/nvim/init.lua nvim --link
  dotular add ~/.zshrc shell --direction sync
  dotular add ~/.zshrc`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			if err := config.ValidateDirection(direction); err != nil {
				return usageErrorf("invalid --direction: %v", err)
			}
			cfg, err := loadConfig()
			if err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					return err
				}
				cfg = config.Config{}
			}

			var moduleName, srcPath string
			if len(args) >= 2 {
				moduleName = args[1]
			} else {
				srcPath = platform.ExpandPath(args[0])
				absSrcForInfer, inferErr := filepath.Abs(srcPath)
				if inferErr != nil {
					return fmt.Errorf("resolve path: %w", inferErr)
				}
				inferred, inferErr := inferModuleName(ctx, absSrcForInfer)
				if inferErr != nil {
					return inferErr
				}
				moduleName = inferred
			}

			if mod := cfg.Module(moduleName); mod != nil && mod.IsRegistry() {
				return fmt.Errorf("cannot add items to registry-backed module %q", moduleName)
			}
			if len(args) >= 2 {
				srcPath = platform.ExpandPath(args[0])
			}

			// Resolve the source to an absolute path.
			absSrc, err := filepath.Abs(srcPath)
			if err != nil {
				return fmt.Errorf("resolve path: %w", err)
			}

			info, err := os.Stat(absSrc)
			if err != nil {
				return fmt.Errorf("stat %q: %w", absSrc, err)
			}

			isDir := info.IsDir()
			baseName := filepath.Base(absSrc)

			// Determine where the config file lives so we can compute
			// the module store directory relative to it.
			cfgDir := filepath.Dir(configFile)
			if !filepath.IsAbs(cfgDir) {
				cfgDir, _ = filepath.Abs(cfgDir)
			}
			moduleDir := filepath.Join(cfgDir, moduleName)

			// Create the module store directory.
			if err := os.MkdirAll(moduleDir, 0o755); err != nil {
				return fmt.Errorf("create module directory: %w", err)
			}

			dest := filepath.Join(moduleDir, baseName)

			// Copy the file or directory into the store.
			if isDir {
				if err := fsutil.CopyDir(absSrc, dest); err != nil {
					return fmt.Errorf("copy directory: %w", err)
				}
			} else {
				if err := fsutil.CopyFile(absSrc, dest); err != nil {
					return fmt.Errorf("copy file: %w", err)
				}
			}

			// Determine the destination platform map — use the parent
			// directory of the source path as the destination for the
			// current platform.
			srcParent := filepath.Dir(absSrc)
			pmap := config.PlatformMap{}
			switch platform.Current() {
			case "darwin":
				pmap.MacOS = srcParent
			case "windows":
				pmap.Windows = srcParent
			case "linux":
				pmap.Linux = srcParent
			}

			// Build the new item. Leave Direction unset at the default so the
			// generated YAML does not restate it.
			item := config.Item{
				Destination: pmap,
				Link:        link,
			}
			if direction != config.DefaultDirection {
				item.Direction = direction
			}
			if isDir {
				item.Directory = baseName
			} else {
				item.File = baseName
			}

			// Find or create the module.
			mod := cfg.Module(moduleName)
			if mod == nil {
				cfg.Modules = append(cfg.Modules, config.Module{
					Name:  moduleName,
					Items: []config.Item{item},
				})
			} else {
				mod.Items = append(mod.Items, item)
			}

			// Write the config back.
			if err := config.Save(configFile, cfg); err != nil {
				return err
			}

			typeStr := "file"
			if isDir {
				typeStr = "directory"
			}
			u := ui.New(os.Stdout, os.Stderr)
			u.Success(fmt.Sprintf("added %s %q to module %q", typeStr, baseName, moduleName))
			u.Info(fmt.Sprintf("  store: %s", dest))
			u.Info(fmt.Sprintf("  config: %s", configFile))
			return nil
		},
	}

	cmd.Flags().BoolVar(&link, "link", false, "use symlink instead of copy at apply time")
	cmd.Flags().StringVar(&direction, "direction", config.DefaultDirection, "file direction: push, pull, or sync")
	return cmd
}

func inferModuleName(ctx context.Context, absPath string) (string, error) {
	u := ui.New(os.Stdout, os.Stderr)

	// Try registry-based inference.
	entries, err := registry.FetchIndex(ctx, u)
	if err == nil && len(entries) > 0 {
		var modules []registry.RemoteModule
		lockErr := registry.WithRegistryMutationLock(func() error {
			lock, err := registry.LoadLock(registry.LockPath(configFile))
			if err != nil {
				return err
			}
			for _, entry := range entries {
				mod, _, fetchErr := registry.Fetch(ctx, entry.Name, lock, registry.FetchOptions{NoCache: noCache}, u)
				if fetchErr == nil {
					modules = append(modules, *mod)
				}
			}
			return nil
		})
		if lockErr == nil && len(modules) > 0 {
			matches := scanner.MatchPath(absPath, modules, platform.Current(), platform.ExpandPath, actions.OSIsDir)
			if len(matches) == 1 {
				u.Info(fmt.Sprintf("Matched registry module: %s", matches[0].ModuleName))
				return matches[0].ModuleName, nil
			}
			if len(matches) > 1 {
				u.Info("Multiple registry modules match this path:")
				for _, m := range matches {
					u.Info(fmt.Sprintf("  - %s", m.ModuleName))
				}
			}
		}
	}

	// Prompt the user.
	if !isTerminal() {
		return "", fmt.Errorf("module name required when stdin is not a terminal; use: dotular add <path> <module>")
	}

	var name string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Module name").
				Description("Enter a name for the module").
				Value(&name),
		),
	)
	if err := form.Run(); err != nil {
		return "", err
	}
	if name == "" {
		return "", fmt.Errorf("module name cannot be empty")
	}
	return name, nil
}

// --- apply -------------------------------------------------------------------

func applyCmd() *cobra.Command {
	var ignoreTags bool
	cmd := &cobra.Command{
		Use:   "apply [module...]",
		Short: "Apply modules (all if none specified)",
		Example: `  dotular apply
  dotular apply homebrew "Visual Studio Code"
  dotular apply --dry-run
  dotular apply --no-atomic`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg, selected, err := loadAndResolveTaggedConfig(ctx, ignoreTags, args)
			if err != nil {
				return err
			}
			r := newRunner(cfg)
			r.MachineTags = selected.machineTags
			r.IgnoreTags = ignoreTags

			return applyNamedModules(ctx, r, cfg, selected, args)
		},
	}
	addIgnoreTagsFlag(cmd, &ignoreTags)
	return cmd
}

func inactiveModuleError(name string) error {
	return usageErrorf(
		"module %q is inactive: tag filters do not match; use --ignore-tags to override",
		name,
	)
}

func rejectInactiveNamedModules(selected taggedConfig, names []string) error {
	for _, name := range names {
		for i, active := range selected.activeMask {
			raw := selected.raw.Modules[i]
			if !active && (raw.Name == name || raw.From == name) {
				return inactiveModuleError(name)
			}
		}
	}
	return nil
}

// selectModules resolves module names against the materialised config or their
// raw aliases/refs, failing on the first unknown name.
func selectModules(cfg config.Config, selected taggedConfig, names []string) ([]config.Module, error) {
	if err := rejectInactiveNamedModules(selected, names); err != nil {
		return nil, err
	}

	mods := make([]config.Module, 0, len(names))
	for _, name := range names {
		if mod := cfg.Module(name); mod != nil {
			mods = append(mods, *mod)
			continue
		}

		resolvedIndex := 0
		found := false
		for i, active := range selected.activeMask {
			if !active {
				continue
			}
			if resolvedIndex >= len(cfg.Modules) {
				break
			}
			raw := selected.raw.Modules[i]
			if raw.Name == name || raw.From == name {
				mods = append(mods, cfg.Modules[resolvedIndex])
				found = true
				break
			}
			resolvedIndex++
		}
		if !found {
			return nil, usageErrorf("module %q not found in config", name)
		}
	}
	return mods, nil
}

// applyNamedModules applies the named modules, or every module when none are named.
func applyNamedModules(
	ctx context.Context,
	r *runner.Runner,
	cfg config.Config,
	selected taggedConfig,
	names []string,
) error {
	if len(names) == 0 {
		return r.ApplyAll(ctx)
	}
	mods, err := selectModules(cfg, selected, names)
	if err != nil {
		return err
	}
	for _, mod := range mods {
		if result := r.ApplyModule(ctx, mod); result.Err != nil {
			return result.Err
		}
	}
	return nil
}

// --- push / pull / sync ------------------------------------------------------

func directionCmd(direction, short string) *cobra.Command {
	var ignoreTags bool
	cmd := &cobra.Command{
		Use:   fmt.Sprintf("%s [module...]", direction),
		Short: short,
		Example: fmt.Sprintf(`  dotular %[1]s
  dotular %[1]s "Visual Studio Code"
  dotular %[1]s --dry-run`, direction),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg, selected, err := loadAndResolveTaggedConfig(ctx, ignoreTags, args)
			if err != nil {
				return err
			}
			r := newRunner(cfg)
			r.MachineTags = selected.machineTags
			r.IgnoreTags = ignoreTags
			r.Command = direction
			r.DirectionOverride = direction

			return applyNamedModules(ctx, r, cfg, selected, args)
		},
	}
	addIgnoreTagsFlag(cmd, &ignoreTags)
	return cmd
}

// --- list --------------------------------------------------------------------

func listCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all modules defined in the config",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			selected, err := loadTaggedConfig(false)
			if err != nil {
				return err
			}
			cfg, err := resolveConfig(ctx, selected.active)
			if err != nil {
				return err
			}

			u := ui.New(cmd.OutOrStdout(), cmd.ErrOrStderr())
			activeIndex := 0
			for i, raw := range selected.raw.Modules {
				if !selected.activeMask[i] {
					name := raw.Name
					if name == "" {
						name = raw.From
					}
					u.Info(fmt.Sprintf("%s  %s",
						color.Bold(fmt.Sprintf("%-30s", name)),
						color.Dim("skipped (tag mismatch)")))
					continue
				}
				if activeIndex >= len(cfg.Modules) {
					return fmt.Errorf("list modules: resolved module count does not match active config")
				}
				mod := cfg.Modules[activeIndex]
				activeIndex++
				counts := make(map[string]int)
				for _, item := range mod.Items {
					counts[item.Type()]++
				}
				total := len(mod.Items)
				breakdown := formatTypeCounts(counts)
				u.Info(fmt.Sprintf("%s  %s",
					color.Bold(fmt.Sprintf("%-30s", mod.Name)),
					color.Dim(fmt.Sprintf("%d items (%s)", total, breakdown))))
			}
			if activeIndex != len(cfg.Modules) {
				return fmt.Errorf("list modules: resolved module count does not match active config")
			}
			return nil
		},
	}
}

// formatTypeCounts formats a map of item type counts into a human-readable string.
func formatTypeCounts(counts map[string]int) string {
	types := []string{"package", "file", "directory", "script", "binary", "run", "setting"}
	var parts []string
	for _, t := range types {
		if n, ok := counts[t]; ok && n > 0 {
			label := t
			if n != 1 {
				label += "s"
			}
			parts = append(parts, fmt.Sprintf("%d %s", n, label))
		}
	}
	return strings.Join(parts, ", ")
}

// --- status ------------------------------------------------------------------

func statusCmd() *cobra.Command {
	var ignoreTags bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show what would be applied for the current platform",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg, selected, err := loadAndResolveTaggedConfig(ctx, ignoreTags, nil)
			if err != nil {
				return err
			}
			r := runner.New(cfg, true, true, false)
			r.MachineTags = selected.machineTags
			r.IgnoreTags = ignoreTags
			return r.ApplyAll(ctx)
		},
	}
	addIgnoreTagsFlag(cmd, &ignoreTags)
	return cmd
}

// --- platform ----------------------------------------------------------------

func platformCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "platform",
		Short: "Print the detected platform (OS)",
		Run: func(cmd *cobra.Command, args []string) {
			u := ui.New(os.Stdout, os.Stderr)
			u.Info(fmt.Sprintf("os: %s", platform.Current()))
		},
	}
}

// --- verify ------------------------------------------------------------------

// errVerifyFailed reports that the checks ran and some did not pass. Distinct
// from any other error verify can return, which means a check could not be run.
var errVerifyFailed = errors.New("some verify checks failed")

func verifyCmd() *cobra.Command {
	var ignoreTags bool
	cmd := &cobra.Command{
		Use:   "verify [module...]",
		Short: "Run verify checks without modifying anything",
		Example: `  dotular verify
  dotular verify "Visual Studio Code"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg, selected, err := loadAndResolveTaggedConfig(ctx, ignoreTags, args)
			if err != nil {
				return err
			}
			r := runner.New(cfg, false, verbose, false)
			r.MachineTags = selected.machineTags
			r.IgnoreTags = ignoreTags
			r.Command = "verify"

			var allPassed bool
			if len(args) == 0 {
				allPassed, err = r.VerifyAll(ctx)
				if err != nil {
					return err
				}
			} else {
				mods, err := selectModules(cfg, selected, args)
				if err != nil {
					return err
				}
				allPassed = true
				for _, mod := range mods {
					passed, verErr := r.VerifyModule(ctx, mod)
					if verErr != nil {
						return verErr
					}
					if !passed {
						allPassed = false
					}
				}
			}

			if !allPassed {
				return errVerifyFailed
			}
			return nil
		},
	}
	addIgnoreTagsFlag(cmd, &ignoreTags)
	return cmd
}

// --- encrypt / decrypt -------------------------------------------------------

func encryptCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "encrypt <file>",
		Short: "Encrypt a plaintext file with the configured age key (writes <file>.age)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src := args[0]
			// RepoPath is a no-op on a ".age" path, so the destination would be
			// the source and the file would be re-encrypted over itself.
			if strings.HasSuffix(src, ".age") {
				return fmt.Errorf("%s is already encrypted; encrypt writes <file>.age and would overwrite it in place", src)
			}
			key, err := keyFromConfig()
			if err != nil {
				return err
			}
			dst := ageutil.RepoPath(src)
			u := ui.New(os.Stdout, os.Stderr)
			u.Info(fmt.Sprintf("encrypting %s → %s", src, dst))
			return key.EncryptFile(src, dst)
		},
	}
}

func decryptCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "decrypt <file.age>",
		Short: "Decrypt an age-encrypted file (writes without the .age extension)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := keyFromConfig()
			if err != nil {
				return err
			}
			src := args[0]
			dst := strings.TrimSuffix(src, ".age")
			u := ui.New(os.Stdout, os.Stderr)
			u.Info(fmt.Sprintf("decrypting %s → %s", src, dst))
			return key.DecryptFile(src, dst)
		},
	}
}

func keyFromConfig() (*ageutil.Key, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	// Reuse runner's resolver so env vars are respected.
	r := runner.New(cfg, false, false, false)
	if r.AgeKey == nil {
		return nil, fmt.Errorf("no age key configured; set age.identity or age.passphrase in %s, or set DOTULAR_AGE_IDENTITY / DOTULAR_AGE_PASSPHRASE", configFile)
	}
	return r.AgeKey, nil
}

// --- tag ---------------------------------------------------------------------

func tagCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tag",
		Short: "Manage machine tags",
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "Print current machine tags",
			RunE: func(cmd *cobra.Command, args []string) error {
				if err := tags.EnsureInitialised(); err != nil {
					return err
				}
				cfg, err := tags.Load()
				if err != nil {
					return err
				}
				u := ui.New(os.Stdout, os.Stderr)
				u.Info(color.Bold(fmt.Sprintf("machine config: %s", tags.ConfigPath())))
				if len(cfg.Tags) == 0 {
					u.Info(color.Dim("(no tags)"))
					return nil
				}
				for _, t := range cfg.Tags {
					u.Info(fmt.Sprintf("  · %s", t))
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "add <tag>",
			Short: "Add a tag to this machine",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if err := tags.EnsureInitialised(); err != nil {
					return err
				}
				if err := tags.Add(args[0]); err != nil {
					return err
				}
				u := ui.New(os.Stdout, os.Stderr)
				u.Success(fmt.Sprintf("added tag %q", args[0]))
				return nil
			},
		},
	)
	return requireSubcommand(cmd)
}

// --- log ---------------------------------------------------------------------

func logCmd() *cobra.Command {
	var moduleFilter string
	var limit int

	cmd := &cobra.Command{
		Use:   "log",
		Short: "Show the audit log",
		Example: `  dotular log
  dotular log --module homebrew
  dotular log --limit 20`,
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := audit.Read(moduleFilter, limit)
			if err != nil {
				return fmt.Errorf("read audit log: %w", err)
			}
			u := ui.New(os.Stdout, os.Stderr)
			if len(entries) == 0 {
				u.Info("(no log entries)")
				return nil
			}

			headers := []string{"TIME", "COMMAND", "MODULE", "OUTCOME", "ITEM"}
			var rows [][]string
			for _, e := range entries {
				ts := e.Time.Local().Format(time.DateTime)
				outcome := e.Outcome
				if e.Error != "" {
					outcome += " (" + e.Error + ")"
				}
				// Pre-color outcome
				switch e.Outcome {
				case "success":
					outcome = color.Green(outcome)
				case "failure":
					outcome = color.BoldRed(outcome)
				case "skipped":
					outcome = color.Dim(outcome)
				}
				rows = append(rows, []string{ts, e.Command, e.Module, outcome, e.Item})
			}
			u.Table(headers, rows, nil)
			u.Info(fmt.Sprintf("\nlog: %s", audit.LogPath()))
			return nil
		},
	}

	cmd.Flags().StringVar(&moduleFilter, "module", "", "filter log by module name")
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of entries to show")
	return cmd
}

// --- registry ----------------------------------------------------------------

func writePinChanges(w io.Writer, changes []registry.PinChange) error {
	if len(changes) == 0 {
		return nil
	}

	if _, err := io.WriteString(w, "REF\tSTATUS\tOLD\tNEW\n"); err != nil {
		return err
	}

	for _, change := range changes {
		oldChecksum := change.OldSHA256
		if oldChecksum == "" {
			oldChecksum = "none"
		}

		if _, err := fmt.Fprintf(
			w,
			"%s\t%s\t%s\t%s\n",
			change.Ref,
			change.Status,
			oldChecksum,
			change.NewSHA256,
		); err != nil {
			return err
		}
	}

	return nil
}

func runRegistryUpdateCheck(
	ctx context.Context,
	stdout io.Writer,
	cfg *config.Config,
	configPath string,
) error {
	changes, err := checkRegistryPins(ctx, cfg, configPath)
	if len(changes) != 0 {
		if writeErr := writePinChanges(stdout, changes); writeErr != nil {
			return writeErr
		}
	}
	return err
}

func registryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "registry",
		Short: "Manage the local registry cache",
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List available registry modules",
		RunE: func(cmd *cobra.Command, args []string) error {
			cached, err := cmd.Flags().GetBool("cached")
			if err != nil {
				return err
			}
			u := ui.New(os.Stdout, os.Stderr)

			if cached {
				// Only the config path is needed to locate the lock file, so
				// this lists the cache even when dotular.yaml is absent.
				lock, err := registry.LoadLock(registry.LockPath(configFile))
				if err != nil {
					return err
				}
				if len(lock.Registry) == 0 {
					u.Info("(no cached registry modules)")
					return nil
				}
				headers := []string{"REF", "TRUST", "FETCHED"}
				var rows [][]string
				for ref, entry := range lock.Registry {
					ref := registry.ParseRef(ref)
					trustStr := ref.Trust.String()
					switch trustStr {
					case "official":
						trustStr = color.BoldGreen(trustStr)
					case "github":
						trustStr = color.Dim(trustStr)
					case "external":
						trustStr = color.Yellow(trustStr)
					}
					rows = append(rows, []string{
						ref.Raw,
						trustStr,
						entry.FetchedAt.Local().Format(time.DateTime),
					})
				}
				u.Table(headers, rows, nil)
				return nil
			}

			// Default: fetch and display remote index.
			ctx := context.Background()
			entries, err := registry.FetchIndex(ctx, u)
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				u.Info("(no modules in registry)")
				return nil
			}
			headers := []string{"NAME", "VERSION"}
			var rows [][]string
			for _, e := range entries {
				rows = append(rows, []string{e.Name, e.Version})
			}
			u.Table(headers, rows, nil)
			return nil
		},
	}
	listCmd.Flags().Bool("cached", false, "show locally cached modules instead of the remote index")

	var check bool
	updateCmd := &cobra.Command{
		Use:   "update",
		Short: "Re-fetch all registry modules referenced in the config",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.NoArgs(cmd, args); err != nil {
				return &usageError{err: err}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if check {
				return runRegistryUpdateCheck(ctx, out, &cfg, configFile)
			}
			u := ui.New(out, cmd.ErrOrStderr())
			changes, err := updateRegistryPins(ctx, cfg, configFile, u)
			for _, change := range changes {
				old := change.OldSHA256
				if old == "" {
					old = "none"
				}
				if _, writeErr := fmt.Fprintf(
					out,
					"%s\t%s\t%s\n",
					change.Ref,
					old,
					change.NewSHA256,
				); writeErr != nil {
					return writeErr
				}
			}
			return err
		},
	}
	updateCmd.Flags().BoolVar(&check, "check", false, "check registry pins without updating them")

	cmd.AddCommand(
		listCmd,
		&cobra.Command{
			Use:   "clear",
			Short: "Remove all cached registry modules",
			RunE: func(cmd *cobra.Command, args []string) error {
				if err := registry.ClearCache(); err != nil {
					return err
				}
				u := ui.New(os.Stdout, os.Stderr)
				u.Success("registry cache cleared")
				return nil
			},
		},
		updateCmd,
	)
	return requireSubcommand(cmd)
}

// --- init --------------------------------------------------------------------

func isTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func runPicker(results []scanner.ScanResult) ([]scanner.ScanResult, error) {
	options := make([]huh.Option[int], len(results))
	for i, r := range results {
		label := fmt.Sprintf("%s (%d/%d items matched)",
			r.Module.Name, len(r.MatchedItems), r.TotalItems)
		options[i] = huh.NewOption(label, i)
	}

	var selectedIndices []int

	// Pre-select full matches.
	for i, r := range results {
		if r.Score == 1.0 {
			selectedIndices = append(selectedIndices, i)
		}
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[int]().
				Title("Select modules to add").
				Options(options...).
				Value(&selectedIndices),
		),
	)

	if err := form.Run(); err != nil {
		return nil, err
	}

	var selected []scanner.ScanResult
	for _, idx := range selectedIndices {
		selected = append(selected, results[idx])
	}
	return selected, nil
}

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Scan this machine and suggest modules from the registry",
		Long: `Scans your machine for installed packages and config files, matches
them against the official module registry, and lets you pick which
modules to add to your dotular.yaml.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			u := ui.New(os.Stdout, os.Stderr)

			// Validate the config before registry fetches can mutate cache or lock state.
			cfg, loadErr := loadConfig()
			if loadErr != nil && !errors.Is(loadErr, fs.ErrNotExist) {
				return loadErr
			}

			// 1. Fetch the registry index.
			u.Info("Fetching module registry...")
			entries, err := registry.FetchIndex(ctx, u)
			if err != nil {
				return fmt.Errorf("fetch registry index: %w", err)
			}
			if len(entries) == 0 {
				u.Info("No modules found in registry.")
				return nil
			}

			// 2. Fetch all module definitions.
			var modules []registry.RemoteModule
			err = registry.WithRegistryMutationLock(func() error {
				lockPath := registry.LockPath(configFile)
				lock, err := registry.LoadLock(lockPath)
				if err != nil {
					return err
				}

				for _, entry := range entries {
					mod, _, fetchErr := registry.Fetch(ctx, entry.Name, lock, registry.FetchOptions{NoCache: noCache}, u)
					if fetchErr != nil {
						u.Warn(fmt.Sprintf("skipping %s: %v", entry.Name, fetchErr))
						continue
					}
					modules = append(modules, *mod)
				}
				if len(modules) == 0 {
					return nil
				}

				// Save updated lock file.
				if err := registry.SaveLock(lockPath, lock); err != nil {
					u.Warn(fmt.Sprintf("could not save lock file: %v", err))
				}
				return nil
			})
			if err != nil {
				return err
			}
			if len(modules) == 0 {
				u.Info("No modules could be fetched from registry.")
				return nil
			}

			// 3. Scan the machine.
			u.Info("Scanning installed software...")
			expand := platform.ExpandPath
			fileExists := func(path string) bool {
				_, err := os.Stat(path)
				return err == nil
			}
			pkgInstalled := func(manager, pkg string) bool {
				checkArgs := actions.CheckArgs(manager, pkg)
				if checkArgs == nil {
					return false
				}
				c := exec.CommandContext(ctx, checkArgs[0], checkArgs[1:]...)
				return c.Run() == nil
			}

			results := scanner.ScanInstalled(modules, platform.Current(), expand, fileExists, actions.OSIsDir, pkgInstalled)

			// Filter to results that have at least one match.
			var matched []scanner.ScanResult
			for _, r := range results {
				if len(r.MatchedItems) > 0 {
					matched = append(matched, r)
				}
			}

			if len(matched) == 0 {
				u.Info("No matching modules found on this machine.")
				return nil
			}

			// 4. Interactive picker or auto-select.
			var selected []scanner.ScanResult
			if isTerminal() {
				selected, err = runPicker(matched)
				if err != nil {
					return err
				}
			} else {
				// Non-interactive: auto-select full matches.
				for _, r := range matched {
					if r.Score == 1.0 {
						selected = append(selected, r)
						u.Info(fmt.Sprintf("auto-selected: %s (%d/%d items matched)",
							r.Module.Name, len(r.MatchedItems), r.TotalItems))
					}
				}
			}

			if len(selected) == 0 {
				u.Info("No modules selected.")
				return nil
			}

			// 5. Merge selections into the preflighted config.

			// Normalize existing from: refs for dedup comparison.
			existingURLs := make(map[string]bool)
			for _, mod := range cfg.Modules {
				if mod.From != "" {
					ref := registry.ParseRef(mod.From)
					existingURLs[ref.FetchURL] = true
				}
			}

			added := 0
			for _, r := range selected {
				fromRef := r.Module.Name // bare name expands to official registry
				ref := registry.ParseRef(fromRef)
				if existingURLs[ref.FetchURL] {
					u.Warn(fmt.Sprintf("skipping %s (already in config)", fromRef))
					continue
				}
				cfg.Modules = append(cfg.Modules, config.Module{
					From: fromRef,
				})
				added++
			}

			if added == 0 {
				u.Info("All selected modules are already in your config.")
				return nil
			}

			// 6. Write config.
			if err := config.Save(configFile, cfg); err != nil {
				return err
			}

			u.Success(fmt.Sprintf("Added %d module(s) to %s", added, configFile))
			u.Info(fmt.Sprintf("\nNext: run %s to apply", color.Bold("dotular apply")))
			return nil
		},
	}
}
