package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCurrent(t *testing.T) {
	got := Current()
	if got != runtime.GOOS {
		t.Errorf("Current() = %q, want %q", got, runtime.GOOS)
	}
}

func TestExpandPathTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}

	got := ExpandPath("~/Documents")
	want := filepath.Join(home, "Documents")
	if got != want {
		t.Errorf("ExpandPath(~/Documents) = %q, want %q", got, want)
	}
}

func TestExpandPathTildeAlone(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}

	got := ExpandPath("~")
	if got != home {
		t.Errorf("ExpandPath(~) = %q, want %q", got, home)
	}
}

func TestExpandPathEnvVar(t *testing.T) {
	t.Setenv("DOTULAR_TEST_VAR", "/custom/path")
	got := ExpandPath("$DOTULAR_TEST_VAR/sub")
	if got != "/custom/path/sub" {
		t.Errorf("ExpandPath($DOTULAR_TEST_VAR/sub) = %q", got)
	}
}

func TestExpandPathWindowsEnvVars(t *testing.T) {
	tests := []struct {
		name  string
		value string
		path  string
		want  string
	}{
		{"APPDATA", `C:\Users\test\AppData\Roaming`, `%APPDATA%\fastfetch`, `C:\Users\test\AppData\Roaming\fastfetch`},
		{"LOCALAPPDATA", `C:\Users\test\AppData\Local`, `%LOCALAPPDATA%\Google\Chrome`, `C:\Users\test\AppData\Local\Google\Chrome`},
		{"USERPROFILE", `C:\Users\test`, `%USERPROFILE%\.claude`, `C:\Users\test\.claude`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.name, tt.value)
			if got := ExpandPath(tt.path); got != tt.want {
				t.Errorf("ExpandPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestExpandPathWindowsEnvBoundaries(t *testing.T) {
	const missing = "DOTULAR_TEST_MISSING_WINDOWS_ENV"
	t.Setenv(missing, "temporary")
	if err := os.Unsetenv(missing); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		path string
	}{
		{"unknown variable", `%DOTULAR_TEST_MISSING_WINDOWS_ENV%\config`},
		{"unmatched literal percent", `100%\config`},
		{"paired literal percents", `100%%\config`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExpandPath(tt.path); got != tt.path {
				t.Errorf("ExpandPath(%q) = %q, want unchanged", tt.path, got)
			}
		})
	}
}

func TestExpandPathNoExpansion(t *testing.T) {
	got := ExpandPath("/absolute/path")
	if got != "/absolute/path" {
		t.Errorf("ExpandPath(/absolute/path) = %q", got)
	}
}

func TestPackageManagerOS(t *testing.T) {
	tests := []struct {
		manager string
		want    string
	}{
		{"brew", "darwin"},
		{"brew-cask", "darwin"},
		{"mas", "darwin"},
		{"winget", "windows"},
		{"choco", "windows"},
		{"scoop", "windows"},
		{"apt", "linux"},
		{"apt-get", "linux"},
		{"dnf", "linux"},
		{"yum", "linux"},
		{"pacman", "linux"},
		{"snap", "linux"},
		{"flatpak", ""},
		{"nix", ""},
		{"unknown", ""},
	}
	for _, tt := range tests {
		t.Run(tt.manager, func(t *testing.T) {
			if got := PackageManagerOS(tt.manager); got != tt.want {
				t.Errorf("PackageManagerOS(%q) = %q, want %q", tt.manager, got, tt.want)
			}
		})
	}
}
