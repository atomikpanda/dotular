package registry

import (
	"testing"
)

func TestTrustLevelString(t *testing.T) {
	tests := []struct {
		level TrustLevel
		want  string
	}{
		{Official, "official"},
		{Community, "community"},
		{Private, "private"},
		{TrustLevel(99), "private"},
	}
	for _, tt := range tests {
		if got := tt.level.String(); got != tt.want {
			t.Errorf("TrustLevel(%d).String() = %q, want %q", tt.level, got, tt.want)
		}
	}
}

func TestParseRefBare(t *testing.T) {
	ref := ParseRef("wezterm")
	if ref.Host != "github.com" {
		t.Errorf("Host = %q", ref.Host)
	}
	if ref.Version != "main" {
		t.Errorf("Version = %q", ref.Version)
	}
	if ref.Trust != Private {
		t.Errorf("Trust = %v", ref.Trust)
	}
	if ref.FetchURL == "" {
		t.Error("FetchURL should not be empty")
	}
}

func TestParseRefBareWithVersion(t *testing.T) {
	ref := ParseRef("wezterm@v1.0.0")
	if ref.Version != "v1.0.0" {
		t.Errorf("Version = %q", ref.Version)
	}
}

func TestParseRefOfficial(t *testing.T) {
	ref := ParseRef("dotular.dev/modules/neovim@1.0.0")
	if ref.Host != "dotular.dev" {
		t.Errorf("Host = %q", ref.Host)
	}
	if ref.Trust != Official {
		t.Errorf("Trust = %v, want Official", ref.Trust)
	}
	if ref.Version != "1.0.0" {
		t.Errorf("Version = %q", ref.Version)
	}
	if ref.FetchURL != "https://dotular.dev/modules/neovim/1.0.0.yaml" {
		t.Errorf("FetchURL = %q", ref.FetchURL)
	}
}

func TestParseRefOfficialLatest(t *testing.T) {
	ref := ParseRef("dotular.dev/modules/neovim")
	if ref.FetchURL != "https://dotular.dev/modules/neovim/latest.yaml" {
		t.Errorf("FetchURL = %q", ref.FetchURL)
	}
}

func TestParseRefCommunity(t *testing.T) {
	ref := ParseRef("dotular.dev/community/somemod")
	if ref.Trust != Community {
		t.Errorf("Trust = %v, want Community", ref.Trust)
	}
}

func TestParseRefGitHub(t *testing.T) {
	ref := ParseRef("github.com/user/repo@v1")
	if ref.Host != "github.com" {
		t.Errorf("Host = %q", ref.Host)
	}
	if ref.Trust != Private {
		t.Errorf("Trust = %v", ref.Trust)
	}
	if ref.Version != "v1" {
		t.Errorf("Version = %q", ref.Version)
	}
	// Simple form: user/repo
	if ref.FetchURL != "https://raw.githubusercontent.com/user/repo/v1/dotular-module.yaml" {
		t.Errorf("FetchURL = %q", ref.FetchURL)
	}
}

func TestParseRefGitHubExtended(t *testing.T) {
	ref := ParseRef("github.com/user/repo/modules/neovim@main")
	if ref.FetchURL != "https://raw.githubusercontent.com/user/repo/main/modules/neovim.yaml" {
		t.Errorf("FetchURL = %q", ref.FetchURL)
	}
}

func TestParseRefCustomHost(t *testing.T) {
	ref := ParseRef("custom.host/path/to/module@v2")
	if ref.Trust != Private {
		t.Errorf("Trust = %v", ref.Trust)
	}
	if ref.FetchURL != "https://custom.host/path/to/module@v2" {
		t.Errorf("FetchURL = %q", ref.FetchURL)
	}
}
