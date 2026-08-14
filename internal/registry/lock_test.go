package registry

import (
	"errors"
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
			"github.com/atomikpanda/dotular/modules/neovim@main": {
				SHA256:    "abc123",
				FetchedAt: time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC),
				URL:       "https://raw.githubusercontent.com/atomikpanda/dotular/main/modules/neovim.yaml",
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

	entry, ok := loaded.Registry["github.com/atomikpanda/dotular/modules/neovim@main"]
	if !ok {
		t.Fatal("expected entry in loaded lock")
	}
	if entry.SHA256 != "abc123" {
		t.Errorf("SHA256 = %q", entry.SHA256)
	}
	if entry.URL != "https://raw.githubusercontent.com/atomikpanda/dotular/main/modules/neovim.yaml" {
		t.Errorf("URL = %q", entry.URL)
	}
}

func TestSaveLockReplacesExistingLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dotular.lock.yaml")
	if err := os.WriteFile(path, []byte("old lock bytes"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", path, err)
	}

	lf := &LockFile{
		Registry: map[string]LockEntry{
			"example.com/module@main": {
				SHA256: "new checksum",
				URL:    "https://example.com/module.yaml",
			},
		},
	}
	if err := SaveLock(path, lf); err != nil {
		t.Fatalf("SaveLock() error = %v", err)
	}

	loaded, err := LoadLock(path)
	if err != nil {
		t.Fatalf("LoadLock() error = %v", err)
	}
	if got := loaded.Registry["example.com/module@main"].SHA256; got != "new checksum" {
		t.Fatalf("saved checksum = %q, want %q", got, "new checksum")
	}
}

func TestSaveLockReplacementFailurePreservesDestinationAndCleansTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dotular.lock.yaml")
	tempPath := path + ".tmp"
	oldData := []byte("old lock bytes must survive")
	if err := os.WriteFile(path, oldData, 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", path, err)
	}

	replaceErr := errors.New("replace failed")
	err := saveLockWithReplace(path, &LockFile{}, func(gotTempPath string, gotPath string) error {
		if gotTempPath != tempPath {
			t.Fatalf("replacement temporary path = %q, want %q", gotTempPath, tempPath)
		}
		if gotPath != path {
			t.Fatalf("replacement destination path = %q, want %q", gotPath, path)
		}
		if _, err := os.Stat(gotTempPath); err != nil {
			t.Fatalf("temporary lockfile unavailable during replacement: %v", err)
		}
		return replaceErr
	})
	if !errors.Is(err, replaceErr) {
		t.Fatalf("saveLockWithReplace() error = %v, want errors.Is(replaceErr)", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", path, err)
	}
	if string(got) != string(oldData) {
		t.Fatalf("destination data = %q, want preserved %q", got, oldData)
	}
	if _, err := os.Stat(tempPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("os.Stat(%q) error = %v, want os.ErrNotExist", tempPath, err)
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
