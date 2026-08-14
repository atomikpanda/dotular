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

// ExpandPath expands a leading "~/", $VAR, ${VAR}, and Windows-style %VAR%.
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

	path = os.ExpandEnv(path)
	var expanded strings.Builder
	scan, copied := 0, 0
	changed := false
	for scan < len(path) {
		startOffset := strings.IndexByte(path[scan:], '%')
		if startOffset < 0 {
			break
		}
		start := scan + startOffset
		endOffset := strings.IndexByte(path[start+1:], '%')
		if endOffset < 0 {
			break
		}
		end := start + 1 + endOffset
		if value, ok := os.LookupEnv(path[start+1 : end]); ok {
			if !changed {
				expanded.Grow(len(path))
				changed = true
			}
			expanded.WriteString(path[copied:start])
			expanded.WriteString(value)
			copied = end + 1
		}
		scan = end + 1
	}
	if !changed {
		return path
	}
	expanded.WriteString(path[copied:])
	return expanded.String()
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
