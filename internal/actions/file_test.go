package actions

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileActionResolvedTarget(t *testing.T) {
	tests := []struct {
		name        string
		source      string
		destination string
		wantSuffix  string
	}{
		{"dir destination", ".vimrc", "/home/user/", ".vimrc"},
		{"file destination", ".wezterm.lua", "/home/user/.wezterm.lua", ".wezterm.lua"},
		{"dir no trailing slash", ".vimrc", "/home/user", ".vimrc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &FileAction{Source: tt.source, Destination: tt.destination}
			got := a.ResolvedTarget()
			if filepath.Base(got) != tt.wantSuffix {
				t.Errorf("ResolvedTarget() base = %q, want %q (full: %q)", filepath.Base(got), tt.wantSuffix, got)
			}
		})
	}
}

func TestFileActionDescribe(t *testing.T) {
	tests := []struct {
		name      string
		action    FileAction
		contains  string
	}{
		{"push", FileAction{Source: "a", Destination: "/tmp/", Direction: "push"}, "push"},
		{"pull", FileAction{Source: "a", Destination: "/tmp/", Direction: "pull"}, "pull"},
		{"sync", FileAction{Source: "a", Destination: "/tmp/", Direction: "sync"}, "sync"},
		{"link", FileAction{Source: "a", Destination: "/tmp/", Link: true}, "link"},
		{"encrypted", FileAction{Source: "a", Destination: "/tmp/", Encrypted: true}, "[encrypted]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.action.Describe()
			if got == "" {
				t.Error("Describe() should not be empty")
			}
		})
	}
}

func TestFileActionIsAppliedLink(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.txt")
	dst := filepath.Join(dir, "link.txt")
	os.WriteFile(src, []byte("data"), 0o644)

	// Create symlink.
	absSrc, _ := filepath.Abs(src)
	os.Symlink(absSrc, dst)

	a := &FileAction{
		Source:      src,
		Destination: dst,
		Link:        true,
	}
	applied, err := a.IsApplied(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Error("expected IsApplied=true for correct symlink")
	}
}

func TestFileActionIsAppliedLinkWrong(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.txt")
	dst := filepath.Join(dir, "link.txt")
	other := filepath.Join(dir, "other.txt")
	os.WriteFile(src, []byte("data"), 0o644)
	os.WriteFile(other, []byte("other"), 0o644)

	os.Symlink(other, dst)

	a := &FileAction{Source: src, Destination: dst, Link: true}
	applied, _ := a.IsApplied(context.Background())
	if applied {
		t.Error("expected IsApplied=false for wrong symlink target")
	}
}

func TestFileActionIsAppliedNotLink(t *testing.T) {
	a := &FileAction{Source: "a", Destination: "/tmp/", Link: false}
	applied, _ := a.IsApplied(context.Background())
	if applied {
		t.Error("expected IsApplied=false for non-link action")
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	os.WriteFile(src, []byte("hello"), 0o644)

	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(dst)
	if string(data) != "hello" {
		t.Errorf("copied data = %q", string(data))
	}
}

func TestFilesEqual(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	c := filepath.Join(dir, "c.txt")

	os.WriteFile(a, []byte("same"), 0o644)
	os.WriteFile(b, []byte("same"), 0o644)
	os.WriteFile(c, []byte("different"), 0o644)

	eq, err := filesEqual(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if !eq {
		t.Error("expected equal files")
	}

	eq, err = filesEqual(a, c)
	if err != nil {
		t.Fatal(err)
	}
	if eq {
		t.Error("expected unequal files")
	}
}

func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "exists.txt")
	os.WriteFile(f, []byte("x"), 0o644)

	if !fileExists(f) {
		t.Error("expected true for existing file")
	}
	if fileExists(filepath.Join(dir, "nope.txt")) {
		t.Error("expected false for non-existing file")
	}
}

func TestCreateSymlink(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.txt")
	dst := filepath.Join(dir, "symlink.txt")
	os.WriteFile(src, []byte("data"), 0o644)

	if err := createSymlink(src, dst); err != nil {
		t.Fatal(err)
	}

	target, err := os.Readlink(dst)
	if err != nil {
		t.Fatal(err)
	}
	absSrc, _ := filepath.Abs(src)
	if target != absSrc {
		t.Errorf("symlink target = %q, want %q", target, absSrc)
	}
}

func TestCreateSymlinkOverwrite(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.txt")
	dst := filepath.Join(dir, "symlink.txt")
	os.WriteFile(src, []byte("data"), 0o644)
	os.WriteFile(dst, []byte("old"), 0o644)

	if err := createSymlink(src, dst); err != nil {
		t.Fatal(err)
	}

	target, _ := os.Readlink(dst)
	absSrc, _ := filepath.Abs(src)
	if target != absSrc {
		t.Errorf("symlink target = %q, want %q", target, absSrc)
	}
}

func TestFileActionRunPushDryRun(t *testing.T) {
	a := &FileAction{
		Source:      "test.txt",
		Destination: "/tmp/",
		Direction:   "push",
	}
	err := a.Run(context.Background(), true)
	if err != nil {
		t.Errorf("dry run should not error: %v", err)
	}
}

func TestFileActionRunPush(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.txt")
	destDir := filepath.Join(dir, "dest")
	os.WriteFile(src, []byte("content"), 0o644)

	a := &FileAction{
		Source:      src,
		Destination: destDir + "/",
		Direction:   "push",
	}
	if err := a.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(destDir, filepath.Base(src)))
	if string(data) != "content" {
		t.Errorf("pushed content = %q", string(data))
	}
}

func TestFileActionRunPull(t *testing.T) {
	dir := t.TempDir()
	sysFile := filepath.Join(dir, "system.txt")
	repoFile := filepath.Join(dir, "repo", "system.txt")
	os.WriteFile(sysFile, []byte("from system"), 0o644)

	a := &FileAction{
		Source:      repoFile,
		Destination: sysFile,
		Direction:   "pull",
	}
	if err := a.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(repoFile)
	if string(data) != "from system" {
		t.Errorf("pulled content = %q", string(data))
	}
}

func TestFileActionRunLink(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.txt")
	destDir := filepath.Join(dir, "dest")
	os.WriteFile(src, []byte("linked"), 0o644)

	a := &FileAction{
		Source:      src,
		Destination: destDir + "/",
		Link:        true,
	}
	if err := a.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(destDir, filepath.Base(src))
	linkDest, err := os.Readlink(target)
	if err != nil {
		t.Fatal(err)
	}
	absSrc, _ := filepath.Abs(src)
	if linkDest != absSrc {
		t.Errorf("link target = %q, want %q", linkDest, absSrc)
	}
}

func TestFileActionPermissions(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	destDir := filepath.Join(dir, "dest")
	os.WriteFile(src, []byte("secret"), 0o644)

	a := &FileAction{
		Source:      src,
		Destination: destDir + "/",
		Direction:   "push",
		Permissions: "0600",
	}
	if err := a.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(destDir, filepath.Base(src))
	info, _ := os.Stat(target)
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %o, want 0600", perm)
	}
}

func TestReadLine(t *testing.T) {
	r := &mockReader{data: "hello\nworld"}
	line, err := readLine(r)
	if err != nil {
		t.Fatal(err)
	}
	if line != "hello" {
		t.Errorf("readLine() = %q", line)
	}
}

type mockReader struct {
	data string
	pos  int
}

func (m *mockReader) Read(p []byte) (n int, err error) {
	if m.pos >= len(m.data) {
		return 0, nil
	}
	n = copy(p, m.data[m.pos:])
	m.pos += n
	return n, nil
}

func TestFileActionRunSyncBothExistEqual(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "repo", "test.txt")
	destDir := filepath.Join(dir, "system")
	os.MkdirAll(filepath.Join(dir, "repo"), 0o755)
	os.MkdirAll(destDir, 0o755)
	os.WriteFile(src, []byte("same content"), 0o644)
	destFile := filepath.Join(destDir, "test.txt")
	os.WriteFile(destFile, []byte("same content"), 0o644)

	a := &FileAction{
		Source:      src,
		Destination: destDir + "/",
		Direction:   "sync",
	}
	if err := a.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}
}

func TestFileActionRunSyncRepoOnlyPushes(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "repo", "test.txt")
	destDir := filepath.Join(dir, "system")
	os.MkdirAll(filepath.Join(dir, "repo"), 0o755)
	os.WriteFile(src, []byte("repo content"), 0o644)

	a := &FileAction{
		Source:      src,
		Destination: destDir + "/",
		Direction:   "sync",
	}
	if err := a.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(destDir, "test.txt"))
	if string(data) != "repo content" {
		t.Errorf("synced content = %q", string(data))
	}
}

func TestFileActionRunSyncSysOnlyPulls(t *testing.T) {
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "repo")
	os.MkdirAll(repoDir, 0o755)
	src := filepath.Join(repoDir, "test.txt") // Does not exist
	destDir := filepath.Join(dir, "system")
	os.MkdirAll(destDir, 0o755)
	sysFile := filepath.Join(destDir, "test.txt")
	os.WriteFile(sysFile, []byte("system content"), 0o644)

	a := &FileAction{
		Source:      src,
		Destination: sysFile,
		Direction:   "sync",
	}
	if err := a.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(src)
	if string(data) != "system content" {
		t.Errorf("pulled content = %q", string(data))
	}
}

func TestFileActionRunSyncNeitherExists(t *testing.T) {
	a := &FileAction{
		Source:      "/tmp/dotular-test-nonexistent-src",
		Destination: "/tmp/dotular-test-nonexistent-dst/",
		Direction:   "sync",
	}
	err := a.Run(context.Background(), false)
	if err == nil {
		t.Error("expected error when neither file exists")
	}
}

func TestFileActionPermissionsStatus(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	destDir := filepath.Join(dir, "dest")
	os.MkdirAll(destDir, 0o755)
	os.WriteFile(src, []byte("data"), 0o644)
	target := filepath.Join(destDir, "src.txt")
	os.WriteFile(target, []byte("data"), 0o644)

	a := &FileAction{
		Source:      src,
		Destination: destDir + "/",
		Permissions: "0644",
	}
	status := a.PermissionsStatus()
	if status == "" {
		t.Error("expected non-empty permissions status")
	}

	// Test with mismatched permissions.
	os.Chmod(target, 0o755)
	a2 := &FileAction{
		Source:      src,
		Destination: destDir + "/",
		Permissions: "0600",
	}
	status2 := a2.PermissionsStatus()
	if status2 == "" {
		t.Error("expected non-empty status for mismatch")
	}

	// Test with no permissions set.
	a3 := &FileAction{Source: src, Destination: destDir + "/"}
	if a3.PermissionsStatus() != "" {
		t.Error("expected empty status with no permissions")
	}

	// Test with link (should return empty).
	a4 := &FileAction{Source: src, Destination: destDir + "/", Permissions: "0600", Link: true}
	if a4.PermissionsStatus() != "" {
		t.Error("expected empty status for link")
	}

	// Test with invalid permissions.
	a5 := &FileAction{Source: src, Destination: destDir + "/", Permissions: "invalid"}
	status5 := a5.PermissionsStatus()
	if status5 == "" {
		t.Error("expected non-empty for invalid permissions")
	}
}

func TestFileActionRunPullMissingSystem(t *testing.T) {
	a := &FileAction{
		Source:      "/tmp/dotular-test-repo-file",
		Destination: "/tmp/dotular-nonexistent-system-file.txt",
		Direction:   "pull",
	}
	err := a.Run(context.Background(), false)
	if err == nil {
		t.Error("expected error for missing system file on pull")
	}
}

func TestFileActionEncryptedPushNoKey(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "secret.txt.age")
	os.WriteFile(src, []byte("encrypted"), 0o644)

	a := &FileAction{
		Source:      filepath.Join(dir, "secret.txt"),
		Destination: filepath.Join(dir, "dest") + "/",
		Direction:   "push",
		Encrypted:   true,
		AgeKey:      nil,
	}
	err := a.Run(context.Background(), false)
	if err == nil {
		t.Error("expected error for encrypted push with no key")
	}
}

func TestFileActionRunSyncBothDifferent(t *testing.T) {
	// When both exist and differ, resolveConflict is called which reads stdin.
	// We can test the branch up to that point by verifying the error doesn't crash.
	dir := t.TempDir()
	src := filepath.Join(dir, "repo", "test.txt")
	destDir := filepath.Join(dir, "system")
	os.MkdirAll(filepath.Join(dir, "repo"), 0o755)
	os.MkdirAll(destDir, 0o755)
	os.WriteFile(src, []byte("repo version"), 0o644)
	destFile := filepath.Join(destDir, "test.txt")
	os.WriteFile(destFile, []byte("system version"), 0o644)

	a := &FileAction{
		Source:      src,
		Destination: destDir + "/",
		Direction:   "sync",
	}
	// This will try to read stdin for conflict resolution.
	// With empty stdin, it should skip (default choice).
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Close() // EOF
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	err := a.Run(context.Background(), false)
	// Should not error — just skip on empty input.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFileActionEncryptedPullNoKey(t *testing.T) {
	dir := t.TempDir()
	sysFile := filepath.Join(dir, "system.txt")
	os.WriteFile(sysFile, []byte("data"), 0o644)

	a := &FileAction{
		Source:      filepath.Join(dir, "repo.txt"),
		Destination: sysFile,
		Direction:   "pull",
		Encrypted:   true,
		AgeKey:      nil,
	}
	err := a.Run(context.Background(), false)
	if err == nil {
		t.Error("expected error for encrypted pull with no key")
	}
}

func TestSyncEqualNonEncrypted(t *testing.T) {
	dir := t.TempDir()
	repoFile := filepath.Join(dir, "repo.txt")
	sysFile := filepath.Join(dir, "system.txt")
	os.WriteFile(repoFile, []byte("same"), 0o644)
	os.WriteFile(sysFile, []byte("same"), 0o644)

	a := &FileAction{Source: repoFile, Destination: dir + "/", Direction: "sync"}
	equal, err := a.syncEqual(repoFile, sysFile)
	if err != nil {
		t.Fatal(err)
	}
	if !equal {
		t.Error("expected equal")
	}
}

func TestSyncEqualNonEncryptedDifferent(t *testing.T) {
	dir := t.TempDir()
	repoFile := filepath.Join(dir, "repo.txt")
	sysFile := filepath.Join(dir, "system.txt")
	os.WriteFile(repoFile, []byte("repo"), 0o644)
	os.WriteFile(sysFile, []byte("system"), 0o644)

	a := &FileAction{Source: repoFile, Destination: dir + "/", Direction: "sync"}
	equal, err := a.syncEqual(repoFile, sysFile)
	if err != nil {
		t.Fatal(err)
	}
	if equal {
		t.Error("expected not equal")
	}
}

func TestFileActionIsAppliedLinkNotExists(t *testing.T) {
	a := &FileAction{
		Source:      "/tmp/dotular-nonexistent",
		Destination: "/tmp/dotular-nonexistent-link.txt",
		Link:        true,
	}
	applied, err := a.IsApplied(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Error("expected IsApplied=false for nonexistent link")
	}
}

func TestFileActionResolvedDir(t *testing.T) {
	a := &FileAction{Source: "test.txt", Destination: "/home/user/dest/"}
	got := a.ResolvedDir()
	if got != "/home/user/dest" {
		t.Errorf("ResolvedDir() = %q", got)
	}
}

func TestFileActionRunPushInvalidPermissions(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	destDir := filepath.Join(dir, "dest")
	os.WriteFile(src, []byte("data"), 0o644)

	a := &FileAction{
		Source:      src,
		Destination: destDir + "/",
		Direction:   "push",
		Permissions: "invalid",
	}
	err := a.Run(context.Background(), false)
	if err == nil {
		t.Error("expected error for invalid permissions")
	}
}

func TestFileActionEncryptedSyncNoKey(t *testing.T) {
	dir := t.TempDir()
	repoFile := filepath.Join(dir, "repo.txt.age")
	sysFile := filepath.Join(dir, "system.txt")
	os.WriteFile(repoFile, []byte("encrypted"), 0o644)
	os.WriteFile(sysFile, []byte("plain"), 0o644)

	a := &FileAction{
		Source:      filepath.Join(dir, "repo.txt"),
		Destination: sysFile,
		Direction:   "sync",
		Encrypted:   true,
		AgeKey:      nil,
	}
	// syncEqual for encrypted files will try to decrypt — should fail with no key.
	_, err := a.syncEqual(repoFile, sysFile)
	if err == nil {
		t.Error("expected error for encrypted sync with no key")
	}
}

func TestFileActionRunSyncConflictChoice1(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "repo", "test.txt")
	destDir := filepath.Join(dir, "system")
	os.MkdirAll(filepath.Join(dir, "repo"), 0o755)
	os.MkdirAll(destDir, 0o755)
	os.WriteFile(src, []byte("repo version"), 0o644)
	destFile := filepath.Join(destDir, "test.txt")
	os.WriteFile(destFile, []byte("system version"), 0o644)

	a := &FileAction{
		Source:      src,
		Destination: destDir + "/",
		Direction:   "sync",
	}

	// Simulate user choosing "1" (keep repo).
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte("1\n"))
	w.Close()
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	if err := a.Run(context.Background(), false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(destFile)
	if string(data) != "repo version" {
		t.Errorf("expected repo version pushed, got %q", string(data))
	}
}

func TestFileActionRunSyncConflictChoice2(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "repo", "test.txt")
	destDir := filepath.Join(dir, "system")
	os.MkdirAll(filepath.Join(dir, "repo"), 0o755)
	os.MkdirAll(destDir, 0o755)
	os.WriteFile(src, []byte("repo version"), 0o644)
	destFile := filepath.Join(destDir, "test.txt")
	os.WriteFile(destFile, []byte("system version"), 0o644)

	a := &FileAction{
		Source:      src,
		Destination: destDir + "/",
		Direction:   "sync",
	}

	// Simulate user choosing "2" (keep system).
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	w.Write([]byte("2\n"))
	w.Close()
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	if err := a.Run(context.Background(), false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(src)
	if string(data) != "system version" {
		t.Errorf("expected system version pulled, got %q", string(data))
	}
}

func TestFileActionPermissionsStatusNonexistent(t *testing.T) {
	a := &FileAction{
		Source:      "nonexistent.txt",
		Destination: "/tmp/dotular-nonexistent-dir/",
		Permissions: "0644",
	}
	// File doesn't exist — should return empty.
	if a.PermissionsStatus() != "" {
		t.Error("expected empty status for nonexistent file")
	}
}
