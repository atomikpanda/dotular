package actions

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/atomikpanda/dotular/internal/color"
	"github.com/atomikpanda/dotular/internal/platform"
)

// DirectoryAction manages a whole directory tree between the repo and the system.
//
// Supported directions:
//   - push (default): copy repo directory → system directory.
//   - pull: copy system directory → repo directory.
//   - sync: push if system dir missing; pull if repo dir missing; push if both
//     present (full per-file directory sync is not yet implemented — use file
//     items for fine-grained sync control).
//
// Link=true creates a symlink at the system destination pointing to the repo
// directory (equivalent to permanent push, always in sync).
//
// Idempotency: DirectoryAction implements Idempotent for link items. It
// verifies that the symlink exists and resolves to the correct source path.
type DirectoryAction struct {
	Source      string // repo-side directory path
	Destination string // system-side parent directory (may contain ~ / $VARS)
	Direction   string // "push" | "pull" | "sync"
	Link        bool
	Permissions string // applied to every file written (optional)
}

// ResolvedTarget returns the fully expanded destination directory path.
// If the destination's basename matches the source basename or has a file
// extension, it is treated as the complete path. Otherwise the source basename
// is appended. A trailing "/" always forces directory treatment (append basename).
func (a *DirectoryAction) ResolvedTarget() string {
	expanded := platform.ExpandPath(a.Destination)
	base := filepath.Base(expanded)
	srcBase := filepath.Base(a.Source)
	// If the destination already ends with the source directory name, use as-is.
	if base == srcBase {
		return expanded
	}
	// If destination has a trailing slash, always treat as parent directory.
	if strings.HasSuffix(a.Destination, "/") {
		return filepath.Join(expanded, srcBase)
	}
	return filepath.Join(expanded, srcBase)
}

// ResolvedDir returns the parent directory of the resolved target.
func (a *DirectoryAction) ResolvedDir() string {
	return filepath.Dir(a.ResolvedTarget())
}

func (a *DirectoryAction) Describe() string {
	dest := a.ResolvedTarget()
	if a.Link {
		return fmt.Sprintf("link-dir  %s -> %s", a.Source, dest)
	}
	switch a.Direction {
	case "pull":
		return fmt.Sprintf("pull-dir  %s <- %s", a.Source, dest)
	case "sync":
		return fmt.Sprintf("sync-dir  %s <-> %s", a.Source, dest)
	default:
		return fmt.Sprintf("push-dir  %s -> %s", a.Source, dest)
	}
}

// IsApplied implements Idempotent for link items.
func (a *DirectoryAction) IsApplied(ctx context.Context) (bool, error) {
	if !a.Link {
		return false, nil
	}
	target := a.ResolvedTarget()
	link, err := os.Readlink(target)
	if err != nil {
		return false, nil
	}
	abs, err := filepath.Abs(a.Source)
	if err != nil {
		return false, nil
	}
	return link == abs, nil
}

func (a *DirectoryAction) Run(ctx context.Context, dryRun bool) error {
	target := a.ResolvedTarget()
	dest := a.ResolvedDir()

	if dryRun {
		fmt.Printf("    %s\n", color.Dim("[dry-run] "+a.Describe()))
		return nil
	}

	if a.Link {
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return fmt.Errorf("create parent directory: %w", err)
		}
		return createDirSymlink(a.Source, target)
	}

	switch a.Direction {
	case "pull":
		return copyDir(target, a.Source)
	case "sync":
		repoExists := dirExists(a.Source)
		sysExists := dirExists(target)
		switch {
		case !repoExists && !sysExists:
			return fmt.Errorf("sync-dir: neither repo nor system directory exists (%s)", filepath.Base(a.Source))
		case repoExists && !sysExists:
			fmt.Printf("    %s\n", color.Cyan("sync-dir: system copy missing, pushing"))
			return copyDir(a.Source, target)
		case !repoExists && sysExists:
			fmt.Printf("    %s\n", color.Cyan("sync-dir: repo copy missing, pulling"))
			return copyDir(target, a.Source)
		default:
			// Both exist: push repo over system (per-file sync requires file items).
			fmt.Printf("    %s\n", color.Cyan("sync-dir: both exist, pushing repo -> system"))
			return copyDir(a.Source, target)
		}
	default: // push
		return copyDir(a.Source, target)
	}
}

// --- helpers -----------------------------------------------------------------

func createDirSymlink(src, dst string) error {
	abs, err := filepath.Abs(src)
	if err != nil {
		return fmt.Errorf("resolve source path: %w", err)
	}
	if fi, err := os.Lstat(dst); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(dst); err != nil {
				return fmt.Errorf("remove existing symlink: %w", err)
			}
		} else {
			return fmt.Errorf("destination exists and is not a symlink: %s", dst)
		}
	}
	return os.Symlink(abs, dst)
}

// copyDir recursively copies the src directory tree into dst (created if needed).
func copyDir(src, dst string) error {
	src = filepath.Clean(src)
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFilePath(path, target)
	})
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
