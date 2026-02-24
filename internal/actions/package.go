package actions

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// PackageAction installs a package via the specified package manager.
//
// Idempotency: PackageAction implements Idempotent. IsApplied queries the
// package manager (e.g. `brew list`, `winget list`) to check whether the
// package is already installed. If the check command is unavailable the
// query is skipped and the install proceeds normally.
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

// IsApplied returns true when the package is already installed according to
// the package manager. Returns (false, nil) when the check is unsupported.
func (a *PackageAction) IsApplied(ctx context.Context) (bool, error) {
	args := checkArgs(a.Manager, a.Package)
	if args == nil {
		return false, nil // no check available for this manager
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	if _, ok := err.(*exec.ExitError); ok {
		return false, nil // non-zero = not installed
	}
	// The check binary itself could not be executed — don't block the install.
	return false, nil
}

// checkArgs returns the command to test whether a package is installed.
// Returns nil when no check is defined for the manager.
func checkArgs(manager, pkg string) []string {
	switch manager {
	case "brew":
		return []string{"brew", "list", "--formula", pkg}
	case "brew-cask":
		return []string{"brew", "list", "--cask", pkg}
	case "mas":
		return []string{"mas", "list"} // imprecise but side-effect free
	case "winget":
		return []string{"winget", "list", "--id", pkg, "-e"}
	case "choco":
		return []string{"choco", "list", "--local-only", pkg}
	case "scoop":
		return []string{"scoop", "info", pkg}
	case "apt", "apt-get":
		return []string{"dpkg", "-s", pkg}
	case "dnf":
		return []string{"rpm", "-q", pkg}
	case "yum":
		return []string{"rpm", "-q", pkg}
	case "pacman":
		return []string{"pacman", "-Q", pkg}
	case "snap":
		return []string{"snap", "list", pkg}
	case "flatpak":
		return []string{"flatpak", "info", pkg}
	default:
		return nil
	}
}

// installArgs returns the command + arguments needed to install pkg.
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
