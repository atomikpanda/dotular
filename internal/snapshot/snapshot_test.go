package snapshot

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNewAndDiscard(t *testing.T) {
	snap, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if snap.dir == "" {
		t.Error("snapshot dir should not be empty")
	}
	if _, err := os.Stat(snap.dir); err != nil {
		t.Errorf("snapshot dir should exist: %v", err)
	}
	if err := snap.Discard(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(snap.dir); !os.IsNotExist(err) {
		t.Error("snapshot dir should be removed after Discard")
	}
}

func TestRecordExistingFileAndRestore(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "myfile.txt")
	os.WriteFile(target, []byte("original"), 0o644)

	snap, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Discard()

	// Record the file.
	if err := snap.Record(target); err != nil {
		t.Fatal(err)
	}

	// Modify the file.
	os.WriteFile(target, []byte("modified"), 0o644)

	// Restore.
	if err := snap.Restore(); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(target)
	if string(data) != "original" {
		t.Errorf("after restore: %q, want %q", string(data), "original")
	}
}

func TestRecordNonExistentFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "newfile.txt")

	snap, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Discard()

	if err := snap.Record(target); err != nil {
		t.Fatal(err)
	}

	// Create the file.
	os.WriteFile(target, []byte("created"), 0o644)

	// Restore should remove it.
	if err := snap.Restore(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Error("file should be removed after rollback")
	}
}

func TestRecordDuplicateIsNoop(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "file.txt")
	os.WriteFile(target, []byte("data"), 0o644)

	snap, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Discard()

	if err := snap.Record(target); err != nil {
		t.Fatal(err)
	}
	if err := snap.Record(target); err != nil {
		t.Fatal(err)
	}
	// Should only have one entry.
	if len(snap.saved) != 1 {
		t.Errorf("expected 1 saved entry, got %d", len(snap.saved))
	}
}

func TestRecordDuplicateCreatedIsNoop(t *testing.T) {
	snap, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Discard()

	target := "/tmp/dotular-test-nonexistent-" + t.Name()
	if err := snap.Record(target); err != nil {
		t.Fatal(err)
	}
	if err := snap.Record(target); err != nil {
		t.Fatal(err)
	}
	if len(snap.created) != 1 {
		t.Errorf("expected 1 created entry, got %d", len(snap.created))
	}
}

func TestRecordDirectory(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "mydir")
	os.MkdirAll(srcDir, 0o755)
	os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("aaa"), 0o644)

	snap, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Discard()

	if err := snap.Record(srcDir); err != nil {
		t.Fatal(err)
	}

	// Remove the directory.
	os.RemoveAll(srcDir)

	// Restore.
	if err := snap.Restore(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(srcDir, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "aaa" {
		t.Errorf("restored file = %q", string(data))
	}
}

// A recorded symlink must come back as a symlink pointing at the same target,
// even if the apply replaced it with a regular file.
func TestRecordSymlinkRestoresASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	os.WriteFile(target, []byte("pointed at"), 0o644)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	snap, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Discard()

	if err := snap.Record(link); err != nil {
		t.Fatal(err)
	}

	// Replace the symlink with a regular file, as an apply would.
	os.Remove(link)
	os.WriteFile(link, []byte("not a link any more"), 0o644)

	if err := snap.Restore(); err != nil {
		t.Fatal(err)
	}

	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("restored path is not a symlink: %v", err)
	}
	if got != target {
		t.Errorf("link target = %q, want %q", got, target)
	}
	// Restoring must not have written through the link into its target.
	if data, _ := os.ReadFile(target); string(data) != "pointed at" {
		t.Errorf("symlink target was overwritten: %q", data)
	}
}
