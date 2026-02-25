package actions

import (
	"context"
	"testing"
)

func TestPackageActionDescribe(t *testing.T) {
	a := &PackageAction{Package: "neovim", Manager: "brew"}
	got := a.Describe()
	want := `install package "neovim" via brew`
	if got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}
}

func TestInstallArgs(t *testing.T) {
	tests := []struct {
		manager string
		pkg     string
		first   string
		errMsg  string
	}{
		{"brew", "git", "brew", ""},
		{"brew-cask", "firefox", "brew", ""},
		{"mas", "123", "mas", ""},
		{"winget", "Git.Git", "winget", ""},
		{"choco", "git", "choco", ""},
		{"scoop", "git", "scoop", ""},
		{"apt", "git", "sudo", ""},
		{"apt-get", "git", "sudo", ""},
		{"dnf", "git", "sudo", ""},
		{"yum", "git", "sudo", ""},
		{"pacman", "git", "sudo", ""},
		{"snap", "code", "sudo", ""},
		{"flatpak", "org.app", "flatpak", ""},
		{"nix", "git", "nix-env", ""},
		{"unknown-mgr", "pkg", "", "unknown package manager"},
	}
	for _, tt := range tests {
		t.Run(tt.manager, func(t *testing.T) {
			args, err := installArgs(tt.manager, tt.pkg)
			if tt.errMsg != "" {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if args[0] != tt.first {
				t.Errorf("first arg = %q, want %q", args[0], tt.first)
			}
		})
	}
}

func TestCheckArgs(t *testing.T) {
	tests := []struct {
		manager string
		wantNil bool
	}{
		{"brew", false},
		{"brew-cask", false},
		{"mas", false},
		{"winget", false},
		{"choco", false},
		{"scoop", false},
		{"apt", false},
		{"apt-get", false},
		{"dnf", false},
		{"yum", false},
		{"pacman", false},
		{"snap", false},
		{"flatpak", false},
		{"nix", true},
		{"unknown", true},
	}
	for _, tt := range tests {
		t.Run(tt.manager, func(t *testing.T) {
			args := checkArgs(tt.manager, "pkg")
			if tt.wantNil && args != nil {
				t.Errorf("expected nil for %q", tt.manager)
			}
			if !tt.wantNil && args == nil {
				t.Errorf("expected non-nil for %q", tt.manager)
			}
		})
	}
}

func TestPackageActionRunDryRun(t *testing.T) {
	a := &PackageAction{Package: "git", Manager: "brew"}
	if err := a.Run(context.Background(), true); err != nil {
		t.Errorf("dry run error: %v", err)
	}
}

func TestPackageActionRunUnknownManager(t *testing.T) {
	a := &PackageAction{Package: "git", Manager: "nonexistent"}
	err := a.Run(context.Background(), false)
	if err == nil {
		t.Error("expected error for unknown manager")
	}
}

func TestPackageActionRunDryRunUnknownManager(t *testing.T) {
	// Dry run still calls installArgs, which fails for unknown managers.
	a := &PackageAction{Package: "git", Manager: "nonexistent"}
	err := a.Run(context.Background(), true)
	if err == nil {
		t.Error("expected error for unknown manager even in dry run")
	}
}

func TestPackageActionIsAppliedNoCheck(t *testing.T) {
	// nix has no check command — should return false, nil.
	a := &PackageAction{Package: "git", Manager: "nix"}
	applied, err := a.IsApplied(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Error("expected false for manager with no check")
	}
}

func TestPackageActionIsAppliedMissingBinary(t *testing.T) {
	// Use a manager whose check binary won't exist — should return false, nil.
	a := &PackageAction{Package: "test-pkg", Manager: "pacman"}
	applied, err := a.IsApplied(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// pacman likely doesn't exist on macOS/other, so the exec will fail.
	if applied {
		t.Error("expected false when check binary is missing")
	}
}
