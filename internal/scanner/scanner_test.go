package scanner

import (
	"testing"

	"github.com/atomikpanda/dotular/internal/config"
	"github.com/atomikpanda/dotular/internal/registry"
)

func TestResolveFileTarget(t *testing.T) {
	expand := func(p string) string {
		if len(p) > 1 && p[:2] == "~/" {
			return "/home/user" + p[1:]
		}
		return p
	}

	tests := []struct {
		name     string
		dest     string
		fileName string
		want     string
	}{
		{
			name:     "dest with extension is complete path",
			dest:     "~/.wezterm.lua",
			fileName: "wezterm.lua",
			want:     "/home/user/.wezterm.lua",
		},
		{
			name:     "dest without extension is directory",
			dest:     "~/",
			fileName: ".zshrc",
			want:     "/home/user/.zshrc",
		},
		{
			name:     "dest directory no trailing slash",
			dest:     "~/.config/nvim",
			fileName: "init.lua",
			want:     "/home/user/.config/nvim/init.lua",
		},
		{
			name:     "dest with trailing slash forces directory",
			dest:     "~/.wezterm.lua/",
			fileName: "wezterm.lua",
			want:     "/home/user/.wezterm.lua/wezterm.lua",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveFileTarget(tt.dest, tt.fileName, expand)
			if got != tt.want {
				t.Errorf("resolveFileTarget(%q, %q) = %q, want %q", tt.dest, tt.fileName, got, tt.want)
			}
		})
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
	results := MatchPath(home+"/.wezterm.lua", modules, "darwin", expand)
	if len(results) != 1 {
		t.Fatalf("expected 1 match, got %d", len(results))
	}
	if results[0].ModuleName != "wezterm" {
		t.Errorf("ModuleName = %q", results[0].ModuleName)
	}

	// Prefix match (file under directory destination)
	results = MatchPath(home+"/.config/nvim/init.lua", modules, "darwin", expand)
	if len(results) != 1 {
		t.Fatalf("expected 1 match for prefix, got %d", len(results))
	}
	if results[0].ModuleName != "nvim" {
		t.Errorf("ModuleName = %q", results[0].ModuleName)
	}

	// No match
	results = MatchPath("/some/other/path", modules, "darwin", expand)
	if len(results) != 0 {
		t.Errorf("expected 0 matches, got %d", len(results))
	}

	// Wrong OS — wezterm has no windows destination
	results = MatchPath(home+"/.wezterm.lua", modules, "windows", expand)
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

	results := ScanInstalled(modules, "darwin", expand, fileExists, pkgInstalled)

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

	results := ScanInstalled(modules, "linux", expand, fileExists, pkgInstalled)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].TotalItems != 0 {
		t.Errorf("TotalItems = %d, want 0 (all items excluded for wrong OS)", results[0].TotalItems)
	}
}
