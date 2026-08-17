// Package snapshot captures file state before a module apply so it can be
// restored atomically on failure.
package snapshot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/atomikpanda/dotular/internal/fsutil"
)

type savedRecord struct {
	destination  string
	snapshotPath string
}

// Snapshot holds ordered copies of paths that existed before an apply started,
// plus the highest roots created by writes so rollback can remove them.
type Snapshot struct {
	dir           string
	saved         []savedRecord
	restoreTarget map[string]string
	recordedPaths []string
	created       []string
}

var (
	restoreSavedPath   = restoreSaved
	removeCreatedPath  = os.RemoveAll
	discardSnapshotDir = os.RemoveAll
)

// New creates an empty Snapshot backed by a temporary directory.
func New() (*Snapshot, error) {
	dir, err := os.MkdirTemp("", "dotular-snap-*")
	if err != nil {
		return nil, fmt.Errorf("create snapshot dir: %w", err)
	}
	return &Snapshot{dir: dir, restoreTarget: make(map[string]string)}, nil
}

// Record saves the current state of path so it can be restored later.
// If path does not exist, Record owns the highest missing ancestor immediately
// below its closest existing parent. Calling Record twice for the same exact
// path is a no-op after the first successful call.
func (s *Snapshot) Record(path string) error {
	if _, alreadyRecorded := s.restoreTarget[path]; alreadyRecorded {
		return nil
	}
	for _, ownedRoot := range s.created {
		if pathWithin(ownedRoot, path) {
			s.restoreTarget[path] = ownedRoot
			s.recordedPaths = append(s.recordedPaths, path)
			return nil
		}
	}

	// Lstat, not Stat: a symlink is an existing boundary and must be saved as a
	// symlink rather than traversed while deciding what rollback owns.
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		createdRoot, err := highestMissingAncestor(path)
		if err != nil {
			return fmt.Errorf("snapshot %s: %w", path, err)
		}
		s.restoreTarget[path] = createdRoot
		s.recordedPaths = append(s.recordedPaths, path)
		s.created = append(s.created, createdRoot)
		return nil
	}
	if err != nil {
		return fmt.Errorf("snapshot %s: %w", path, err)
	}

	tmpPath := filepath.Join(s.dir, strconv.Itoa(len(s.saved)))
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		err = fsutil.CopySymlink(path, tmpPath)
	case info.IsDir():
		err = fsutil.CopyDir(path, tmpPath)
	default:
		err = fsutil.CopyFile(path, tmpPath)
	}
	if err != nil {
		return fmt.Errorf("snapshot %s: %w", path, err)
	}
	s.saved = append(s.saved, savedRecord{destination: path, snapshotPath: tmpPath})
	s.restoreTarget[path] = path
	s.recordedPaths = append(s.recordedPaths, path)
	return nil
}

func highestMissingAncestor(path string) (string, error) {
	missing := path
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		_, err := os.Lstat(parent)
		if err == nil {
			return missing, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		missing = parent
		if filepath.Dir(parent) == parent {
			return "", err
		}
	}
}

func pathWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil &&
		relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// RestoreResult reports the restore outcome for one path passed to Record.
type RestoreResult struct {
	Path string
	Err  error
}

// Restore writes saved paths back in reverse capture order, then removes owned
// created roots in reverse declaration order. Every failure remains observable.
func (s *Snapshot) Restore() error {
	results := s.RestoreResults()
	errs := make([]error, 0, len(results))
	for _, result := range results {
		errs = append(errs, result.Err)
	}
	return errors.Join(errs...)
}

// RestoreResults restores the snapshot and reports each recorded path separately.
func (s *Snapshot) RestoreResults() []RestoreResult {
	outcomes := make(map[string]error, len(s.saved)+len(s.created))
	for i := len(s.saved) - 1; i >= 0; i-- {
		record := s.saved[i]
		if err := restoreSavedPath(record); err != nil {
			outcomes[record.destination] = fmt.Errorf("restore %s: %w", record.destination, err)
		} else {
			outcomes[record.destination] = nil
		}
	}
	for i := len(s.created) - 1; i >= 0; i-- {
		path := s.created[i]
		if err := removeCreatedPath(path); err != nil {
			outcomes[path] = fmt.Errorf("remove created path %s: %w", path, err)
		} else {
			outcomes[path] = nil
		}
	}

	results := make([]RestoreResult, 0, len(s.recordedPaths))
	for _, path := range s.recordedPaths {
		results = append(results, RestoreResult{Path: path, Err: outcomes[s.restoreTarget[path]]})
	}
	return results
}

func restoreSaved(record savedRecord) error {
	// Lstat: a saved symlink must be identified as one rather than followed.
	info, err := os.Lstat(record.snapshotPath)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return fsutil.CopySymlink(record.snapshotPath, record.destination)
	case info.IsDir():
		removeErr := os.RemoveAll(record.destination)
		copyErr := fsutil.CopyDir(record.snapshotPath, record.destination)
		return errors.Join(removeErr, copyErr)
	default:
		return restoreFile(record.snapshotPath, record.destination)
	}
}

// restoreFile writes a saved regular file back to dest. A non-regular
// replacement is removed first: writing through a symlink would overwrite its
// target, while copying over a directory cannot recreate the captured file.
func restoreFile(tmp, dest string) error {
	info, err := os.Lstat(dest)
	switch {
	case err == nil && !info.Mode().IsRegular():
		if err := os.RemoveAll(dest); err != nil {
			return fmt.Errorf("remove non-regular destination: %w", err)
		}
	case err != nil && !os.IsNotExist(err):
		return fmt.Errorf("inspect destination: %w", err)
	}
	return fsutil.CopyFile(tmp, dest)
}

// Discard removes the temporary snapshot directory.
func (s *Snapshot) Discard() error {
	return discardSnapshotDir(s.dir)
}
