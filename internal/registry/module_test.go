package registry

import (
	"strings"
	"testing"
)

func TestTrustLevelString(t *testing.T) {
	tests := []struct {
		level TrustLevel
		want  string
	}{
		{Official, "official"},
		{GitHub, "github"},
		{External, "external"},
		{TrustLevel(99), "external"},
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
	if ref.Trust != Official {
		t.Errorf("Trust = %v, want Official", ref.Trust)
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
	if ref.Trust != Official {
		t.Errorf("Trust = %v, want Official", ref.Trust)
	}
}

func TestParseRefOfficialGitHub(t *testing.T) {
	ref := ParseRef("github.com/atomikpanda/dotular/modules/neovim@main")
	if ref.Host != "github.com" {
		t.Errorf("Host = %q", ref.Host)
	}
	if ref.Trust != Official {
		t.Errorf("Trust = %v, want Official", ref.Trust)
	}
	if ref.Version != "main" {
		t.Errorf("Version = %q", ref.Version)
	}
	if ref.FetchURL != "https://raw.githubusercontent.com/atomikpanda/dotular/main/modules/neovim.yaml" {
		t.Errorf("FetchURL = %q", ref.FetchURL)
	}
}

func TestParseRefGitHub(t *testing.T) {
	ref := ParseRef("github.com/user/repo@v1")
	if ref.Host != "github.com" {
		t.Errorf("Host = %q", ref.Host)
	}
	if ref.Trust != GitHub {
		t.Errorf("Trust = %v, want GitHub", ref.Trust)
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
	if ref.Trust != GitHub {
		t.Errorf("Trust = %v, want GitHub", ref.Trust)
	}
	if ref.FetchURL != "https://raw.githubusercontent.com/user/repo/main/modules/neovim.yaml" {
		t.Errorf("FetchURL = %q", ref.FetchURL)
	}
}

func TestParseRefCustomHost(t *testing.T) {
	ref := ParseRef("custom.host/path/to/module@v2")
	if ref.Trust != External {
		t.Errorf("Trust = %v, want External", ref.Trust)
	}
	// The version is recorded on the Ref but kept out of the fetch URL: no host
	// serves "<path>@<version>" as a real path.
	if ref.Version != "v2" {
		t.Errorf("Version = %q, want %q", ref.Version, "v2")
	}
	if ref.FetchURL != "https://custom.host/path/to/module" {
		t.Errorf("FetchURL = %q", ref.FetchURL)
	}
}

// An external host is fetched from a bare path, so a requested version cannot be
// honoured. That must fail loudly rather than quietly serving whatever the
// unversioned path returns.
func TestExternalRefRejectsExplicitVersion(t *testing.T) {
	ref := ParseRef("custom.host/path/to/module@v2")
	err := ref.checkVersionSupported()
	if err == nil {
		t.Fatal("checkVersionSupported() = nil, want an error for a versioned external ref")
	}
	for _, want := range []string{"custom.host/path/to/module@v2", "v2", "not supported"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q, want it to mention %q", err, want)
		}
	}
}

func TestExternalRefWithoutVersionIsAllowed(t *testing.T) {
	ref := ParseRef("custom.host/path/to/module")
	if ref.Trust != External {
		t.Fatalf("Trust = %v, want External", ref.Trust)
	}
	if err := ref.checkVersionSupported(); err != nil {
		t.Errorf("checkVersionSupported() = %v, want nil", err)
	}
	if ref.FetchURL != "https://custom.host/path/to/module" {
		t.Errorf("FetchURL = %q, want the bare path", ref.FetchURL)
	}
}

// GitHub refs encode the version as the git ref in the URL, so they stay valid.
func TestGitHubRefWithVersionIsAllowed(t *testing.T) {
	ref := ParseRef("github.com/user/repo@v1")
	if err := ref.checkVersionSupported(); err != nil {
		t.Errorf("checkVersionSupported() = %v, want nil", err)
	}
	if !strings.Contains(ref.FetchURL, "/v1/") {
		t.Errorf("FetchURL = %q, want the version in the path", ref.FetchURL)
	}
}
