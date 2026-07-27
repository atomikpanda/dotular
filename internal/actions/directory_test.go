package actions

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDirectoryActionResolvedTarget(t *testing.T) {
	a := &DirectoryAction{Source: "nvim", Destination: "/home/user/.config/"}
	got := a.ResolvedTarget()
	if filepath.Base(got) != "nvim" {
		t.Errorf("ResolvedTarget() base = %q, want nvim", filepath.Base(got))
	}
}

func TestDirectoryActionResolvedTargetSameBase(t *testing.T) {
	a := &DirectoryAction{Source: "nvim", Destination: "/home/user/.config/nvim"}
	got := a.ResolvedTarget()
	if got != "/home/user/.config/nvim" {
		t.Errorf("ResolvedTarget() = %q", got)
	}
}

func TestDirectoryActionDescribe(t *testing.T) {
	tests := []struct {
		name      string
		action    DirectoryAction
		contains  string
	}{
		{"push", DirectoryAction{Source: "dir", Destination: "/tmp/", Direction: "push"}, "push-dir"},
		{"pull", DirectoryAction{Source: "dir", Destination: "/tmp/", Direction: "pull"}, "pull-dir"},
		{"sync", DirectoryAction{Source: "dir", Destination: "/tmp/", Direction: "sync"}, "sync-dir"},
		{"link", DirectoryAction{Source: "dir", Destination: "/tmp/", Link: true}, "link-dir"},
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

func TestDirectoryActionIsAppliedLink(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "source")
	os.MkdirAll(srcDir, 0o755)
	dstDir := filepath.Join(dir, "dest", "source")
	os.MkdirAll(filepath.Dir(dstDir), 0o755)

	absSrc, _ := filepath.Abs(srcDir)
	os.Symlink(absSrc, dstDir)

	a := &DirectoryAction{Source: srcDir, Destination: filepath.Join(dir, "dest") + "/", Link: true}
	applied, err := a.IsApplied(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Error("expected IsApplied=true for correct dir symlink")
	}
}

func TestDirectoryActionIsAppliedNotLink(t *testing.T) {
	a := &DirectoryAction{Source: "dir", Destination: "/tmp/", Link: false}
	applied, _ := a.IsApplied(context.Background())
	if applied {
		t.Error("expected IsApplied=false for non-link")
	}
}

func TestDirExists(t *testing.T) {
	dir := t.TempDir()
	if !dirExists(dir) {
		t.Error("expected true for existing dir")
	}

	f := filepath.Join(dir, "file.txt")
	os.WriteFile(f, []byte("x"), 0o644)
	if dirExists(f) {
		t.Error("expected false for file (not dir)")
	}

	if dirExists(filepath.Join(dir, "nope")) {
		t.Error("expected false for non-existent")
	}
}

func TestDirectoryActionRunPush(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "f.txt"), []byte("data"), 0o644)

	destParent := filepath.Join(dir, "dest")

	a := &DirectoryAction{
		Source:      src,
		Destination: destParent + "/",
		Direction:   "push",
	}
	if err := a.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(destParent, "src", "f.txt"))
	if string(data) != "data" {
		t.Errorf("pushed data = %q", string(data))
	}
}

func TestDirectoryActionRunDryRun(t *testing.T) {
	a := &DirectoryAction{Source: "dir", Destination: "/tmp/", Direction: "push"}
	if err := a.Run(context.Background(), true); err != nil {
		t.Errorf("dry run error: %v", err)
	}
}

func TestDirectoryActionRunPull(t *testing.T) {
	dir := t.TempDir()
	sysDir := filepath.Join(dir, "system", "mydir")
	os.MkdirAll(sysDir, 0o755)
	os.WriteFile(filepath.Join(sysDir, "f.txt"), []byte("pulled"), 0o644)

	repoDir := filepath.Join(dir, "repo")

	a := &DirectoryAction{
		Source:      filepath.Join(repoDir, "mydir"),
		Destination: filepath.Join(dir, "system") + "/",
		Direction:   "pull",
	}
	// Pull copies from system to repo, but ResolvedTarget is the system dir.
	// For pull, the Run function calls copyDir(target, source).
	// Since target (system dir) exists, this should work.
	if err := a.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}
}

func TestDirectoryActionRunSyncRepoOnly(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "repo", "mydir")
	os.MkdirAll(srcDir, 0o755)
	os.WriteFile(filepath.Join(srcDir, "f.txt"), []byte("data"), 0o644)
	destParent := filepath.Join(dir, "system")

	a := &DirectoryAction{
		Source:      srcDir,
		Destination: destParent + "/",
		Direction:   "sync",
	}
	if err := a.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}
}

func TestDirectoryActionRunSyncSysOnly(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "repo", "mydir") // doesn't exist
	destParent := filepath.Join(dir, "system")
	sysDir := filepath.Join(destParent, "mydir")
	os.MkdirAll(sysDir, 0o755)
	os.WriteFile(filepath.Join(sysDir, "f.txt"), []byte("from sys"), 0o644)

	a := &DirectoryAction{
		Source:      srcDir,
		Destination: destParent + "/",
		Direction:   "sync",
	}
	if err := a.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}
}

func TestDirectoryActionRunSyncBoth(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "repo", "mydir")
	os.MkdirAll(srcDir, 0o755)
	os.WriteFile(filepath.Join(srcDir, "f.txt"), []byte("repo"), 0o644)
	destParent := filepath.Join(dir, "system")
	sysDir := filepath.Join(destParent, "mydir")
	os.MkdirAll(sysDir, 0o755)
	os.WriteFile(filepath.Join(sysDir, "f.txt"), []byte("sys"), 0o644)

	a := &DirectoryAction{
		Source:      srcDir,
		Destination: destParent + "/",
		Direction:   "sync",
	}
	if err := a.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}
}

func TestDirectoryActionRunSyncNeither(t *testing.T) {
	a := &DirectoryAction{
		Source:      "/tmp/dotular-nonexistent-src",
		Destination: "/tmp/dotular-nonexistent-dst/",
		Direction:   "sync",
	}
	err := a.Run(context.Background(), false)
	if err == nil {
		t.Error("expected error when neither dir exists")
	}
}

func TestDirectoryActionRunLink(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	os.MkdirAll(src, 0o755)
	destParent := filepath.Join(dir, "dest")

	a := &DirectoryAction{
		Source:      src,
		Destination: destParent + "/",
		Link:        true,
	}
	if err := a.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(destParent, "src")
	linkDest, err := os.Readlink(target)
	if err != nil {
		t.Fatal(err)
	}
	absSrc, _ := filepath.Abs(src)
	if linkDest != absSrc {
		t.Errorf("link = %q, want %q", linkDest, absSrc)
	}
}

func TestCreateDirSymlinkOverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	os.MkdirAll(src, 0o755)
	dst := filepath.Join(dir, "link")

	// Create an existing symlink pointing elsewhere.
	other := filepath.Join(dir, "other")
	os.MkdirAll(other, 0o755)
	absOther, _ := filepath.Abs(other)
	os.Symlink(absOther, dst)

	// Overwriting existing symlink should succeed.
	if err := createDirSymlink(src, dst); err != nil {
		t.Fatal(err)
	}
	got, _ := os.Readlink(dst)
	absSrc, _ := filepath.Abs(src)
	if got != absSrc {
		t.Errorf("symlink = %q, want %q", got, absSrc)
	}
}

func TestCreateDirSymlinkFailsOnExistingDir(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	os.MkdirAll(src, 0o755)
	dst := filepath.Join(dir, "existing-dir")
	os.MkdirAll(dst, 0o755)

	// Should fail because dst is a real directory (not a symlink).
	err := createDirSymlink(src, dst)
	if err == nil {
		t.Error("expected error when destination is a real directory")
	}
}

func TestDirectoryActionIsAppliedLinkWrong(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "source")
	os.MkdirAll(srcDir, 0o755)
	otherDir := filepath.Join(dir, "other")
	os.MkdirAll(otherDir, 0o755)
	dstDir := filepath.Join(dir, "dest", "source")
	os.MkdirAll(filepath.Dir(dstDir), 0o755)

	absOther, _ := filepath.Abs(otherDir)
	os.Symlink(absOther, dstDir)

	a := &DirectoryAction{Source: srcDir, Destination: filepath.Join(dir, "dest") + "/", Link: true}
	applied, err := a.IsApplied(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Error("expected IsApplied=false for wrong symlink target")
	}
}

func TestDirectoryActionIsAppliedLinkNotExists(t *testing.T) {
	a := &DirectoryAction{
		Source:      "/tmp/dotular-test-nonexistent-src",
		Destination: "/tmp/dotular-test-nonexistent-dst/",
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

func TestDirectoryActionResolvedDir(t *testing.T) {
	a := &DirectoryAction{Source: "nvim", Destination: "/home/user/.config/"}
	got := a.ResolvedDir()
	if got != "/home/user/.config" {
		t.Errorf("ResolvedDir() = %q", got)
	}
}

// WritePaths must name the side the direction actually writes, because that is
// what the runner snapshots for rollback.
func TestDirectoryActionWritePaths(t *testing.T) {
	target := filepath.Join("/tmp", "tree")
	tests := []struct {
		name   string
		action DirectoryAction
		want   []string
	}{
		{"push", DirectoryAction{Source: "m/tree", Destination: "/tmp/", Direction: "push"}, []string{target}},
		{"pull", DirectoryAction{Source: "m/tree", Destination: "/tmp/", Direction: "pull"}, []string{"m/tree"}},
		{"sync", DirectoryAction{Source: "m/tree", Destination: "/tmp/", Direction: "sync"}, []string{target, "m/tree"}},
		{"link", DirectoryAction{Source: "m/tree", Destination: "/tmp/", Direction: "pull", Link: true}, []string{target}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.action.WritePaths()
			if len(got) != len(tt.want) {
				t.Fatalf("WritePaths() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("WritePaths()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// permissions: on a directory item must reach every file the copy writes —
// the bundled skill recommends "0600" for directories of credentials.
func TestDirectoryActionAppliesPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "creds")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(src, "token"), []byte("t"), 0o644)
	os.WriteFile(filepath.Join(src, "sub", "nested"), []byte("n"), 0o666)

	destParent := filepath.Join(dir, "system")
	a := &DirectoryAction{
		Source:      src,
		Destination: destParent + "/",
		Direction:   "push",
		Permissions: "0600",
	}
	if err := a.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(destParent, "creds")
	for _, rel := range []string{"token", filepath.Join("sub", "nested")} {
		info, err := os.Stat(filepath.Join(target, rel))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %#o, want %#o", rel, got, 0o600)
		}
	}
	// The directory itself is not a file; its copied mode stands.
	info, err := os.Stat(filepath.Join(target, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("sub dir mode = %#o, want %#o", got, 0o755)
	}
}

func TestDirectoryActionInvalidPermissions(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "tree")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(src, "f"), []byte("f"), 0o644)

	a := &DirectoryAction{
		Source:      src,
		Destination: filepath.Join(dir, "system") + "/",
		Direction:   "push",
		Permissions: "not-octal",
	}
	if err := a.Run(context.Background(), false); err == nil {
		t.Error("expected an error for an unparseable permissions value")
	}
}
