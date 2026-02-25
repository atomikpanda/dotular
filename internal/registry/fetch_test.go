package registry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/atomikpanda/dotular/internal/config"
)

func TestModuleCachePath(t *testing.T) {
	got := moduleCachePath("dotular.dev/modules/neovim@1.0.0")
	if got == "" {
		t.Error("expected non-empty cache path")
	}
	// Should not contain slashes or @ in the filename part.
	// The path replacer should have sanitized them.
}

func TestCachedRefs(t *testing.T) {
	lock := &LockFile{
		Registry: map[string]LockEntry{
			"ref1": {},
			"ref2": {},
		},
	}
	refs := CachedRefs(lock)
	if len(refs) != 2 {
		t.Errorf("expected 2 refs, got %d", len(refs))
	}
}

func TestCachedRefsEmpty(t *testing.T) {
	lock := &LockFile{Registry: map[string]LockEntry{}}
	refs := CachedRefs(lock)
	if len(refs) != 0 {
		t.Errorf("expected 0 refs, got %d", len(refs))
	}
}

func TestCollectActiveRefs(t *testing.T) {
	cfg := config.Config{
		Modules: []config.Module{
			{Name: "local", Items: []config.Item{{Package: "git"}}},
			{Name: "remote", From: "dotular.dev/modules/neovim@1.0.0"},
			{Name: "remote2", From: "github.com/user/repo"},
		},
	}
	refs := CollectActiveRefs(cfg)
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
	if !refs["dotular.dev/modules/neovim@1.0.0"] {
		t.Error("missing neovim ref")
	}
	if !refs["github.com/user/repo"] {
		t.Error("missing user/repo ref")
	}
}

func TestParseModule(t *testing.T) {
	data := []byte(`
name: test-module
version: "1.0"
params:
  editor:
    default: vim
items:
  - package: neovim
    via: brew
`)
	mod, _, err := parseModule(data)
	if err != nil {
		t.Fatal(err)
	}
	if mod.Name != "test-module" {
		t.Errorf("Name = %q", mod.Name)
	}
	if mod.Version != "1.0" {
		t.Errorf("Version = %q", mod.Version)
	}
	if len(mod.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(mod.Items))
	}
	if mod.Items[0].Package != "neovim" {
		t.Errorf("Package = %q", mod.Items[0].Package)
	}
}

func TestParseModuleInvalid(t *testing.T) {
	_, _, err := parseModule([]byte("{{invalid"))
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestWriteCacheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "cache.yaml")
	data := []byte("cached data")
	if err := writeCacheFile(path, data); err != nil {
		t.Fatal(err)
	}
	read, _ := os.ReadFile(path)
	if string(read) != "cached data" {
		t.Errorf("read = %q", string(read))
	}
}

func TestClearCache(t *testing.T) {
	// ClearCache removes ~/.cache/dotular/registry.
	// Just verify it doesn't panic.
	err := ClearCache()
	if err != nil {
		t.Fatal(err)
	}
}

func TestUnusedCacheEntries(t *testing.T) {
	lock := &LockFile{
		Registry: map[string]LockEntry{
			"ref1": {},
			"ref2": {},
			"ref3": {},
		},
	}
	active := map[string]bool{"ref1": true, "ref3": true}
	unused := UnusedCacheEntries(lock, active)
	if len(unused) != 1 {
		t.Fatalf("expected 1 unused, got %d", len(unused))
	}
	if unused[0] != "ref2" {
		t.Errorf("unused = %q", unused[0])
	}
}
