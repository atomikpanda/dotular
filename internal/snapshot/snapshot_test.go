package snapshot

import (
	"bytes"
	"errors"
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

func TestRecordFileRestoresOverReplacementDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Discard()
	if err := snap.Record(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "replacement"), []byte("directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := snap.Restore(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("restored mode = %v, want regular file", info.Mode())
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(content); got != "original" {
		t.Fatalf("restored content = %q, want original", got)
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

// If the apply replaced the recorded file with a symlink (what a link item
// does), restoring must remove the link rather than write through it into the
// repo-side file it points at.
func TestRestoreDoesNotWriteThroughASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo-copy")
	os.WriteFile(repo, []byte("repo version"), 0o644)
	dest := filepath.Join(dir, "system-copy")
	os.WriteFile(dest, []byte("system version"), 0o644)

	snap, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Discard()

	if err := snap.Record(dest); err != nil {
		t.Fatal(err)
	}

	// Replace the destination with a symlink into the repo, as a link item does.
	os.Remove(dest)
	if err := os.Symlink(repo, dest); err != nil {
		t.Fatal(err)
	}

	if err := snap.Restore(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Lstat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("restore left a symlink where a regular file had been")
	}
	if data, _ := os.ReadFile(dest); string(data) != "system version" {
		t.Errorf("destination = %q, want %q", data, "system version")
	}
	if data, _ := os.ReadFile(repo); string(data) != "repo version" {
		t.Errorf("restore wrote through the symlink into the repo copy: %q", data)
	}
}

func TestRecordMissingPathOwnsHighestMissingAncestor(t *testing.T) {
	existingParent := t.TempDir()
	createdRoot := filepath.Join(existingParent, "created")
	target := filepath.Join(createdRoot, "nested", "tool")

	snap, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Discard()

	if err := snap.Record(target); err != nil {
		t.Fatal(err)
	}
	if len(snap.created) != 1 || snap.created[0] != createdRoot {
		t.Fatalf("created roots = %v, want [%s]", snap.created, createdRoot)
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("installed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := snap.Restore(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Lstat(createdRoot); !os.IsNotExist(err) {
		t.Errorf("created root still exists after restore: %v", err)
	}
	if _, err := os.Lstat(existingParent); err != nil {
		t.Errorf("pre-existing parent was removed: %v", err)
	}
}

func TestRestoreMissingPathUsesCapturedSymlinkParentTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}

	root := t.TempDir()
	capturedParent := t.TempDir()
	replacementParent := t.TempDir()
	link := filepath.Join(root, "parent")
	if err := os.Symlink(capturedParent, link); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(link, "created", "tool")

	snap, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Discard()
	if err := snap.Record(target); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("installed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(replacementParent, link); err != nil {
		t.Fatal(err)
	}
	replacementTarget := filepath.Join(replacementParent, "created", "tool")
	if err := os.MkdirAll(filepath.Dir(replacementTarget), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacementTarget, []byte("pre-existing"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := snap.Restore(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(capturedParent, "created")); !os.IsNotExist(err) {
		t.Errorf("captured created root still exists after restore: %v", err)
	}
	if data, err := os.ReadFile(replacementTarget); err != nil || string(data) != "pre-existing" {
		t.Errorf("replacement symlink target = %q, %v; want pre-existing data untouched", data, err)
	}
}

func TestRecordMissingPathsDeduplicatesSharedCreatedAncestor(t *testing.T) {
	existingParent := t.TempDir()
	createdRoot := filepath.Join(existingParent, "created")
	targets := []string{
		filepath.Join(createdRoot, "one", "tool"),
		filepath.Join(createdRoot, "two", "tool"),
	}

	snap, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Discard()

	for _, target := range targets {
		if err := snap.Record(target); err != nil {
			t.Fatal(err)
		}
	}
	if len(snap.created) != 1 || snap.created[0] != createdRoot {
		t.Fatalf("created roots = %v, want one deterministic root %s", snap.created, createdRoot)
	}

	for _, target := range targets {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("installed"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := snap.Restore(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(createdRoot); !os.IsNotExist(err) {
		t.Errorf("shared created root still exists after restore: %v", err)
	}
}

func TestRestoreNestedRecordsInReverseCaptureOrder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}

	root := t.TempDir()
	tree := filepath.Join(root, "tree")
	nestedDir := filepath.Join(tree, "nested")
	file := filepath.Join(nestedDir, "config")
	link := filepath.Join(nestedDir, "current")
	if err := os.MkdirAll(nestedDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tree, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(nestedDir, 0o710); err != nil {
		t.Fatal(err)
	}
	wantBytes := []byte{0x00, 0xff, 'o', 'l', 'd'}
	if err := os.WriteFile(file, wantBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("config", link); err != nil {
		t.Fatal(err)
	}

	snap, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Discard()

	for _, path := range []string{tree, file, link} {
		if err := snap.Record(path); err != nil {
			t.Fatal(err)
		}
	}
	for i, want := range []string{tree, file, link} {
		if snap.saved[i].destination != want {
			t.Fatalf("saved[%d].destination = %s, want %s", i, snap.saved[i].destination, want)
		}
	}

	if err := os.RemoveAll(tree); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other", link); err != nil {
		t.Fatal(err)
	}

	if err := snap.Restore(); err != nil {
		t.Fatal(err)
	}
	gotBytes, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Errorf("restored bytes = %v, want %v", gotBytes, wantBytes)
	}
	fileInfo, err := os.Lstat(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("restored file mode = %04o, want 0600", got)
	}
	dirInfo, err := os.Lstat(nestedDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o710 {
		t.Errorf("restored directory mode = %04o, want 0710", got)
	}
	gotTarget, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("restored path is not a symlink: %v", err)
	}
	if gotTarget != "config" {
		t.Errorf("restored symlink target = %q, want %q", gotTarget, "config")
	}
}

func TestRestoreJoinsEveryFailureInReverseOrder(t *testing.T) {
	root := t.TempDir()
	existing := []string{
		filepath.Join(root, "first"),
		filepath.Join(root, "second"),
	}
	for _, path := range existing {
		if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	snap, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Discard()
	for _, path := range existing {
		if err := snap.Record(path); err != nil {
			t.Fatal(err)
		}
	}

	created := []string{
		filepath.Join(root, "created-first"),
		filepath.Join(root, "created-second"),
	}
	for _, path := range created {
		if err := snap.Record(path); err != nil {
			t.Fatal(err)
		}
	}

	restoreFirst := errors.New("restore first")
	restoreSecond := errors.New("restore second")
	removeFirst := errors.New("remove first")
	removeSecond := errors.New("remove second")
	wantErrors := map[string]error{
		existing[0]: restoreFirst,
		existing[1]: restoreSecond,
		created[0]:  removeFirst,
		created[1]:  removeSecond,
	}
	var calls []string
	originalRestore := restoreSavedPath
	originalRemove := removeCreatedPath
	restoreSavedPath = func(record savedRecord) error {
		calls = append(calls, record.destination)
		return wantErrors[record.destination]
	}
	removeCreatedPath = func(path string) error {
		calls = append(calls, path)
		return wantErrors[path]
	}
	t.Cleanup(func() {
		restoreSavedPath = originalRestore
		removeCreatedPath = originalRemove
	})

	err = snap.Restore()
	if err == nil {
		t.Fatal("Restore() error = nil, want every restore and removal failure")
	}
	for _, want := range []error{restoreFirst, restoreSecond, removeFirst, removeSecond} {
		if !errors.Is(err, want) {
			t.Errorf("Restore() error = %v, want errors.Is(_, %q)", err, want)
		}
	}
	wantCalls := []string{existing[1], existing[0], created[1], created[0]}
	if len(calls) != len(wantCalls) {
		t.Fatalf("restore calls = %v, want %v", calls, wantCalls)
	}
	for i := range wantCalls {
		if calls[i] != wantCalls[i] {
			t.Errorf("restore calls = %v, want %v", calls, wantCalls)
			break
		}
	}
}

func TestDiscardReturnsRemovalFailure(t *testing.T) {
	snap, err := New()
	if err != nil {
		t.Fatal(err)
	}
	discardErr := errors.New("discard failed")
	originalDiscard := discardSnapshotDir
	discardSnapshotDir = func(path string) error {
		if path != snap.dir {
			t.Fatalf("discard path = %s, want %s", path, snap.dir)
		}
		return discardErr
	}
	t.Cleanup(func() {
		discardSnapshotDir = originalDiscard
		os.RemoveAll(snap.dir)
	})

	if err := snap.Discard(); !errors.Is(err, discardErr) {
		t.Fatalf("Discard() error = %v, want errors.Is(_, %q)", err, discardErr)
	}
}
