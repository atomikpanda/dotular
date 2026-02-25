package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Current returns the runtime.GOOS value ("darwin", "windows", "linux", …).
func Current() string {
	return runtime.GOOS
}

// ExpandPath expands a leading "~/" and environment variables in path.
func ExpandPath(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	return os.ExpandEnv(path)
}

// PackageManagerOS maps a package manager name to the OS it runs on.
// Returns "" when the manager is not OS-specific (always available).
func PackageManagerOS(manager string) string {
	switch manager {
	case "brew", "brew-cask", "mas":
		return "darwin"
	case "winget", "choco", "scoop":
		return "windows"
	case "apt", "apt-get", "dnf", "yum", "pacman", "snap":
		return "linux"
	default:
		return "" // cross-platform (nix, flatpak, etc.) – let the runner decide
	}
}
