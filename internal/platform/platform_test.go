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
