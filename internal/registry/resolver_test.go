package registry

import (
	"strings"
	"testing"

	"github.com/atomikpanda/dotular/internal/config"
)

func TestResolveParams(t *testing.T) {
	defs := map[string]Param{
		"editor": {Default: "vim", Description: "editor"},
		"theme":  {Default: "dark", Description: "theme"},
	}
	with := map[string]any{
		"editor": "nvim",
		"extra":  "val",
	}

	params := resolveParams(defs, with)

	if params["editor"] != "nvim" {
		t.Errorf("editor = %v", params["editor"])
	}
	if params["theme"] != "dark" {
		t.Errorf("theme = %v", params["theme"])
	}
	if params["extra"] != "val" {
		t.Errorf("extra = %v", params["extra"])
	}
}

func TestResolveParamsEmpty(t *testing.T) {
	params := resolveParams(nil, nil)
	if len(params) != 0 {
		t.Errorf("expected empty params, got %d", len(params))
	}
}

func TestMergeOverrides(t *testing.T) {
	base := []config.Item{
		{Package: "git", Via: "brew"},
		{Package: "curl", Via: "brew"},
		{File: ".vimrc"},
	}
	overrides := []config.Item{
		{Package: "curl", Via: "apt"},    // replaces
		{Package: "neovim", Via: "brew"}, // appends (no match)
	}

	result := mergeOverrides(base, overrides)

	if len(result) != 4 {
		t.Fatalf("expected 4 items, got %d", len(result))
	}
	// git unchanged
	if result[0].Package != "git" || result[0].Via != "brew" {
		t.Errorf("item 0: %+v", result[0])
	}
	// curl replaced
	if result[1].Package != "curl" || result[1].Via != "apt" {
		t.Errorf("item 1: %+v", result[1])
	}
	// .vimrc unchanged
	if result[2].File != ".vimrc" {
		t.Errorf("item 2: %+v", result[2])
	}
	// neovim appended
	if result[3].Package != "neovim" {
		t.Errorf("item 3: %+v", result[3])
	}
}

func TestMergeOverridesEmpty(t *testing.T) {
	base := []config.Item{{Package: "git"}}
	result := mergeOverrides(base, nil)
	if len(result) != 1 || result[0].Package != "git" {
		t.Errorf("unexpected result: %+v", result)
	}
}

func TestRenderItems(t *testing.T) {
	items := []config.Item{
		{Package: "{{ .pkg }}", Via: "brew"},
	}
	params := map[string]any{"pkg": "neovim"}

	result, err := renderItems(items, params)
	if err != nil {
		t.Fatal(err)
	}
	if result[0].Package != "neovim" {
		t.Errorf("Package = %q", result[0].Package)
	}
}

func TestRenderItemsValidatesRenderedValues(t *testing.T) {
	tests := []struct {
		name   string
		items  []config.Item
		params map[string]any
		want   string
	}{
		{
			name:   "primary renders empty",
			items:  []config.Item{{Package: "{{ .package }}"}},
			params: map[string]any{"package": ""},
			want:   "expected exactly one primary field",
		},
		{
			name:   "direction renders invalid",
			items:  []config.Item{{File: "config", Direction: "{{ .direction }}"}},
			params: map[string]any{"direction": "pul"},
			want:   `direction "pul"`,
		},
		{
			name:  "multiple primaries rejected",
			items: []config.Item{{Package: "git", File: "config"}},
			want:  "package, file",
		},
		{
			name:  "malformed template without params",
			items: []config.Item{{Package: "{{ .bad"}},
			want:  "parse template",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := renderItems(tt.items, tt.params)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("renderItems() error = %v, want containing %q", err, tt.want)
			}
			if got != nil {
				t.Fatalf("renderItems() = %#v, want nil on validation error", got)
			}
		})
	}

	t.Run("direction renders valid", func(t *testing.T) {
		got, err := renderItems(
			[]config.Item{{File: "config", Direction: "{{ .direction }}"}},
			map[string]any{"direction": "sync"},
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Direction != "sync" {
			t.Fatalf("renderItems() = %#v, want one item with direction sync", got)
		}
	})
}
