package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/atomikpanda/dotular/internal/ageutil"
	"github.com/atomikpanda/dotular/internal/audit"
	"github.com/atomikpanda/dotular/internal/config"
	"github.com/atomikpanda/dotular/internal/platform"
	"github.com/atomikpanda/dotular/internal/runner"
	"github.com/atomikpanda/dotular/internal/tags"
)

var (
	configFile string
	dryRun     bool
	verbose    bool
	noAtomic   bool
)

func main() {
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

	root.AddCommand(
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
	)

	return root
}

func loadConfig() (config.Config, error) {
	cfg, err := config.Load(configFile)
	if err != nil {
		return config.Config{}, fmt.Errorf("load config %q: %w", configFile, err)
	}
	return cfg, nil
}

func newRunner(cfg config.Config) *runner.Runner {
	return runner.New(cfg, dryRun, verbose, !noAtomic)
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
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			r := newRunner(cfg)
			ctx := context.Background()

			if len(args) == 0 {
				return r.ApplyAll(ctx)
			}
			for _, name := range args {
				mod := cfg.Module(name)
				if mod == nil {
					return fmt.Errorf("module %q not found in config", name)
				}
				if err := r.ApplyModule(ctx, *mod); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// --- push / pull / sync ------------------------------------------------------

// directionCmd builds a push, pull, or sync command. All three work like
// apply but set DirectionOverride so every non-link file item uses the given
// direction regardless of what is declared in the YAML.
func directionCmd(direction, short string) *cobra.Command {
	return &cobra.Command{
		Use:   fmt.Sprintf("%s [module...]", direction),
		Short: short,
		Example: fmt.Sprintf(`  dotular %[1]s
  dotular %[1]s "Visual Studio Code"
  dotular %[1]s --dry-run`, direction),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			r := newRunner(cfg)
			r.Command = direction
			r.DirectionOverride = direction
			ctx := context.Background()

			if len(args) == 0 {
				return r.ApplyAll(ctx)
			}
			for _, name := range args {
				mod := cfg.Module(name)
				if mod == nil {
					return fmt.Errorf("module %q not found in config", name)
				}
				if err := r.ApplyModule(ctx, *mod); err != nil {
					return err
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
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			for _, mod := range cfg.Modules {
				fmt.Fprintf(os.Stdout, "%-30s  %d item(s)\n", mod.Name, len(mod.Items))
			}
			return nil
		},
	}
}

// --- status ------------------------------------------------------------------

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show what would be applied for the current platform",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			r := runner.New(cfg, true, true, false)
			return r.ApplyAll(context.Background())
		},
	}
}

// --- platform ----------------------------------------------------------------

func platformCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "platform",
		Short: "Print the detected platform (OS)",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(os.Stdout, "os: %s\n", platform.Current())
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
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			r := runner.New(cfg, false, verbose, false)
			r.Command = "verify"
			ctx := context.Background()

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
				fmt.Fprintln(os.Stderr, "\nsome verify checks failed")
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
			fmt.Printf("encrypting %s -> %s\n", src, dst)
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
			fmt.Printf("decrypting %s -> %s\n", src, dst)
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
				fmt.Printf("machine config: %s\n", tags.ConfigPath())
				if len(cfg.Tags) == 0 {
					fmt.Println("(no tags)")
					return nil
				}
				for _, t := range cfg.Tags {
					fmt.Printf("  - %s\n", t)
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
				fmt.Printf("added tag %q\n", args[0])
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
			if len(entries) == 0 {
				fmt.Println("(no log entries)")
				return nil
			}

			fmt.Printf("%-20s  %-8s  %-20s  %-8s  %s\n",
				"TIME", "COMMAND", "MODULE", "OUTCOME", "ITEM")
			fmt.Println(repeatStr("-", 90))
			for _, e := range entries {
				ts := e.Time.Local().Format(time.DateTime)
				outcome := e.Outcome
				if e.Error != "" {
					outcome += " (" + e.Error + ")"
				}
				fmt.Printf("%-20s  %-8s  %-20s  %-8s  %s\n",
					ts, e.Command, e.Module, outcome, e.Item)
			}
			fmt.Printf("\nlog: %s\n", audit.LogPath())
			return nil
		},
	}

	cmd.Flags().StringVar(&moduleFilter, "module", "", "filter log by module name")
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of entries to show")
	return cmd
}

// repeatStr returns s repeated n times.
func repeatStr(s string, n int) string {
	b := make([]byte, n*len(s))
	for i := range b {
		b[i] = s[i%len(s)]
	}
	return string(b)
}
