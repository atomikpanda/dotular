package actions

import (
	"context"
	"os"
	"path/filepath"
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

func TestCopyDir(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	os.MkdirAll(filepath.Join(src, "sub"), 0o755)
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("aaa"), 0o644)
	os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("bbb"), 0o644)

	if err := copyDir(src, dst); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dst, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "aaa" {
		t.Errorf("a.txt = %q", string(data))
	}

	data, err = os.ReadFile(filepath.Join(dst, "sub", "b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "bbb" {
		t.Errorf("sub/b.txt = %q", string(data))
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
