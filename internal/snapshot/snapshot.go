// Package snapshot captures file state before a module apply so it can be
// restored atomically on failure.
package snapshot

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

// Snapshot holds copies of files that existed before an apply started, plus a
// list of paths that were newly created so they can be removed on rollback.
type Snapshot struct {
	dir     string
	saved   map[string]string // destination path → copy inside dir
	created []string          // paths that did not exist before; delete on rollback
}

// New creates an empty Snapshot backed by a temporary directory.
func New() (*Snapshot, error) {
	dir, err := os.MkdirTemp("", "dotular-snap-*")
	if err != nil {
		return nil, fmt.Errorf("create snapshot dir: %w", err)
	}
	return &Snapshot{dir: dir, saved: make(map[string]string)}, nil
}

// Record saves the current state of path so it can be restored later.
// If path does not exist, it is added to the created list (deleted on rollback).
// Calling Record twice for the same path is a no-op after the first call.
func (s *Snapshot) Record(path string) error {
	if _, alreadyRecorded := s.saved[path]; alreadyRecorded {
		return nil
	}
	for _, p := range s.created {
		if p == path {
			return nil
		}
	}

	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		s.created = append(s.created, path)
		return nil
	}
	if err != nil {
		return fmt.Errorf("snapshot %s: %w", path, err)
	}

	tmpPath := filepath.Join(s.dir, strconv.Itoa(len(s.saved)))
	if info.IsDir() {
		if err := copyDir(path, tmpPath); err != nil {
			return fmt.Errorf("snapshot %s: %w", path, err)
		}
	} else {
		if err := copyFile(path, tmpPath); err != nil {
			return fmt.Errorf("snapshot %s: %w", path, err)
		}
	}
	s.saved[path] = tmpPath
	return nil
}

// Restore writes all saved files back to their original paths and removes any
// newly created files. It continues past individual errors, returning the first.
func (s *Snapshot) Restore() error {
	var first error
	for dest, tmp := range s.saved {
		info, err := os.Stat(tmp)
		if err != nil {
			if first == nil {
				first = fmt.Errorf("restore %s: %w", dest, err)
			}
			continue
		}
		if info.IsDir() {
			os.RemoveAll(dest)
			err = copyDir(tmp, dest)
		} else {
			err = copyFile(tmp, dest)
		}
		if err != nil && first == nil {
			first = fmt.Errorf("restore %s: %w", dest, err)
		}
	}
	for _, path := range s.created {
		os.RemoveAll(path) // best-effort; handles both files and directories
	}
	return first
}

// Discard removes the temporary snapshot directory.
func (s *Snapshot) Discard() error {
	return os.RemoveAll(s.dir)
}

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
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
