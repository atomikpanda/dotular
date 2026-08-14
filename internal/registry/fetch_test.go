package registry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atomikpanda/dotular/internal/config"
	"github.com/atomikpanda/dotular/internal/testutil"
	"github.com/atomikpanda/dotular/internal/ui"
)

// The registry cache lives under the home directory, so the whole suite needs a
// home of its own or it would read and overwrite the developer's real cache.
func TestMain(m *testing.M) {
	os.Exit(testutil.IsolateHome(m))
}

func TestModuleCachePath(t *testing.T) {
	got := moduleCachePath("github.com/atomikpanda/dotular/modules/neovim@main")
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
			{Name: "remote", From: "github.com/atomikpanda/dotular/modules/neovim@main"},
			{Name: "remote2", From: "github.com/user/repo"},
		},
	}
	refs := CollectActiveRefs(cfg)
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
	if !refs["github.com/atomikpanda/dotular/modules/neovim@main"] {
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
	mod, err := parseModule(data)
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
	_, err := parseModule([]byte("{{invalid"))
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

func TestResolveRejectsDriftWithoutMutation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref := serveTestModule(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, replacementModuleYAML)
	})
	const missingRef = "example.invalid/missing.yaml"
	configPath := filepath.Join(t.TempDir(), "dotular.yaml")
	original := LockEntry{
		SHA256: testModuleChecksum(testModuleYAML),
		URL:    ParseRef(ref).FetchURL,
	}
	lock := &LockFile{Registry: map[string]LockEntry{ref: original}}
	if err := SaveLock(LockPath(configPath), lock); err != nil {
		t.Fatal(err)
	}
	if err := writeCacheFile(moduleCachePath(ref), []byte(testModuleYAML)); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Modules: []config.Module{
		{From: ref},
		{From: missingRef},
	}}

	_, err := Resolve(
		context.Background(),
		cfg,
		configPath,
		ResolveOptions{NoCache: true},
		ui.New(io.Discard, io.Discard),
	)
	if err == nil {
		t.Fatal("Resolve() succeeded, want checksum drift rejection")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Resolve() error = %q, want checksum mismatch", err)
	}

	persisted := loadTestLock(t, LockPath(configPath))
	if len(persisted.Registry) != 1 {
		t.Fatalf("persisted registry entries = %d, want 1", len(persisted.Registry))
	}
	requireLockEntryUnchanged(t, original, persisted.Registry[ref])
	if _, ok := persisted.Registry[missingRef]; ok {
		t.Fatalf("missing ref %q was pinned after drift failure", missingRef)
	}
	cached, readErr := os.ReadFile(moduleCachePath(ref))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(cached) != testModuleYAML {
		t.Fatalf("cache changed after drift failure: %q", cached)
	}
	if _, statErr := os.Stat(moduleCachePath(missingRef)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing ref cache error = %v, want not exist", statErr)
	}
}

func TestResolveKeepsAllOrdinaryCallFormsImmutable(t *testing.T) {
	type caller func(
		context.Context,
		string,
		config.Config,
		string,
		*LockFile,
		*ui.UI,
	) error
	tests := []struct {
		name         string
		call         caller
		wantMismatch bool
	}{
		{
			name: "Fetch cached",
			call: func(ctx context.Context, ref string, _ config.Config, _ string, lock *LockFile, u *ui.UI) error {
				_, _, err := Fetch(ctx, ref, lock, FetchOptions{}, u)
				return err
			},
		},
		{
			name: "Fetch no-cache",
			call: func(ctx context.Context, ref string, _ config.Config, _ string, lock *LockFile, u *ui.UI) error {
				_, _, err := Fetch(ctx, ref, lock, FetchOptions{NoCache: true}, u)
				return err
			},
			wantMismatch: true,
		},
		{
			name: "Resolve cached",
			call: func(ctx context.Context, _ string, cfg config.Config, configPath string, _ *LockFile, u *ui.UI) error {
				_, err := Resolve(ctx, cfg, configPath, ResolveOptions{}, u)
				return err
			},
		},
		{
			name: "Resolve no-cache",
			call: func(ctx context.Context, _ string, cfg config.Config, configPath string, _ *LockFile, u *ui.UI) error {
				_, err := Resolve(ctx, cfg, configPath, ResolveOptions{NoCache: true}, u)
				return err
			},
			wantMismatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			ref := serveTestModule(t, func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, replacementModuleYAML)
			})
			configPath := filepath.Join(t.TempDir(), "dotular.yaml")
			original := LockEntry{
				SHA256: testModuleChecksum(testModuleYAML),
				URL:    ParseRef(ref).FetchURL,
			}
			lock := &LockFile{Registry: map[string]LockEntry{ref: original}}
			if err := SaveLock(LockPath(configPath), lock); err != nil {
				t.Fatal(err)
			}
			if err := writeCacheFile(moduleCachePath(ref), []byte(testModuleYAML)); err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{Modules: []config.Module{{From: ref}}}

			err := tt.call(
				context.Background(),
				ref,
				cfg,
				configPath,
				lock,
				ui.New(io.Discard, io.Discard),
			)
			if tt.wantMismatch {
				if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
					t.Fatalf("%s error = %v, want checksum mismatch", tt.name, err)
				}
			} else if err != nil {
				t.Fatalf("%s error = %v", tt.name, err)
			}

			requireLockEntryUnchanged(t, original, lock.Registry[ref])
			persisted := loadTestLock(t, LockPath(configPath))
			requireLockEntryUnchanged(t, original, persisted.Registry[ref])
			cached, readErr := os.ReadFile(moduleCachePath(ref))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(cached) != testModuleYAML {
				t.Fatalf("%s changed cache: %q", tt.name, cached)
			}
		})
	}
}
