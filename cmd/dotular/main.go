package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/atomikpanda/dotular/internal/config"
	"github.com/atomikpanda/dotular/internal/platform"
	"github.com/atomikpanda/dotular/internal/runner"
)

var (
	configFile string
	dryRun     bool
	verbose    bool
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

	root.AddCommand(
		applyCmd(),
		listCmd(),
		statusCmd(),
		platformCmd(),
	)

	return root
}

// loadConfig loads the YAML config from the --config path.
func loadConfig() (config.Config, error) {
	cfg, err := config.Load(configFile)
	if err != nil {
		return nil, fmt.Errorf("load config %q: %w", configFile, err)
	}
	return cfg, nil
}

// applyCmd applies one or more modules (all modules when no names are given).
func applyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "apply [module...]",
		Short: "Apply modules (all if none specified)",
		Example: `  dotular apply
  dotular apply homebrew "Visual Studio Code"
  dotular apply --dry-run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			r := runner.New(cfg, dryRun, verbose)
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

// listCmd prints every module and its item count.
func listCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all modules defined in the config",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			for _, mod := range cfg {
				fmt.Fprintf(os.Stdout, "%-30s  %d item(s)\n", mod.Name, len(mod.Items))
			}
			return nil
		},
	}
}

// statusCmd does a verbose dry-run to show what would happen.
func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show what would be applied for the current platform",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			// Always verbose + dry-run so every item is described.
			r := runner.New(cfg, true, true)
			return r.ApplyAll(context.Background())
		},
	}
}

// platformCmd prints the detected platform information.
func platformCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "platform",
		Short: "Print the detected platform (OS)",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(os.Stdout, "os: %s\n", platform.Current())
		},
	}
}
