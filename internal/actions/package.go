package actions

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// PackageAction installs a package via the specified package manager.
type PackageAction struct {
	Package string
	Manager string // e.g. "brew", "winget", "apt"
}

func (a *PackageAction) Describe() string {
	return fmt.Sprintf("install package %q via %s", a.Package, a.Manager)
}

func (a *PackageAction) Run(ctx context.Context, dryRun bool) error {
	args, err := installArgs(a.Manager, a.Package)
	if err != nil {
		return err
	}
	if dryRun {
		fmt.Printf("    [dry-run] %s %s\n", args[0], strings.Join(args[1:], " "))
		return nil
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// installArgs returns the command + arguments needed to install pkg with the given manager.
func installArgs(manager, pkg string) ([]string, error) {
	switch manager {
	case "brew":
		return []string{"brew", "install", pkg}, nil
	case "brew-cask":
		return []string{"brew", "install", "--cask", pkg}, nil
	case "mas":
		return []string{"mas", "install", pkg}, nil
	case "winget":
		return []string{"winget", "install", "--id", pkg, "-e", "--accept-source-agreements"}, nil
	case "choco":
		return []string{"choco", "install", pkg, "-y"}, nil
	case "scoop":
		return []string{"scoop", "install", pkg}, nil
	case "apt", "apt-get":
		return []string{"sudo", "apt-get", "install", "-y", pkg}, nil
	case "dnf":
		return []string{"sudo", "dnf", "install", "-y", pkg}, nil
	case "yum":
		return []string{"sudo", "yum", "install", "-y", pkg}, nil
	case "pacman":
		return []string{"sudo", "pacman", "-S", "--noconfirm", pkg}, nil
	case "snap":
		return []string{"sudo", "snap", "install", pkg}, nil
	case "flatpak":
		return []string{"flatpak", "install", "-y", pkg}, nil
	case "nix":
		return []string{"nix-env", "-iA", pkg}, nil
	default:
		return nil, fmt.Errorf("unknown package manager: %q", manager)
	}
}
