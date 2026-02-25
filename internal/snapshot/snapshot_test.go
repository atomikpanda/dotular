package snapshot

import (
	"os"
	"path/filepath"
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
