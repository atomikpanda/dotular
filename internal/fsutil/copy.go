// Package fsutil holds the file and directory copy used everywhere dotular
// writes to disk. Both copies preserve the source's permission bits and
// recreate symlinks as symlinks, so a copy is a faithful copy — snapshot
// rollback depends on that fidelity.
package fsutil

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// CopyFile copies the contents of src to dst, preserving src's permission
// bits. An existing dst is truncated.
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	mode := info.Mode().Perm()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy contents: %w", err)
	}
	// OpenFile's mode applies only when it creates dst, and is masked by the
	// umask even then, so set the mode explicitly.
	if err := out.Chmod(mode); err != nil {
		return fmt.Errorf("set destination mode: %w", err)
	}
	// Close reports flush errors that io.Copy cannot see; dropping it would
	// report a truncated copy as a success.
	return out.Close()
}

// CopySymlink recreates the symlink at src as a symlink at dst, pointing at the
// same (unresolved) target. An existing dst is replaced.
func CopySymlink(src, dst string) error {
	target, err := os.Readlink(src)
	if err != nil {
		return fmt.Errorf("read symlink: %w", err)
	}
	if _, err := os.Lstat(dst); err == nil {
		if err := os.RemoveAll(dst); err != nil {
			return fmt.Errorf("remove existing destination: %w", err)
		}
	}
	if err := os.Symlink(target, dst); err != nil {
		return fmt.Errorf("create symlink: %w", err)
	}
	return nil
}

// CopyDir recursively copies the src tree into dst, creating dst if needed.
// File and directory modes are preserved and symlinks are recreated rather
// than dereferenced.
func CopyDir(src, dst string) error {
	src = filepath.Clean(src)

	type dirMode struct {
		path string
		mode os.FileMode
	}
	var dirs []dirMode

	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat source: %w", err)
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			return CopySymlink(path, target)
		case d.IsDir():
			// Force owner access while copying, then restore the real mode
			// afterwards: a source directory that denies the owner write or
			// search would otherwise block copying its own contents.
			if err := os.MkdirAll(target, info.Mode().Perm()|0o700); err != nil {
				return fmt.Errorf("create directory: %w", err)
			}
			dirs = append(dirs, dirMode{target, info.Mode().Perm()})
			return nil
		default:
			return CopyFile(path, target)
		}
	})
	if err != nil {
		return err
	}

	// Deepest first, so tightening a parent cannot lock out its children.
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chmod(dirs[i].path, dirs[i].mode); err != nil {
			return fmt.Errorf("set directory mode: %w", err)
		}
	}
	return nil
}
