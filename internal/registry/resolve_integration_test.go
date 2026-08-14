package registry

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/atomikpanda/dotular/internal/config"
	"github.com/atomikpanda/dotular/internal/ui"
)

func TestResolveOptionContract(t *testing.T) {
	t.Parallel()

	opts := ResolveOptions{NoCache: true}
	if !opts.NoCache {
		t.Fatal("ResolveOptions.NoCache = false; want true")
	}
}

func TestDownloadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := download(context.Background(), srv.URL+"/missing")
	if err == nil {
		t.Error("expected error for 404")
	}
}

func TestResolveLocalModules(t *testing.T) {
	cfg := config.Config{
		Modules: []config.Module{
			{Name: "local", Items: []config.Item{{Package: "git", Via: "brew"}}},
		},
	}

	dir := t.TempDir()
	configPath := filepath.Join(dir, "dotular.yaml")
	os.WriteFile(configPath, []byte("modules: []"), 0o644)

	result, err := Resolve(context.Background(), cfg, configPath, ResolveOptions{}, ui.New(&bytes.Buffer{}, &bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Modules) != 1 {
		t.Fatalf("expected 1 module, got %d", len(result.Modules))
	}
	if result.Modules[0].Name != "local" {
		t.Errorf("Name = %q", result.Modules[0].Name)
	}
}

// Fetch must reject a versioned external ref, and must do so before consulting
// the cache: a lockfile entry pins content but still cannot select the requested
// version, so a warm cache must not mask the bad reference. No server is needed
// because the guard returns before any I/O.
func TestFetchRejectsVersionedExternalRefBeforeCache(t *testing.T) {
	dir := t.TempDir()
	lock, err := LoadLock(filepath.Join(dir, "dotular.lock.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	rawRef := "custom.host/path/to/module@v2"
	lock.Registry[rawRef] = LockEntry{SHA256: "not-consulted"}

	u := ui.New(&bytes.Buffer{}, &bytes.Buffer{})
	_, _, err = Fetch(context.Background(), rawRef, lock, FetchOptions{}, u)
	if err == nil {
		t.Fatal("Fetch() = nil error, want a rejection for a versioned external ref")
	}
	if !strings.Contains(err.Error(), "version selection is not supported") {
		t.Errorf("error = %q, want it to explain that external version selection is unsupported", err)
	}
	// The checksum mismatch path must not be what fired.
	if strings.Contains(err.Error(), "checksum") {
		t.Errorf("error = %q, want the version guard to fire before the cache check", err)
	}
}

func TestResolvePreservesAge(t *testing.T) {
	ageCfg := &config.AgeConfig{Passphrase: "test"}
	cfg := config.Config{
		Age:     ageCfg,
		Modules: []config.Module{},
	}

	dir := t.TempDir()
	configPath := filepath.Join(dir, "dotular.yaml")
	os.WriteFile(configPath, []byte("modules: []"), 0o644)

	result, err := Resolve(context.Background(), cfg, configPath, ResolveOptions{}, ui.New(&bytes.Buffer{}, &bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Age == nil || result.Age.Passphrase != "test" {
		t.Error("expected age config to be preserved")
	}
}

func writeResolveRegistryConfig(t *testing.T, ref string, entry *LockEntry) (string, string) {
	t.Helper()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "dotular.yaml")
	content := []byte("modules:\n  - from: " + ref + "\n")
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	lockPath := LockPath(configPath)
	lock := &LockFile{Registry: make(map[string]LockEntry)}
	if entry != nil {
		lock.Registry[ref] = *entry
	}
	if err := SaveLock(lockPath, lock); err != nil {
		t.Fatal(err)
	}
	return configPath, lockPath
}

func TestResolvePersistsInitialPin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testModuleYAML))
	})
	configPath, lockPath := writeResolveRegistryConfig(t, ref, nil)

	result, err := Resolve(
		context.Background(),
		config.Config{Modules: []config.Module{{From: ref}}},
		configPath,
		ResolveOptions{},
		ui.New(&bytes.Buffer{}, &bytes.Buffer{}),
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(result.Modules) != 1 {
		t.Fatalf("resolved modules = %d, want 1", len(result.Modules))
	}

	entry, ok := loadTestLock(t, lockPath).Registry[ref]
	if !ok {
		t.Fatal("durable lock has no initial registry pin after Resolve() succeeded")
	}
	if got, want := entry.SHA256, testModuleChecksum(testModuleYAML); got != want {
		t.Fatalf("durable checksum = %q, want %q", got, want)
	}
}

func TestResolveSaveLockFailureIsFatal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testModuleYAML))
	})
	configPath, lockPath := writeResolveRegistryConfig(t, ref, nil)
	if err := os.Mkdir(lockPath+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	result, err := Resolve(
		context.Background(),
		config.Config{Modules: []config.Module{{From: ref}}},
		configPath,
		ResolveOptions{},
		ui.New(&stdout, &stderr),
	)
	if err == nil {
		t.Fatal("Resolve() = nil error, want the lock persistence failure")
	}
	if !strings.Contains(err.Error(), "save lockfile") || !strings.Contains(err.Error(), "write lockfile") {
		t.Fatalf("Resolve() error = %q, want the wrapped lock persistence failure", err)
	}
	if len(result.Modules) != 0 {
		t.Fatalf("Resolve() returned %d modules with a fatal save error, want no success result", len(result.Modules))
	}
	if strings.Contains(stderr.String(), "could not save lockfile") {
		t.Fatalf("Resolve() downgraded the save failure to a warning: %q", stderr.String())
	}
	if _, ok := loadTestLock(t, lockPath).Registry[ref]; ok {
		t.Fatal("durable lock gained an entry despite SaveLock failure")
	}
}

func TestResolveNoCacheRejectsDrift(t *testing.T) {
	tests := []struct {
		name        string
		networkYAML string
		cacheYAML   string
		wantErr     bool
	}{
		{
			name:        "matching",
			networkYAML: testModuleYAML,
			cacheYAML:   replacementModuleYAML,
		},
		{
			name:        "differing",
			networkYAML: replacementModuleYAML,
			cacheYAML:   testModuleYAML,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			var requests atomic.Int32
			ref := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Write([]byte(tt.networkYAML))
			})
			original := LockEntry{
				SHA256: testModuleChecksum(testModuleYAML),
				URL:    ParseRef(ref).FetchURL,
			}
			configPath, lockPath := writeResolveRegistryConfig(t, ref, &original)
			if err := writeCacheFile(moduleCachePath(ref), []byte(tt.cacheYAML)); err != nil {
				t.Fatal(err)
			}

			result, err := Resolve(
				context.Background(),
				config.Config{Modules: []config.Module{{From: ref}}},
				configPath,
				ResolveOptions{NoCache: true},
				ui.New(&bytes.Buffer{}, &bytes.Buffer{}),
			)
			if tt.wantErr {
				if err == nil {
					t.Fatal("Resolve() = nil error, want a checksum mismatch")
				}
				if !strings.Contains(err.Error(), "checksum mismatch") {
					t.Fatalf("Resolve() error = %q, want it to contain %q", err, "checksum mismatch")
				}
				if len(result.Modules) != 0 {
					t.Fatalf("Resolve() returned %d modules after checksum mismatch, want 0", len(result.Modules))
				}
			} else {
				if err != nil {
					t.Fatalf("Resolve() error = %v", err)
				}
				if len(result.Modules) != 1 {
					t.Fatalf("resolved modules = %d, want 1", len(result.Modules))
				}
			}

			requireLockEntryUnchanged(t, original, loadTestLock(t, lockPath).Registry[ref])
			if got := requests.Load(); got != 1 {
				t.Fatalf("network requests = %d, want 1", got)
			}
		})
	}
}
