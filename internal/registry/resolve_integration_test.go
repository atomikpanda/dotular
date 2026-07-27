package registry

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atomikpanda/dotular/internal/config"
	"github.com/atomikpanda/dotular/internal/ui"
)

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

	result, err := Resolve(context.Background(), cfg, configPath, false, ui.New(&bytes.Buffer{}, &bytes.Buffer{}))
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
	_, _, err = Fetch(context.Background(), rawRef, lock, false, u)
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

	result, err := Resolve(context.Background(), cfg, configPath, false, ui.New(&bytes.Buffer{}, &bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Age == nil || result.Age.Passphrase != "test" {
		t.Error("expected age config to be preserved")
	}
}
