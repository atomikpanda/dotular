// Package fsutil holds the file and directory copies used everywhere dotular
// writes to disk. Symlinks are always recreated as symlinks rather than
// dereferenced, but permission bits come in two flavours, because reproducing
// the source's mode is right in some directions and wrong in others:
//
//   - CopyFile / CopyDir reproduce the source's modes exactly. Use them where
//     the source's mode IS the intended mode: snapshot capture and restore
//     (rollback fidelity depends on it) and reading the system into the repo.
//   - CopyContents / CopyDirContents leave the destination's own modes alone and
//     use the platform defaults for anything they create. Use them when writing
//     the repo out to the system: git records only the exec bit, so a repo file's
//     0644 is an artifact of checkout, not a statement of intent, and propagating
//     it would silently widen a 0600 destination. The `permissions:` field is the
//     explicit control for destination modes.
package fsutil

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Modes used for paths these helpers create when the source's modes are not
// being propagated. They match what os.Create and os.MkdirAll have always
// produced here, and are narrowed further by the process umask.
const (
	defaultFileMode os.FileMode = 0o644
	defaultDirMode  os.FileMode = 0o755
)

// CopyFile copies the contents of src to dst, reproducing src's permission bits
// on dst. An existing dst is truncated and re-moded.
func CopyFile(src, dst string) error {
	return copyFile(src, dst, true)
}

// CopyContents copies the contents of src to dst without touching dst's mode: an
// existing dst keeps the permissions it already has, and a dst that has to be
// created gets defaultFileMode.
func CopyContents(src, dst string) error {
	return copyFile(src, dst, false)
}

func copyFile(src, dst string, preserveMode bool) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}

	mode := defaultFileMode
	if preserveMode {
		mode = info.Mode().Perm()
	}

	// OpenFile follows a symlink at dst, truncating its target instead of
	// replacing the entry — which corrupts a file outside the managed tree, and
	// rollback cannot recover it because the snapshot records the link rather
	// than what it points at. Replace the entry, as CopySymlink already does.
	if dstInfo, err := os.Lstat(dst); err == nil && dstInfo.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(dst); err != nil {
			return fmt.Errorf("remove destination symlink: %w", err)
		}
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy contents: %w", err)
	}
	if preserveMode {
		// OpenFile's mode applies only when it creates dst, and is masked by the
		// umask even then, so set the mode explicitly.
		if err := out.Chmod(mode); err != nil {
			return fmt.Errorf("set destination mode: %w", err)
		}
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

// CopyDir recursively copies the src tree into dst, creating dst if needed,
// reproducing every file and directory mode from the source.
func CopyDir(src, dst string) error {
	return copyDir(src, dst, true)
}

// CopyDirContents recursively copies the src tree into dst without propagating
// the source's modes: existing destinations keep their own permissions, and
// anything created gets the platform defaults.
func CopyDirContents(src, dst string) error {
	return copyDir(src, dst, false)
}

func copyDir(src, dst string, preserveModes bool) error {
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
			if !preserveModes {
				// MkdirAll leaves an existing directory's mode alone, which is
				// exactly what we want here.
				if err := os.MkdirAll(target, defaultDirMode); err != nil {
					return fmt.Errorf("create directory: %w", err)
				}
				return nil
			}
			// Force owner access while copying, then set the real mode
			// afterwards: a source directory that denies the owner write or
			// search would otherwise block copying its own contents.
			if err := os.MkdirAll(target, info.Mode().Perm()|0o700); err != nil {
				return fmt.Errorf("create directory: %w", err)
			}
			dirs = append(dirs, dirMode{target, info.Mode().Perm()})
			return nil
		default:
			return copyFile(path, target, preserveModes)
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
