package actions

import (
	"os"
	"path/filepath"
	"strings"
)

// Destination resolution lives here rather than on the action types because the
// scanner must answer "what path would the runner manage?" without building an
// action. Both the actions and the scanner call these functions so the two can
// never disagree; the expand and isDir hooks exist only so the scanner can stay
// injectable for tests.

// OSIsDir reports whether path is an existing directory on the real filesystem.
func OSIsDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// ResolveFileTarget returns the fully expanded destination path a file item
// writes to. srcBase is the repo-side file's basename.
//
// An existing directory always takes the basename appended, since a file cannot
// be written over a directory. Otherwise a destination whose basename has an
// extension (e.g. ~/.wezterm.lua) is a complete file path. A trailing "/" on the
// raw destination always forces directory treatment — it is the author's
// explicit signal and expansion/cleaning erases it, so it must be read from the
// unexpanded value.
func ResolveFileTarget(dest, srcBase string, expand func(string) string, isDir func(string) bool) string {
	expanded := filepath.Clean(expand(dest))
	if isDir(expanded) {
		return filepath.Join(expanded, srcBase)
	}
	if !strings.HasSuffix(dest, "/") && filepath.Ext(filepath.Base(expanded)) != "" {
		return expanded
	}
	return filepath.Join(expanded, srcBase)
}

// ResolveDirectoryTarget returns the fully expanded destination path a directory
// item writes to. srcBase is the repo-side directory's basename. The destination
// is the complete path only when it already ends with srcBase; otherwise it is
// the parent and srcBase is appended.
func ResolveDirectoryTarget(dest, srcBase string, expand func(string) string) string {
	expanded := filepath.Clean(expand(dest))
	if filepath.Base(expanded) == srcBase {
		return expanded
	}
	return filepath.Join(expanded, srcBase)
}
