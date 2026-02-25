package config

import (
	"os"
	"path/filepath"
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
	m := Module{From: "dotular.dev/modules/foo"}
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
