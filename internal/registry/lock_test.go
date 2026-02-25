package registry

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLockPath(t *testing.T) {
	got := LockPath("/home/user/dotular.yaml")
	want := "/home/user/dotular.lock.yaml"
	if got != want {
		t.Errorf("LockPath() = %q, want %q", got, want)
	}
}

func TestLoadLockMissing(t *testing.T) {
	lf, err := LoadLock("/nonexistent/dotular.lock.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if lf.Registry == nil {
		t.Error("expected initialized Registry map")
	}
	if len(lf.Registry) != 0 {
		t.Errorf("expected empty Registry, got %d", len(lf.Registry))
	}
}

func TestSaveAndLoadLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dotular.lock.yaml")

	lf := &LockFile{
		Registry: map[string]LockEntry{
			"dotular.dev/modules/neovim@1.0.0": {
				SHA256:    "abc123",
				FetchedAt: time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC),
				URL:       "https://dotular.dev/modules/neovim/1.0.0.yaml",
			},
		},
	}

	if err := SaveLock(path, lf); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadLock(path)
	if err != nil {
		t.Fatal(err)
	}

	entry, ok := loaded.Registry["dotular.dev/modules/neovim@1.0.0"]
	if !ok {
		t.Fatal("expected entry in loaded lock")
	}
	if entry.SHA256 != "abc123" {
		t.Errorf("SHA256 = %q", entry.SHA256)
	}
	if entry.URL != "https://dotular.dev/modules/neovim/1.0.0.yaml" {
		t.Errorf("URL = %q", entry.URL)
	}
}

func TestLoadLockInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.lock.yaml")
	os.WriteFile(path, []byte("{{invalid yaml"), 0o644)

	_, err := LoadLock(path)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoadLockNilRegistry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.lock.yaml")
	os.WriteFile(path, []byte("{}"), 0o644)

	lf, err := LoadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if lf.Registry == nil {
		t.Error("expected initialized Registry map even from empty YAML")
	}
}
