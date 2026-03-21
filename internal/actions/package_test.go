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
		pkg     string
		wantNil bool
		want0   string
	}{
		{"brew", "git", false, "brew"},
		{"brew-cask", "wezterm", false, "brew"},
		{"apt", "curl", false, "dpkg"},
		{"unknown-mgr", "foo", true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.manager+"/"+tt.pkg, func(t *testing.T) {
			got := CheckArgs(tt.manager, tt.pkg)
			if tt.wantNil {
				if got != nil {
					t.Errorf("CheckArgs(%q, %q) = %v, want nil", tt.manager, tt.pkg, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("CheckArgs(%q, %q) = nil, want non-nil", tt.manager, tt.pkg)
			}
			if got[0] != tt.want0 {
				t.Errorf("CheckArgs(%q, %q)[0] = %q, want %q", tt.manager, tt.pkg, got[0], tt.want0)
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
	// unknown manager has no check command — should return false, nil.
	a := &PackageAction{Package: "git", Manager: "unknown"}
	applied, err := a.IsApplied(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Error("expected false for manager with no check")
	}
}

func TestCheckArgsNix(t *testing.T) {
	args := CheckArgs("nix", "nixpkgs.git")
	if args == nil {
		t.Fatal("expected non-nil check args for nix")
	}
	if args[0] != "nix-env" {
		t.Errorf("first arg = %q, want %q", args[0], "nix-env")
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
