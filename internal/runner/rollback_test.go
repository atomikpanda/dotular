package runner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/atomikpanda/dotular/internal/config"
	"github.com/atomikpanda/dotular/internal/ui"
)

// These tests exercise what rollback actually restores, rather than asserting
// that the word "rollback" reached stderr. Each one drives a real module whose
// second item fails, so ApplyModule takes the snapshot-restore path.

// atomicRunner returns a Runner configured for a real (non-dry-run) atomic apply.
func atomicRunner() *Runner {
	r := newTestRunner(config.Config{})
	r.DryRun = false
	r.Atomic = true
	var buf bytes.Buffer
	r.Out = &buf
	r.UI = ui.New(&buf, &bytes.Buffer{})
	return r
}

// chdir points the process at dir for the duration of the test, because
// buildAction builds module-relative source paths that must resolve from cwd.
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })
}

// rollbackModule wraps item in a module that applies it and then fails.
func rollbackModule(name string, item config.Item) config.Module {
	return config.Module{
		Name:  name,
		Items: []config.Item{item, {Run: "false"}},
	}
}

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile only applies mode on creation, so set it explicitly.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func mustFail(t *testing.T, result ModuleResult) {
	t.Helper()
	if result.Err == nil {
		t.Fatal("expected the module to fail so rollback would run")
	}
}

// A pre-existing destination that a push overwrites must come back byte-for-byte.
func TestRollbackRestoresOverwrittenFileContent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	dir := t.TempDir()
	write(t, filepath.Join(dir, "rb", "conf"), "repo version", 0o644)
	dest := filepath.Join(dir, "dest", "conf")
	write(t, dest, "original system version", 0o644)
	chdir(t, dir)

	mod := rollbackModule("rb", config.Item{
		File:        "conf",
		Destination: config.PlatformMap{MacOS: filepath.Join(dir, "dest") + "/"},
		Direction:   "push",
	})
	mustFail(t, atomicRunner().ApplyModule(context.Background(), mod))

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original system version" {
		t.Errorf("rollback did not restore content: got %q, want %q", got, "original system version")
	}
}

// A destination that did not exist before the apply must not survive rollback.
func TestRollbackRemovesFileCreatedDuringApply(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	dir := t.TempDir()
	write(t, filepath.Join(dir, "rb", "conf"), "repo version", 0o644)
	dest := filepath.Join(dir, "dest", "conf")
	chdir(t, dir)

	mod := rollbackModule("rb", config.Item{
		File:        "conf",
		Destination: config.PlatformMap{MacOS: filepath.Join(dir, "dest") + "/"},
		Direction:   "push",
	})
	mustFail(t, atomicRunner().ApplyModule(context.Background(), mod))

	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("rollback left a file it created: %s (stat err = %v)", dest, err)
	}
}

// Restoring a 0600 file must not widen its mode — dotular advertises
// permissions: "0600" for secrets, so a rollback that relaxes them is a leak.
func TestRollbackPreservesFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	dir := t.TempDir()
	write(t, filepath.Join(dir, "rb", "secret"), "new secret", 0o644)
	dest := filepath.Join(dir, "dest", "secret")
	write(t, dest, "original secret", 0o600)
	chdir(t, dir)

	mod := rollbackModule("rb", config.Item{
		File:        "secret",
		Destination: config.PlatformMap{MacOS: filepath.Join(dir, "dest") + "/"},
		Direction:   "push",
	})
	mustFail(t, atomicRunner().ApplyModule(context.Background(), mod))

	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("rollback widened permissions: got %#o, want %#o", got, 0o600)
	}
}

// Files inside a snapshotted directory tree must keep their modes. Restore
// removes the tree and re-copies it, so this is where mode loss is total.
func TestRollbackPreservesModesInsideDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	dir := t.TempDir()
	write(t, filepath.Join(dir, "rb", "tree", "f"), "repo version", 0o644)
	destTree := filepath.Join(dir, "dest", "tree")
	secret := filepath.Join(destTree, "secret")
	write(t, secret, "original", 0o600)
	chdir(t, dir)

	mod := rollbackModule("rb", config.Item{
		Directory:   "tree",
		Destination: config.PlatformMap{MacOS: filepath.Join(dir, "dest") + "/"},
		Direction:   "push",
	})
	mustFail(t, atomicRunner().ApplyModule(context.Background(), mod))

	info, err := os.Stat(secret)
	if err != nil {
		t.Fatalf("rollback lost a file inside the directory: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("rollback widened permissions inside directory: got %#o, want %#o", got, 0o600)
	}
}

// A link item replaces the destination with a symlink. Rolling that back must
// restore the original regular file, and must not write through the new symlink
// into the repo-side source.
func TestRollbackRestoresFileReplacedBySymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "rb", "conf")
	write(t, source, "repo version", 0o644)
	dest := filepath.Join(dir, "dest", "conf")
	write(t, dest, "original system version", 0o644)
	chdir(t, dir)

	mod := rollbackModule("rb", config.Item{
		File:        "conf",
		Destination: config.PlatformMap{MacOS: filepath.Join(dir, "dest") + "/"},
		Link:        true,
	})
	mustFail(t, atomicRunner().ApplyModule(context.Background(), mod))

	info, err := os.Lstat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("rollback left a symlink where a regular file had been")
	}
	if got, err := os.ReadFile(dest); err == nil && string(got) != "original system version" {
		t.Errorf("rollback did not restore the original file: got %q", got)
	}
	// The repo-side source must be untouched; restoring through a symlink would
	// have overwritten it with the destination's saved contents.
	repo, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(repo) != "repo version" {
		t.Errorf("rollback wrote through the symlink into the repo source: got %q", repo)
	}
}

// direction: pull writes the repo-side copy, so that is the file rollback has
// to protect.
func TestRollbackRestoresRepoSideOnPull(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "rb", "conf")
	write(t, source, "repo original", 0o644)
	dest := filepath.Join(dir, "dest", "conf")
	write(t, dest, "system version", 0o644)
	chdir(t, dir)

	mod := rollbackModule("rb", config.Item{
		File:        "conf",
		Destination: config.PlatformMap{MacOS: filepath.Join(dir, "dest") + "/"},
		Direction:   "pull",
	})
	mustFail(t, atomicRunner().ApplyModule(context.Background(), mod))

	got, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "repo original" {
		t.Errorf("rollback did not restore the repo-side file that pull overwrote: got %q, want %q", got, "repo original")
	}
}
