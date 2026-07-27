package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

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

func perm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestCopyFileCopiesContentAndMode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	write(t, src, "hello", 0o600)

	if err := CopyFile(src, dst); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("contents = %q, want %q", data, "hello")
	}
	if runtime.GOOS != "windows" {
		if got := perm(t, dst); got != 0o600 {
			t.Errorf("mode = %#o, want %#o", got, 0o600)
		}
	}
}

// Overwriting must adopt the source's mode, not keep the destination's.
func TestCopyFileOverwritesModeToo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	write(t, src, "new", 0o600)
	write(t, dst, "old", 0o666)

	if err := CopyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	if got := perm(t, dst); got != 0o600 {
		t.Errorf("mode = %#o, want %#o", got, 0o600)
	}
}

func TestCopyFileMissingSource(t *testing.T) {
	dir := t.TempDir()
	if err := CopyFile(filepath.Join(dir, "nope"), filepath.Join(dir, "dst")); err == nil {
		t.Error("expected an error for a missing source")
	}
}

func TestCopyFileUnwritableDestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	write(t, src, "hello", 0o644)
	if err := CopyFile(src, filepath.Join(dir, "missing-dir", "dst")); err == nil {
		t.Error("expected an error for a destination whose parent does not exist")
	}
}

func TestCopySymlinkStaysASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	write(t, target, "pointed at", 0o644)
	src := filepath.Join(dir, "link")
	if err := os.Symlink(target, src); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "copied-link")
	// A pre-existing regular file at dst must be replaced, not written through.
	write(t, dst, "in the way", 0o644)

	if err := CopySymlink(src, dst); err != nil {
		t.Fatal(err)
	}

	got, err := os.Readlink(dst)
	if err != nil {
		t.Fatalf("dst is not a symlink: %v", err)
	}
	if got != target {
		t.Errorf("link target = %q, want %q", got, target)
	}
}

func TestCopySymlinkNonSymlinkSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "regular")
	write(t, src, "not a link", 0o644)
	if err := CopySymlink(src, filepath.Join(dir, "dst")); err == nil {
		t.Error("expected an error for a non-symlink source")
	}
}

func TestCopyDirPreservesModesAndSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	write(t, filepath.Join(src, "secret"), "s3cret", 0o600)
	write(t, filepath.Join(src, "sub", "script"), "#!/bin/sh", 0o755)
	if err := os.Symlink("secret", filepath.Join(src, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(src, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "dst")
	if err := CopyDir(src, dst); err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]os.FileMode{
		filepath.Join(dst, "secret"):        0o600,
		filepath.Join(dst, "sub"):           0o700,
		filepath.Join(dst, "sub", "script"): 0o755,
	} {
		if got := perm(t, path); got != want {
			t.Errorf("%s mode = %#o, want %#o", path, got, want)
		}
	}
	if got, err := os.Readlink(filepath.Join(dst, "alias")); err != nil {
		t.Errorf("alias is not a symlink: %v", err)
	} else if got != "secret" {
		t.Errorf("alias target = %q, want %q", got, "secret")
	}
	if data, err := os.ReadFile(filepath.Join(dst, "sub", "script")); err != nil || string(data) != "#!/bin/sh" {
		t.Errorf("sub/script = %q, %v", data, err)
	}
}

// A directory the owner cannot write must still be copyable: the mode is
// applied after its contents, not before.
func TestCopyDirHandlesUnwritableSourceDirectory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a non-root Unix user")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	locked := filepath.Join(src, "locked")
	write(t, filepath.Join(locked, "f"), "data", 0o644)
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dst")
	// Both trees need owner write back before TempDir's RemoveAll can run.
	t.Cleanup(func() {
		os.Chmod(locked, 0o700)
		os.Chmod(filepath.Join(dst, "locked"), 0o700)
	})

	if err := CopyDir(src, dst); err != nil {
		t.Fatal(err)
	}
	if got := perm(t, filepath.Join(dst, "locked")); got != 0o500 {
		t.Errorf("locked mode = %#o, want %#o", got, 0o500)
	}
	if got := perm(t, filepath.Join(dst, "locked", "f")); got != 0o644 {
		t.Errorf("locked/f mode = %#o, want %#o", got, 0o644)
	}
}

func TestCopyDirMissingSource(t *testing.T) {
	dir := t.TempDir()
	if err := CopyDir(filepath.Join(dir, "nope"), filepath.Join(dir, "dst")); err == nil {
		t.Error("expected an error for a missing source")
	}
}

// defaultCreateMode is the mode a freshly created file gets on this machine:
// the default mode narrowed by the process umask. Asserting against it keeps the
// "no mode propagation" tests independent of the umask the suite runs under.
func defaultCreateMode(t *testing.T, dir string) os.FileMode {
	t.Helper()
	ref := filepath.Join(dir, "umask-reference")
	f, err := os.Create(ref)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(ref)
	return perm(t, ref) & defaultFileMode
}

// The whole point of CopyContents: a restrictive destination must survive a
// push from a repo copy that git left at 0644.
func TestCopyContentsLeavesExistingDestinationMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	write(t, src, "repo copy", 0o644)
	write(t, dst, "system copy", 0o600)

	if err := CopyContents(src, dst); err != nil {
		t.Fatal(err)
	}

	if got := perm(t, dst); got != 0o600 {
		t.Errorf("mode = %#o, want %#o — CopyContents must not widen the destination", got, 0o600)
	}
	if data, _ := os.ReadFile(dst); string(data) != "repo copy" {
		t.Errorf("contents = %q, want %q", data, "repo copy")
	}
}

func TestCopyContentsNewDestinationGetsDefaultMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	write(t, src, "repo copy", 0o600)
	dst := filepath.Join(dir, "dst")

	if err := CopyContents(src, dst); err != nil {
		t.Fatal(err)
	}

	want := defaultCreateMode(t, dir)
	if got := perm(t, dst); got != want {
		t.Errorf("mode = %#o, want %#o — a created destination gets the default, not the source's mode", got, want)
	}
}

func TestCopyContentsMissingSource(t *testing.T) {
	dir := t.TempDir()
	if err := CopyContents(filepath.Join(dir, "nope"), filepath.Join(dir, "dst")); err == nil {
		t.Error("expected an error for a missing source")
	}
}

func TestCopyDirContentsDoesNotPropagateModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	dir := t.TempDir()

	// The repo side, as a git checkout would leave it: everything 0644/0755.
	src := filepath.Join(dir, "src")
	write(t, filepath.Join(src, "config"), "repo config", 0o644)
	write(t, filepath.Join(src, "keys", "id"), "repo key", 0o644)
	if err := os.Symlink("config", filepath.Join(src, "alias")); err != nil {
		t.Fatal(err)
	}

	// The system side, locked down by the user.
	dst := filepath.Join(dir, "dst")
	write(t, filepath.Join(dst, "config"), "system config", 0o600)
	write(t, filepath.Join(dst, "keys", "id"), "system key", 0o600)
	if err := os.Chmod(filepath.Join(dst, "keys"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := CopyDirContents(src, dst); err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]os.FileMode{
		filepath.Join(dst, "config"):     0o600,
		filepath.Join(dst, "keys"):       0o700,
		filepath.Join(dst, "keys", "id"): 0o600,
	} {
		if got := perm(t, path); got != want {
			t.Errorf("%s mode = %#o, want %#o — existing destinations keep their own modes", path, got, want)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(dst, "config")); string(data) != "repo config" {
		t.Errorf("contents were not copied: %q", data)
	}
	// Symlinks are recreated regardless of which mode policy is in play.
	if got, err := os.Readlink(filepath.Join(dst, "alias")); err != nil || got != "config" {
		t.Errorf("alias = %q, %v; want a symlink to %q", got, err, "config")
	}
}

func TestCopyDirContentsCreatesWithDefaultModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	write(t, filepath.Join(src, "sub", "f"), "data", 0o600)
	if err := os.Chmod(filepath.Join(src, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "dst")
	if err := CopyDirContents(src, dst); err != nil {
		t.Fatal(err)
	}

	if got, want := perm(t, filepath.Join(dst, "sub", "f")), defaultCreateMode(t, dir); got != want {
		t.Errorf("created file mode = %#o, want %#o", got, want)
	}
	if got := perm(t, filepath.Join(dst, "sub")); got == 0o700 {
		t.Errorf("created directory mode = %#o; the source's mode must not be propagated", got)
	}
}
