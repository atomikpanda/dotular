package registry

import (
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
		{Package: "curl", Via: "apt"},      // replaces
		{Package: "neovim", Via: "brew"},    // appends (no match)
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
