package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestItemType(t *testing.T) {
	tests := []struct {
		name string
		item Item
		want string
	}{
		{"package", Item{Package: "git"}, "package"},
		{"script", Item{Script: "setup.sh"}, "script"},
		{"setting", Item{Setting: "com.apple.dock"}, "setting"},
		{"file", Item{File: ".vimrc"}, "file"},
		{"directory", Item{Directory: "nvim"}, "directory"},
		{"binary", Item{Binary: "nvim"}, "binary"},
		{"run", Item{Run: "echo hello"}, "run"},
		{"unknown", Item{}, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.item.Type(); got != tt.want {
				t.Errorf("Type() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestItemPrimaryValue(t *testing.T) {
	tests := []struct {
		name string
		item Item
		want string
	}{
		{"package", Item{Package: "git"}, "git"},
		{"script", Item{Script: "setup.sh"}, "setup.sh"},
		{"setting", Item{Setting: "com.apple.dock"}, "com.apple.dock"},
		{"file", Item{File: ".vimrc"}, ".vimrc"},
		{"directory", Item{Directory: "nvim"}, "nvim"},
		{"binary", Item{Binary: "nvim"}, "nvim"},
		{"run", Item{Run: "echo hello"}, "echo hello"},
		{"unknown", Item{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.item.PrimaryValue(); got != tt.want {
				t.Errorf("PrimaryValue() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEffectiveDirection(t *testing.T) {
	tests := []struct {
		direction string
		want      string
	}{
		{"", "push"},
		{"push", "push"},
		{"pull", "pull"},
		{"sync", "sync"},
		{"invalid", "push"},
	}
	for _, tt := range tests {
		t.Run(tt.direction, func(t *testing.T) {
			item := Item{Direction: tt.direction}
			if got := item.EffectiveDirection(); got != tt.want {
				t.Errorf("EffectiveDirection() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPlatformMapForOS(t *testing.T) {
	pm := PlatformMap{MacOS: "/mac", Windows: `C:\win`, Linux: "/linux"}
	tests := []struct {
		goos string
		want string
	}{
		{"darwin", "/mac"},
		{"windows", `C:\win`},
		{"linux", "/linux"},
		{"freebsd", ""},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			if got := pm.ForOS(tt.goos); got != tt.want {
				t.Errorf("ForOS(%q) = %q, want %q", tt.goos, got, tt.want)
			}
		})
	}
}

func TestPlatformMapIsZero(t *testing.T) {
	if !((PlatformMap{}).IsZero()) {
		t.Error("empty PlatformMap should be zero")
	}
	if (PlatformMap{MacOS: "x"}).IsZero() {
		t.Error("non-empty PlatformMap should not be zero")
	}
}

func TestPlatformMapUnmarshalScalar(t *testing.T) {
	var pm PlatformMap
	err := yaml.Unmarshal([]byte(`~/path`), &pm)
	if err != nil {
		t.Fatal(err)
	}
	if pm.MacOS != "~/path" || pm.Windows != "~/path" || pm.Linux != "~/path" {
		t.Errorf("scalar unmarshal: got %+v", pm)
	}
}

func TestPlatformMapUnmarshalMapping(t *testing.T) {
	data := `
macos: ~/Library
windows: '%APPDATA%'
linux: ~/.config
`
	var pm PlatformMap
	if err := yaml.Unmarshal([]byte(data), &pm); err != nil {
		t.Fatal(err)
	}
	if pm.MacOS != "~/Library" {
		t.Errorf("MacOS = %q", pm.MacOS)
	}
	if pm.Windows != "%APPDATA%" {
		t.Errorf("Windows = %q", pm.Windows)
	}
	if pm.Linux != "~/.config" {
		t.Errorf("Linux = %q", pm.Linux)
	}
}

func TestPlatformMapMarshalScalar(t *testing.T) {
	pm := PlatformMap{MacOS: "same", Windows: "same", Linux: "same"}
	data, err := yaml.Marshal(pm)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "same\n" {
		t.Errorf("expected scalar marshal, got %q", string(data))
	}
}

func TestPlatformMapMarshalMapping(t *testing.T) {
	pm := PlatformMap{MacOS: "/mac", Windows: "/win", Linux: "/linux"}
	data, err := yaml.Marshal(pm)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]string
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["macos"] != "/mac" || decoded["windows"] != "/win" || decoded["linux"] != "/linux" {
		t.Errorf("mapping marshal round-trip failed: %v", decoded)
	}
}

func TestLoadNewFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dotular.yaml")
	data := `
modules:
  - name: test-mod
    items:
      - package: git
        via: brew
      - file: .vimrc
        destination: ~/
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Modules) != 1 {
		t.Fatalf("expected 1 module, got %d", len(cfg.Modules))
	}
	if cfg.Modules[0].Name != "test-mod" {
		t.Errorf("module name = %q", cfg.Modules[0].Name)
	}
	if len(cfg.Modules[0].Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(cfg.Modules[0].Items))
	}
}

func TestLoadLegacyFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dotular.yaml")
	data := `
- name: legacy-mod
  items:
    - package: curl
      via: brew
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Modules) != 1 {
		t.Fatalf("expected 1 module, got %d", len(cfg.Modules))
	}
	if cfg.Modules[0].Name != "legacy-mod" {
		t.Errorf("module name = %q", cfg.Modules[0].Name)
	}
}

func TestLoadEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dotular.yaml")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Modules) != 0 {
		t.Errorf("expected 0 modules, got %d", len(cfg.Modules))
	}
}

func TestLoadMissing(t *testing.T) {
	_, err := Load("/nonexistent/dotular.yaml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dotular.yaml")
	if err := os.WriteFile(path, []byte("{{invalid"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoadRejectsTrailingYAMLDocuments(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "mapping root",
			yaml: "modules: []\n---\nmodules: []\n",
			want: "multiple YAML documents",
		},
		{
			name: "legacy sequence root",
			yaml: "[]\n---\n[]\n",
			want: "multiple YAML documents",
		},
		{
			name: "malformed trailing document",
			yaml: "modules: []\n---\n[unterminated\n",
			want: "parse config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "dotular.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o644); err != nil {
				t.Fatal(err)
			}

			_, err := Load(path)
			if err == nil {
				t.Fatal("Load() succeeded, want trailing-document error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load() error = %q, want %q", err, tt.want)
			}
		})
	}
}

func TestConfigModule(t *testing.T) {
	cfg := Config{
		Modules: []Module{
			{Name: "alpha"},
			{Name: "beta"},
		},
	}
	if m := cfg.Module("alpha"); m == nil || m.Name != "alpha" {
		t.Error("expected to find module alpha")
	}
	if m := cfg.Module("gamma"); m != nil {
		t.Error("expected nil for nonexistent module")
	}
}

func TestModuleIsRegistry(t *testing.T) {
	m := Module{From: "github.com/user/repo"}
	if !m.IsRegistry() {
		t.Error("expected IsRegistry() true")
	}
	m2 := Module{Name: "local"}
	if m2.IsRegistry() {
		t.Error("expected IsRegistry() false")
	}
}

func TestSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dotular.yaml")
	cfg := Config{
		Modules: []Module{
			{
				Name: "testmod",
				Items: []Item{
					{Package: "git", Via: "brew"},
					{File: ".vimrc", Destination: PlatformMap{MacOS: "~/"}},
				},
			},
		},
	}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}

	// Reload and verify round-trip.
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Modules) != 1 {
		t.Fatalf("expected 1 module, got %d", len(loaded.Modules))
	}
	if loaded.Modules[0].Name != "testmod" {
		t.Errorf("module name = %q", loaded.Modules[0].Name)
	}
	if len(loaded.Modules[0].Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(loaded.Modules[0].Items))
	}
}

func TestSaveWithAge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dotular.yaml")
	cfg := Config{
		Age:     &AgeConfig{Passphrase: "secret"},
		Modules: []Module{{Name: "test"}},
	}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Age == nil {
		t.Fatal("expected age config")
	}
	if loaded.Age.Passphrase != "secret" {
		t.Errorf("passphrase = %q", loaded.Age.Passphrase)
	}
}

func TestSaveEmptyConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dotular.yaml")
	if err := Save(path, Config{}); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Modules) != 0 {
		t.Errorf("expected 0 modules, got %d", len(loaded.Modules))
	}
}

func TestLoadInvalidRoot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dotular.yaml")
	// A bare scalar is neither a mapping nor a sequence.
	if err := os.WriteFile(path, []byte("42"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for scalar root")
	}
}

func TestPlatformMapUnmarshalTildeNull(t *testing.T) {
	// YAML ~ is interpreted as null — PlatformMap should preserve it.
	data := `
macos: ~
windows: /win
linux: /linux
`
	var pm PlatformMap
	if err := yaml.Unmarshal([]byte(data), &pm); err != nil {
		t.Fatal(err)
	}
	if pm.MacOS != "~" {
		t.Errorf("MacOS = %q, want ~", pm.MacOS)
	}
}

// "~" is the one null-tagged scalar that means something as a path. Every other
// spelling means "no value for this platform" and must not leak its source text
// through as a literal path.
func TestPlatformMapUnmarshalNullSpellingsAreEmpty(t *testing.T) {
	for _, src := range []string{"macos: null", "macos: Null", "macos: NULL", "macos:"} {
		var pm PlatformMap
		if err := yaml.Unmarshal([]byte(src), &pm); err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if pm.MacOS != "" {
			t.Errorf("%s: MacOS = %q, want empty", src, pm.MacOS)
		}
	}
}

func TestPlatformMapUnmarshalRejectsNonScalarMappingValues(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{"sequence", "linux: [~]\n"},
		{"mapping", "linux: {path: ~/.config}\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pm PlatformMap
			err := yaml.Unmarshal([]byte(tt.yaml), &pm)
			if err == nil {
				t.Fatal("yaml.Unmarshal() succeeded, want non-scalar platform value error")
			}
			for _, want := range []string{"line 1", `"linux"`, "scalar"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("yaml.Unmarshal() error = %q, want %q", err, want)
				}
			}
		})
	}
}

func TestLoadWithAge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dotular.yaml")
	data := `
age:
  passphrase: "env:MY_SECRET"
modules:
  - name: test
    items:
      - file: secrets.env
        encrypted: true
        destination: ~/
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Age == nil {
		t.Fatal("expected age config")
	}
	if cfg.Age.Passphrase != "env:MY_SECRET" {
		t.Errorf("passphrase = %q", cfg.Age.Passphrase)
	}
}

func TestPlatformMapUnmarshalInvalid(t *testing.T) {
	// Test with a YAML sequence node (not valid for PlatformMap)
	data := `
destination:
  - one
  - two
`
	var item struct {
		Destination PlatformMap `yaml:"destination"`
	}
	err := yaml.Unmarshal([]byte(data), &item)
	if err == nil {
		t.Error("expected error for sequence node")
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		key  string
	}{
		{"root", "moduels: []\n", "moduels"},
		{"module", "modules:\n  - name: test\n    itmes: []\n", "itmes"},
		{"module hooks", "modules:\n  - name: test\n    hooks:\n      before_aply: echo bad\n    items: []\n", "before_aply"},
		{"item", "modules:\n  - name: test\n    items:\n      - packge: git\n", "packge"},
		{"item hooks", "modules:\n  - name: test\n    items:\n      - package: git\n        hooks:\n          after_aply: echo bad\n", "after_aply"},
		{"legacy module", "- name: test\n  itmes: []\n", "itmes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "dotular.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o644); err != nil {
				t.Fatal(err)
			}

			_, err := Load(path)
			if err == nil {
				t.Fatal("Load() succeeded, want unknown-field error")
			}
			if !strings.Contains(err.Error(), tt.key) || !strings.Contains(err.Error(), "line ") {
				t.Fatalf("Load() error = %q, want key %q and line information", err, tt.key)
			}
		})
	}
}

func TestPlatformMapUnmarshalRejectsUnknownAndDuplicateKeys(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"runtime darwin spelling", "darwin: ~/.config\n", `unknown platform key "darwin"`},
		{"arbitrary key", "freebsd: ~/.config\n", `unknown platform key "freebsd"`},
		{"duplicate canonical key", "macos: one\nmacos: two\n", `duplicate platform key "macos"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pm PlatformMap
			err := yaml.Unmarshal([]byte(tt.yaml), &pm)
			if err == nil {
				t.Fatal("yaml.Unmarshal() succeeded, want error")
			}
			if !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), "line ") {
				t.Fatalf("yaml.Unmarshal() error = %q, want %q and line information", err, tt.want)
			}
		})
	}
}

func TestLoadSupportedRootShapes(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		modules int
	}{
		{"empty file", "", 0},
		{"empty mapping", "{}\n", 0},
		{"empty sequence", "[]\n", 0},
		{"mapping", "modules:\n  - name: local\n    items:\n      - package: git\n", 1},
		{"legacy sequence", "- name: legacy\n  items:\n    - package: curl\n", 1},
		{"mapping with trailing comment and whitespace", "modules: []\n# trailing comment\n\n", 0},
		{"legacy sequence with trailing comment and whitespace", "[]\n# trailing comment\n\n", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "dotular.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if len(cfg.Modules) != tt.modules {
				t.Fatalf("Load() modules = %d, want %d", len(cfg.Modules), tt.modules)
			}
		})
	}
}

func TestValidateDirection(t *testing.T) {
	for _, direction := range []string{"push", "pull", "sync"} {
		t.Run(direction, func(t *testing.T) {
			if err := ValidateDirection(direction); err != nil {
				t.Fatalf("ValidateDirection(%q) error = %v", direction, err)
			}
		})
	}

	err := ValidateDirection("pul")
	if err == nil || !strings.Contains(err.Error(), `direction "pul" must be push, pull, or sync`) {
		t.Fatalf("ValidateDirection(%q) error = %v", "pul", err)
	}
}

func TestValidateItems(t *testing.T) {
	primaryItems := []struct {
		name string
		item Item
	}{
		{"package", Item{Package: "git"}},
		{"script", Item{Script: "setup.sh"}},
		{"setting", Item{Setting: "domain"}},
		{"file", Item{File: ".vimrc"}},
		{"directory", Item{Directory: "nvim"}},
		{"binary", Item{Binary: "tool"}},
		{"run", Item{Run: "echo ok"}},
	}
	for _, tt := range primaryItems {
		t.Run("single primary "+tt.name, func(t *testing.T) {
			if err := ValidateItems([]Item{tt.item}, ItemValidationOptions{}); err != nil {
				t.Fatalf("ValidateItems() error = %v", err)
			}
		})
	}

	directionTests := []struct {
		name      string
		direction string
	}{
		{"empty", ""},
		{"default", DefaultDirection},
		{"pull", "pull"},
		{"sync", "sync"},
	}
	for _, tt := range directionTests {
		t.Run("direction "+tt.name, func(t *testing.T) {
			item := Item{File: ".vimrc", Direction: tt.direction}
			if err := ValidateItems([]Item{item}, ItemValidationOptions{}); err != nil {
				t.Fatalf("ValidateItems() error = %v", err)
			}
		})
	}

	failures := []struct {
		name  string
		items []Item
		want  string
	}{
		{"zero primaries", []Item{{Verify: "true"}}, "item 1: expected exactly one primary field; found none"},
		{"package and file", []Item{{Package: "git", File: ".gitconfig"}}, "item 1: expected exactly one primary field; found package, file"},
		{"script and run", []Item{{Script: "setup.sh", Run: "echo bad"}}, "item 1: expected exactly one primary field; found script, run"},
		{"invalid direction", []Item{{File: ".vimrc", Direction: "pul"}}, `item 1: direction "pul" must be push, pull, or sync`},
	}
	for _, tt := range failures {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateItems(tt.items, ItemValidationOptions{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateItems() error = %v, want %q", err, tt.want)
			}
		})
	}

	templateItem := Item{File: ".vimrc", Direction: "{{ .direction }}"}
	if err := ValidateItems([]Item{templateItem}, ItemValidationOptions{AllowDirectionTemplates: true}); err != nil {
		t.Fatalf("ValidateItems() with allowed direction template error = %v", err)
	}
	if err := ValidateItems([]Item{templateItem}, ItemValidationOptions{}); err == nil {
		t.Fatal("ValidateItems() accepted a direction template without permission")
	}
}

func TestConfigValidate(t *testing.T) {
	valid := Config{Modules: []Module{
		{Name: "local", Items: []Item{{Package: "git"}}},
		{Name: "remote", From: "github.com/example/modules/shell", Override: []Item{{File: ".vimrc", Direction: "pull"}}},
	}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Config.Validate() valid config error = %v", err)
	}

	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			"from and items",
			Config{Modules: []Module{{Name: "remote", From: "github.com/example/modules/shell", Items: []Item{{Package: "git"}}}}},
			`module 1 ("remote"): from and items are mutually exclusive`,
		},
		{
			"invalid override item",
			Config{Modules: []Module{{Name: "remote", From: "github.com/example/modules/shell", Override: []Item{{Package: "git"}, {File: ".vimrc", Direction: "pul"}}}}},
			`module 1 ("remote"): override: item 2: direction "pul" must be push, pull, or sync`,
		},
		{
			"module identity and collection",
			Config{Modules: []Module{{Name: "ok", Items: []Item{{Package: "git"}}}, {Name: "shell", Items: []Item{{Package: "git", File: ".gitconfig"}}}}},
			`module 2 ("shell"): items: item 1: expected exactly one primary field; found package, file`,
		},
		{
			"unnamed module",
			Config{Modules: []Module{{Items: []Item{{Verify: "true"}}}}},
			`module 1: items: item 1: expected exactly one primary field; found none`,
		},
		{
			"stable first failure",
			Config{Modules: []Module{
				{Name: "first", Items: []Item{{Package: "git"}, {Verify: "true"}}},
				{Name: "second", Items: []Item{{File: ".vimrc", Direction: "pul"}}},
			}},
			`module 1 ("first"): items: item 2: expected exactly one primary field; found none`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Config.Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestLoadRejectsIssueExamples(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"misspelled item field", "modules:\n  - name: bad\n    items:\n      - packge: git\n", "packge"},
		{"darwin destination", "modules:\n  - name: bad\n    items:\n      - file: .vimrc\n        destination: {darwin: ~/.config}\n", `unknown platform key "darwin"`},
		{"multiple primaries", "modules:\n  - name: bad\n    items:\n      - package: git\n        file: .gitconfig\n", "expected exactly one primary field; found package, file"},
		{"no primary", "modules:\n  - name: bad\n    items:\n      - verify: 'true'\n", "expected exactly one primary field; found none"},
		{"from and items", "modules:\n  - name: bad\n    from: github.com/example/modules/bad\n    items:\n      - package: git\n", "from and items are mutually exclusive"},
		{"invalid direction", "modules:\n  - name: bad\n    items:\n      - file: .vimrc\n        direction: pul\n", `direction "pul" must be push, pull, or sync`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "dotular.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestSaveRejectsInvalidConfigBeforeWrite(t *testing.T) {
	invalid := Config{Modules: []Module{{Name: "bad", Items: []Item{{Verify: "true"}}}}}

	t.Run("existing destination", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "dotular.yaml")
		existing := []byte("sentinel: unchanged\n")
		if err := os.WriteFile(path, existing, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := Save(path, invalid); err == nil {
			t.Fatal("Save() succeeded, want validation error")
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(existing) {
			t.Fatalf("Save() changed existing destination: got %q, want %q", got, existing)
		}
	})

	t.Run("missing destination", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "dotular.yaml")
		if err := Save(path, invalid); err == nil {
			t.Fatal("Save() succeeded, want validation error")
		}
		_, err := os.Stat(path)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("os.Stat() error = %v, want fs.ErrNotExist", err)
		}
	})
}
