package template

import (
	"strings"
	"testing"

	"github.com/atomikpanda/dotular/internal/config"
)

func TestRender(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		params map[string]any
		want   string
	}{
		{"simple", "hello {{ .name }}", map[string]any{"name": "world"}, "hello world"},
		{"multiple", "{{ .a }} and {{ .b }}", map[string]any{"a": "x", "b": "y"}, "x and y"},
		{"no template", "plain text", map[string]any{"x": "y"}, "plain text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Render(tt.input, tt.params)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRenderMissingKey(t *testing.T) {
	_, err := Render("val={{ .missing }}", nil)
	if err == nil {
		t.Fatal("expected error for missing template key")
	}
}

func TestRenderInvalidTemplate(t *testing.T) {
	_, err := Render("{{ .bad", nil)
	if err == nil {
		t.Error("expected error for invalid template")
	}
}

func TestRenderItem(t *testing.T) {
	item := config.Item{
		Package: "{{ .pkg }}",
		Via:     "brew",
	}
	params := map[string]any{"pkg": "neovim"}
	result, err := RenderItem(item, params)
	if err != nil {
		t.Fatal(err)
	}
	if result.Package != "neovim" {
		t.Errorf("Package = %q, want %q", result.Package, "neovim")
	}
	if result.Via != "brew" {
		t.Errorf("Via = %q, want %q", result.Via, "brew")
	}
}

func TestRenderItemMultipleFields(t *testing.T) {
	item := config.Item{
		Binary:    "{{ .name }}",
		Version:   "{{ .version }}",
		InstallTo: "{{ .dir }}",
	}
	params := map[string]any{
		"name":    "nvim",
		"version": "0.10",
		"dir":     "/usr/local/bin",
	}
	result, err := RenderItem(item, params)
	if err != nil {
		t.Fatal(err)
	}
	if result.Binary != "nvim" {
		t.Errorf("Binary = %q", result.Binary)
	}
	if result.Version != "0.10" {
		t.Errorf("Version = %q", result.Version)
	}
	if result.InstallTo != "/usr/local/bin" {
		t.Errorf("InstallTo = %q", result.InstallTo)
	}
}

func TestRenderItemNoParams(t *testing.T) {
	item := config.Item{Package: "git", Via: "brew"}
	result, err := RenderItem(item, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Package != "git" {
		t.Errorf("Package = %q", result.Package)
	}
}

func TestRenderItemEmptyParams(t *testing.T) {
	item := config.Item{Package: "git", Via: "brew"}
	result, err := RenderItem(item, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Package != "git" {
		t.Errorf("Package = %q", result.Package)
	}
}

func TestRenderEmpty(t *testing.T) {
	got, err := Render("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("Render('') = %q", got)
	}
}

func TestRenderItemInvalidTemplate(t *testing.T) {
	item := config.Item{
		Package: "{{ .bad",
	}
	_, err := RenderItem(item, map[string]any{"x": "y"})
	if err == nil {
		t.Error("expected error for invalid template in item")
	}
}

func TestRenderItemFileFields(t *testing.T) {
	item := config.Item{
		File:      "{{ .filename }}",
		Direction: "{{ .dir }}",
	}
	params := map[string]any{"filename": "config.json", "dir": "push"}
	result, err := RenderItem(item, params)
	if err != nil {
		t.Fatal(err)
	}
	if result.File != "config.json" {
		t.Errorf("File = %q", result.File)
	}
	if result.Direction != "push" {
		t.Errorf("Direction = %q", result.Direction)
	}
}

func TestRenderItemScriptFields(t *testing.T) {
	item := config.Item{
		Script: "https://example.com/{{ .version }}/install.sh",
		Via:    "remote",
	}
	params := map[string]any{"version": "1.2.3"}
	result, err := RenderItem(item, params)
	if err != nil {
		t.Fatal(err)
	}
	if result.Script != "https://example.com/1.2.3/install.sh" {
		t.Errorf("Script = %q", result.Script)
	}
}

func TestRenderItemRollbackFields(t *testing.T) {
	item := config.Item{
		Run:      "install-{{ .service }}",
		Rollback: "uninstall-{{ .service }}",
		Hooks: config.ItemHooks{
			BeforeApply: "prepare-{{ .service }}",
			Rollback: config.RollbackHooks{
				BeforeApply: "undo-prepare-{{ .service }}",
			},
		},
	}

	result, err := RenderItem(item, map[string]any{"service": "custom-service"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Rollback != "uninstall-custom-service" {
		t.Fatalf("Rollback = %q", result.Rollback)
	}
	if result.Hooks.Rollback.BeforeApply != "undo-prepare-custom-service" {
		t.Fatalf("Hooks.Rollback.BeforeApply = %q", result.Hooks.Rollback.BeforeApply)
	}
}

func TestRenderItemRollbackFieldsRejectMissingParameters(t *testing.T) {
	tests := []struct {
		name string
		item config.Item
		key  string
	}{
		{
			name: "item action",
			item: config.Item{Run: "install", Rollback: "{{ .undo_action }}"},
			key:  "undo_action",
		},
		{
			name: "item hook",
			item: config.Item{
				Run: "install",
				Hooks: config.ItemHooks{
					BeforeApply: "prepare",
					Rollback: config.RollbackHooks{
						BeforeApply: "{{ .undo_hook }}",
					},
				},
			},
			key: "undo_hook",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := RenderItem(tt.item, map[string]any{"other": "value"})
			want := `map has no entry for key "` + tt.key + `"`
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("RenderItem() error = %v, want %q", err, want)
			}
		})
	}
}
