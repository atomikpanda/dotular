package scanner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/atomikpanda/dotular/internal/actions"
	"github.com/atomikpanda/dotular/internal/config"
	"github.com/atomikpanda/dotular/internal/platform"
	"github.com/atomikpanda/dotular/internal/registry"
)

// noDir is an IsDirFunc for tests whose fake paths never exist on disk.
func noDir(string) bool { return false }

// TestScannerAgreesWithActionTargets asserts the path ScanInstalled probes is
// exactly the path the runner's action would manage. The two used to resolve
// destinations independently and drifted (#48), so this compares them directly
// rather than checking each side against its own expectation.
func TestScannerAgreesWithActionTargets(t *testing.T) {
	tmp := t.TempDir()
	for _, d := range []string{"nvim.d", "plain", "config", "config/nvim"} {
		if err := os.MkdirAll(filepath.Join(tmp, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name string
		item config.Item
		want string
	}{
		{
			name: "file: existing directory with dotted name",
			item: config.Item{File: "init.lua", Destination: config.PlatformMap{Linux: filepath.Join(tmp, "nvim.d")}},
			want: filepath.Join(tmp, "nvim.d", "init.lua"),
		},
		{
			name: "file: existing directory with plain name",
			item: config.Item{File: "init.lua", Destination: config.PlatformMap{Linux: filepath.Join(tmp, "plain")}},
			want: filepath.Join(tmp, "plain", "init.lua"),
		},
		{
			name: "file: non-existent destination with extension is a complete path",
			item: config.Item{File: "wezterm.lua", Destination: config.PlatformMap{Linux: filepath.Join(tmp, "gone", ".wezterm.lua")}},
			want: filepath.Join(tmp, "gone", ".wezterm.lua"),
		},
		{
			name: "file: trailing slash forces directory treatment",
			item: config.Item{File: "wezterm.lua", Destination: config.PlatformMap{Linux: filepath.Join(tmp, ".wezterm.lua") + "/"}},
			want: filepath.Join(tmp, ".wezterm.lua", "wezterm.lua"),
		},
		{
			name: "directory: destination already ends with source basename",
			item: config.Item{Directory: "nvim", Destination: config.PlatformMap{Linux: filepath.Join(tmp, "config", "nvim")}},
			want: filepath.Join(tmp, "config", "nvim"),
		},
		{
			name: "directory: destination is the parent",
			item: config.Item{Directory: "nvim", Destination: config.PlatformMap{Linux: filepath.Join(tmp, "config")}},
			want: filepath.Join(tmp, "config", "nvim"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var probed string
			fileExists := func(path string) bool {
				probed = path
				return false
			}
			mod := registry.RemoteModule{Name: "mod", Items: []config.Item{tt.item}}
			ScanInstalled([]registry.RemoteModule{mod}, "linux", platform.ExpandPath,
				fileExists, actions.OSIsDir, func(string, string) bool { return false })

			// The runner prefixes the source with the module name.
			dest := tt.item.Destination.ForOS("linux")
			var fromAction string
			if tt.item.File != "" {
				a := &actions.FileAction{Source: filepath.Join("mod", tt.item.File), Destination: dest}
				fromAction = a.ResolvedTarget()
			} else {
				a := &actions.DirectoryAction{Source: filepath.Join("mod", tt.item.Directory), Destination: dest}
				fromAction = a.ResolvedTarget()
			}

			if probed != fromAction {
				t.Errorf("scanner probed %q, action resolves %q", probed, fromAction)
			}
			if fromAction != tt.want {
				t.Errorf("resolved target = %q, want %q", fromAction, tt.want)
			}
		})
	}
}

// TestScanInstalledNoFalsePositive is the reproduction from #48: an existing
// dotted directory and an existing parent directory must not score as adopted
// when neither managed path exists.
func TestScanInstalledNoFalsePositive(t *testing.T) {
	tmp := t.TempDir()
	for _, d := range []string{"nvim.d", "config"} {
		if err := os.MkdirAll(filepath.Join(tmp, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	mod := registry.RemoteModule{
		Name: "nvim",
		Items: []config.Item{
			{File: "init.lua", Destination: config.PlatformMap{Linux: filepath.Join(tmp, "nvim.d")}},
			{Directory: "nvim", Destination: config.PlatformMap{Linux: filepath.Join(tmp, "config")}},
		},
	}
	fileExists := func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	}

	results := ScanInstalled([]registry.RemoteModule{mod}, "linux", platform.ExpandPath,
		fileExists, actions.OSIsDir, func(string, string) bool { return false })

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].TotalItems != 2 {
		t.Errorf("TotalItems = %d, want 2", results[0].TotalItems)
	}
	if len(results[0].MatchedItems) != 0 {
		t.Errorf("matched = %d, want 0: %+v", len(results[0].MatchedItems), results[0].MatchedItems)
	}
	if results[0].Score != 0.0 {
		t.Errorf("Score = %.2f, want 0.00", results[0].Score)
	}
}

func TestMatchPath(t *testing.T) {
	modules := []registry.RemoteModule{
		{
			Name: "wezterm",
			Items: []config.Item{
				{
					File:        "wezterm.lua",
					Destination: config.PlatformMap{MacOS: "~/.wezterm.lua", Linux: "~/.wezterm.lua"},
				},
			},
		},
		{
			Name: "nvim",
			Items: []config.Item{
				{
					Directory:   "nvim",
					Destination: config.PlatformMap{MacOS: "~/.config/nvim", Linux: "~/.config/nvim"},
				},
			},
		},
	}

	home := "/Users/testuser"
	expand := func(p string) string {
		if len(p) > 1 && p[:2] == "~/" {
			return home + p[1:]
		}
		return p
	}

	// Exact file match (destination has extension = complete path)
	results := MatchPath(home+"/.wezterm.lua", modules, "darwin", expand, noDir)
	if len(results) != 1 {
		t.Fatalf("expected 1 match, got %d", len(results))
	}
	if results[0].ModuleName != "wezterm" {
		t.Errorf("ModuleName = %q", results[0].ModuleName)
	}

	// Prefix match (file under directory destination)
	results = MatchPath(home+"/.config/nvim/init.lua", modules, "darwin", expand, noDir)
	if len(results) != 1 {
		t.Fatalf("expected 1 match for prefix, got %d", len(results))
	}
	if results[0].ModuleName != "nvim" {
		t.Errorf("ModuleName = %q", results[0].ModuleName)
	}

	// No match
	results = MatchPath("/some/other/path", modules, "darwin", expand, noDir)
	if len(results) != 0 {
		t.Errorf("expected 0 matches, got %d", len(results))
	}

	// Wrong OS — wezterm has no windows destination
	results = MatchPath(home+"/.wezterm.lua", modules, "windows", expand, noDir)
	if len(results) != 0 {
		t.Errorf("expected 0 matches for wrong OS, got %d", len(results))
	}
}

func TestScanInstalled(t *testing.T) {
	modules := []registry.RemoteModule{
		{
			Name: "wezterm",
			Items: []config.Item{
				{Package: "wezterm", Via: "brew-cask"},
				{
					File:        "wezterm.lua",
					Destination: config.PlatformMap{MacOS: "~/.wezterm.lua"},
				},
			},
		},
		{
			Name: "empty",
			Items: []config.Item{
				{Package: "nonexistent", Via: "brew"},
			},
		},
	}

	home := "/Users/testuser"
	expand := func(p string) string {
		if len(p) > 1 && p[:2] == "~/" {
			return home + p[1:]
		}
		return p
	}

	fileExists := func(path string) bool {
		return path == home+"/.wezterm.lua"
	}
	pkgInstalled := func(manager, pkg string) bool {
		return manager == "brew-cask" && pkg == "wezterm"
	}

	results := ScanInstalled(modules, "darwin", expand, fileExists, noDir, pkgInstalled)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	var wez, empty ScanResult
	for _, r := range results {
		switch r.Module.Name {
		case "wezterm":
			wez = r
		case "empty":
			empty = r
		}
	}

	if wez.TotalItems != 2 {
		t.Errorf("wezterm TotalItems = %d, want 2", wez.TotalItems)
	}
	if len(wez.MatchedItems) != 2 {
		t.Errorf("wezterm matched = %d, want 2", len(wez.MatchedItems))
	}
	if wez.Score != 1.0 {
		t.Errorf("wezterm score = %f, want 1.0", wez.Score)
	}

	if empty.TotalItems != 1 {
		t.Errorf("empty TotalItems = %d, want 1", empty.TotalItems)
	}
	if len(empty.MatchedItems) != 0 {
		t.Errorf("empty matched = %d, want 0", len(empty.MatchedItems))
	}
}

func TestScanInstalledSkipsWrongOS(t *testing.T) {
	modules := []registry.RemoteModule{
		{
			Name: "macos-only",
			Items: []config.Item{
				{Package: "mas-app", Via: "mas"},
				{
					File:        ".rc",
					Destination: config.PlatformMap{MacOS: "~/"},
				},
			},
		},
	}

	expand := func(p string) string { return p }
	fileExists := func(string) bool { return false }
	pkgInstalled := func(string, string) bool { return false }

	results := ScanInstalled(modules, "linux", expand, fileExists, noDir, pkgInstalled)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].TotalItems != 0 {
		t.Errorf("TotalItems = %d, want 0 (all items excluded for wrong OS)", results[0].TotalItems)
	}
}
