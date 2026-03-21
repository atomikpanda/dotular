package main

import (
	"context"
	"fmt"
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
	"github.com/atomikpanda/dotular/internal/platform"
	"github.com/atomikpanda/dotular/internal/registry"
	"github.com/atomikpanda/dotular/internal/runner"
	"github.com/atomikpanda/dotular/internal/scanner"
	"github.com/atomikpanda/dotular/internal/tags"
	"github.com/atomikpanda/dotular/internal/ui"
)

var (
	configFile string
	dryRun     bool
	verbose    bool
	noAtomic   bool
	noCache    bool
)

func main() {
	color.Init()
	root := buildRoot()
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func buildRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "dotular",
		Short: "A modular, cross-platform dotfile manager",
		Long: `dotular manages dotfiles and system configuration across macOS, Windows,
and Linux using a single YAML file.`,
		SilenceUsage: true,
	}

	root.PersistentFlags().StringVarP(&configFile, "config", "c", "dotular.yaml", "path to config file")
	root.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "print actions without executing them")
	root.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "show skipped items and extra output")
	root.PersistentFlags().BoolVar(&noAtomic, "no-atomic", false, "disable snapshot/rollback per module")
	root.PersistentFlags().BoolVar(&noCache, "no-cache", false, "re-fetch registry modules from the network")

	root.AddCommand(
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

	return root
}

// loadConfig parses the raw config file without registry resolution.
func loadConfig() (config.Config, error) {
	cfg, err := config.Load(configFile)
	if err != nil {
		return config.Config{}, fmt.Errorf("load config %q: %w", configFile, err)
	}
	return cfg, nil
}

// loadAndResolveConfig parses the config and resolves any registry module
// references, fetching remote modules and applying param/override logic.
func loadAndResolveConfig(ctx context.Context) (config.Config, error) {
	cfg, err := loadConfig()
	if err != nil {
		return config.Config{}, err
	}
	u := ui.New(os.Stdout, os.Stderr)
	return registry.Resolve(ctx, cfg, configFile, noCache, u)
}

func newRunner(cfg config.Config) *runner.Runner {
	return runner.New(cfg, dryRun, verbose, !noAtomic)
}

// --- add ---------------------------------------------------------------------

func addCmd() *cobra.Command {
	var link bool
	var direction string

	cmd := &cobra.Command{
		Use:   "add <module> <path>",
		Short: "Add a file or directory to a module",
		Long: `Adds a file or directory to a named module. If the module doesn't exist
it is created. Copies (or symlinks with --link) the path into the module's
managed store and records it in the config YAML.`,
		Example: `  dotular add nvim ~/.config/nvim
  dotular add nvim ~/.config/nvim/init.lua --link
  dotular add shell ~/.zshrc --direction sync`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			moduleName := args[0]
			srcPath := platform.ExpandPath(args[1])

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
				if err := copyDirRecursive(absSrc, dest); err != nil {
					return fmt.Errorf("copy directory: %w", err)
				}
			} else {
				if err := copyFileSimple(absSrc, dest); err != nil {
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

			// Load the existing config (or start fresh if it doesn't exist).
			cfg, err := loadConfig()
			if err != nil && !os.IsNotExist(err) {
				return err
			}

			// Build the new item.
			item := config.Item{
				Destination: pmap,
				Direction:   direction,
				Link:        link,
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
	cmd.Flags().StringVar(&direction, "direction", "push", "file direction: push, pull, or sync")
	return cmd
}

// copyFileSimple copies a single file from src to dst.
func copyFileSimple(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, info.Mode().Perm())
}

// copyDirRecursive copies a directory tree from src to dst.
func copyDirRecursive(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFileSimple(path, target)
	})
}

// --- apply -------------------------------------------------------------------

func applyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "apply [module...]",
		Short: "Apply modules (all if none specified)",
		Example: `  dotular apply
  dotular apply homebrew "Visual Studio Code"
  dotular apply --dry-run
  dotular apply --no-atomic`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg, err := loadAndResolveConfig(ctx)
			if err != nil {
				return err
			}
			r := newRunner(cfg)

			if len(args) == 0 {
				return r.ApplyAll(ctx)
			}
			for _, name := range args {
				mod := cfg.Module(name)
				if mod == nil {
					return fmt.Errorf("module %q not found in config", name)
				}
				result := r.ApplyModule(ctx, *mod)
				if result.Err != nil {
					return result.Err
				}
			}
			return nil
		},
	}
}

// --- push / pull / sync ------------------------------------------------------

func directionCmd(direction, short string) *cobra.Command {
	return &cobra.Command{
		Use:   fmt.Sprintf("%s [module...]", direction),
		Short: short,
		Example: fmt.Sprintf(`  dotular %[1]s
  dotular %[1]s "Visual Studio Code"
  dotular %[1]s --dry-run`, direction),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg, err := loadAndResolveConfig(ctx)
			if err != nil {
				return err
			}
			r := newRunner(cfg)
			r.Command = direction
			r.DirectionOverride = direction

			if len(args) == 0 {
				return r.ApplyAll(ctx)
			}
			for _, name := range args {
				mod := cfg.Module(name)
				if mod == nil {
					return fmt.Errorf("module %q not found in config", name)
				}
				result := r.ApplyModule(ctx, *mod)
				if result.Err != nil {
					return result.Err
				}
			}
			return nil
		},
	}
}

// --- list --------------------------------------------------------------------

func listCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all modules defined in the config",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg, err := loadAndResolveConfig(ctx)
			if err != nil {
				return err
			}
			u := ui.New(os.Stdout, os.Stderr)
			for _, mod := range cfg.Modules {
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
	return &cobra.Command{
		Use:   "status",
		Short: "Show what would be applied for the current platform",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg, err := loadAndResolveConfig(ctx)
			if err != nil {
				return err
			}
			r := runner.New(cfg, true, true, false)
			return r.ApplyAll(ctx)
		},
	}
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

func verifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify [module...]",
		Short: "Run verify checks without modifying anything",
		Example: `  dotular verify
  dotular verify "Visual Studio Code"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg, err := loadAndResolveConfig(ctx)
			if err != nil {
				return err
			}
			r := runner.New(cfg, false, verbose, false)
			r.Command = "verify"

			var allPassed bool
			if len(args) == 0 {
				allPassed, err = r.VerifyAll(ctx)
			} else {
				allPassed = true
				for _, name := range args {
					mod := cfg.Module(name)
					if mod == nil {
						return fmt.Errorf("module %q not found in config", name)
					}
					passed, verErr := r.VerifyModule(ctx, *mod)
					if verErr != nil {
						return verErr
					}
					if !passed {
						allPassed = false
					}
				}
			}

			if err != nil {
				return err
			}
			if !allPassed {
				u := ui.New(os.Stdout, os.Stderr)
				u.Warn("some verify checks failed")
				os.Exit(1)
			}
			return nil
		},
	}
}

// --- encrypt / decrypt -------------------------------------------------------

func encryptCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "encrypt <file>",
		Short: "Encrypt a file with the configured age key (writes <file>.age)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := keyFromConfig()
			if err != nil {
				return err
			}
			src := args[0]
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
			dst := src
			if len(dst) > 4 && dst[len(dst)-4:] == ".age" {
				dst = dst[:len(dst)-4]
			}
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
	return cmd
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

func registryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "registry",
		Short: "Manage the local registry cache",
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List cached registry modules",
			RunE: func(cmd *cobra.Command, args []string) error {
				_, err := loadConfig()
				if err != nil {
					return err
				}
				lockPath := registry.LockPath(configFile)
				lock, err := registry.LoadLock(lockPath)
				if err != nil {
					return err
				}
				u := ui.New(os.Stdout, os.Stderr)
				if len(lock.Registry) == 0 {
					u.Info("(no cached registry modules)")
					return nil
				}
				headers := []string{"REF", "TRUST", "FETCHED"}
				var rows [][]string
				for ref, entry := range lock.Registry {
					ref := registry.ParseRef(ref)
					trustStr := ref.Trust.String()
					// Pre-color trust
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
			},
		},
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
		&cobra.Command{
			Use:   "update",
			Short: "Re-fetch all registry modules referenced in the config",
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx := context.Background()
				// Force re-fetch by passing noCache=true.
				cfg, err := loadConfig()
				if err != nil {
					return err
				}
				u := ui.New(os.Stdout, os.Stderr)
				_, err = registry.Resolve(ctx, cfg, configFile, true, u)
				if err != nil {
					return err
				}
				u.Success("registry modules updated")
				return nil
			},
		},
	)
	return cmd
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
			lockPath := registry.LockPath(configFile)
			lock, err := registry.LoadLock(lockPath)
			if err != nil {
				return err
			}

			var modules []registry.RemoteModule
			for _, entry := range entries {
				mod, _, fetchErr := registry.Fetch(ctx, entry.Name, lock, noCache, u)
				if fetchErr != nil {
					u.Warn(fmt.Sprintf("skipping %s: %v", entry.Name, fetchErr))
					continue
				}
				modules = append(modules, *mod)
			}
			if len(modules) == 0 {
				u.Info("No modules could be fetched from registry.")
				return nil
			}

			// Save updated lock file.
			if err := registry.SaveLock(lockPath, lock); err != nil {
				u.Warn(fmt.Sprintf("could not save lock file: %v", err))
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

			results := scanner.ScanInstalled(modules, platform.Current(), expand, fileExists, pkgInstalled)

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

			// 5. Load or create config, merge selections.
			cfg, loadErr := loadConfig()
			if loadErr != nil && !os.IsNotExist(loadErr) {
				return loadErr
			}

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
