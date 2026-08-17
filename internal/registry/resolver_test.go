package registry

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

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
		{Package: "curl", Via: "apt", Rollback: "remove-curl"}, // replaces
		{Package: "neovim", Via: "brew"},                       // appends (no match)
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
	if result[1].Rollback != "remove-curl" {
		t.Errorf("item 1 rollback = %q", result[1].Rollback)
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

func TestRenderItemsRejectsMissingParameter(t *testing.T) {
	got, err := renderItems(
		[]config.Item{{Package: "{{ .pkg }}"}},
		map[string]any{"other": "value"},
	)
	if err == nil {
		t.Fatalf("renderItems() = %#v, want missing key error", got)
	}
	if !strings.Contains(err.Error(), `map has no entry for key "pkg"`) {
		t.Fatalf("renderItems() error = %v, want missing key context", err)
	}
	if got != nil {
		t.Fatalf("renderItems() = %#v, want nil on render error", got)
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
			name:   "rollback renders empty",
			items:  []config.Item{{Run: "install", Rollback: "{{ .rollback }}"}},
			params: map[string]any{"rollback": ""},
			want:   "item 1: rollback must not be blank",
		},
		{
			name: "rollback hook renders empty",
			items: []config.Item{{
				Run: "install",
				Hooks: config.ItemHooks{
					BeforeApply: "prepare",
					Rollback: config.RollbackHooks{
						BeforeApply: "{{ .rollback }}",
					},
				},
			}},
			params: map[string]any{"rollback": ""},
			want:   "item 1: hooks.rollback.before_apply must not be blank",
		},
		{
			name:   "rollback has missing parameter",
			items:  []config.Item{{Run: "install", Rollback: "{{ .rollback }}"}},
			params: map[string]any{"other": "value"},
			want:   `map has no entry for key "rollback"`,
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

func TestRenderItemsRejectsExplicitBlankRollbackValues(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "empty action",
			yaml: "items:\n  - run: install\n    rollback: ''\n",
			want: "item 1: rollback must not be blank",
		},
		{
			name: "null action",
			yaml: "items:\n  - run: install\n    rollback: null\n",
			want: "item 1: rollback must not be blank",
		},
		{
			name: "empty hook",
			yaml: "items:\n  - run: install\n    hooks:\n      before_apply: prepare\n      rollback:\n        before_apply: ''\n",
			want: "item 1: hooks.rollback.before_apply must not be blank",
		},
		{
			name: "null hook",
			yaml: "items:\n  - run: install\n    hooks:\n      before_apply: prepare\n      rollback:\n        before_apply: null\n",
			want: "item 1: hooks.rollback.before_apply must not be blank",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var decoded struct {
				Items []config.Item `yaml:"items"`
			}
			if err := yaml.Unmarshal([]byte(tt.yaml), &decoded); err != nil {
				t.Fatalf("yaml.Unmarshal() items error = %v", err)
			}
			var document yaml.Node
			if err := yaml.Unmarshal([]byte(tt.yaml), &document); err != nil {
				t.Fatalf("yaml.Unmarshal() node error = %v", err)
			}
			config.MarkItemRollbackPresence(decoded.Items, &document)
			got, err := renderItems(decoded.Items, nil)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("renderItems() error = %v, want %q", err, tt.want)
			}
			if got != nil {
				t.Fatalf("renderItems() = %#v, want nil on validation error", got)
			}
		})
	}
}
