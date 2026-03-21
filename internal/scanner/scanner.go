// Package scanner matches local files and packages against registry modules.
package scanner

import (
	"path/filepath"
	"strings"

	"github.com/atomikpanda/dotular/internal/registry"
)

// MatchResult describes a registry module whose destination matches a local path.
type MatchResult struct {
	ModuleName string
	ItemFile   string // the matched item's file/directory field
}

// ExpandFunc expands ~ and env vars in a path.
type ExpandFunc func(string) string

// resolveFileTarget returns the fully expanded target path for a file item.
// Mirrors FileAction.ResolvedTarget() semantics: if the destination has a
// file extension (and no trailing "/"), it is treated as a complete file path.
// Otherwise the item's filename is appended.
func resolveFileTarget(dest, fileName string, expand ExpandFunc) string {
	expanded := filepath.Clean(expand(dest))
	base := filepath.Base(expanded)
	if !strings.HasSuffix(dest, "/") && filepath.Ext(base) != "" {
		return expanded
	}
	return filepath.Join(expanded, fileName)
}

// MatchPath checks if a filesystem path matches any registry module's
// file/directory destination for the given OS. Returns all matches.
// Supports both exact match and prefix match (path is under a destination dir).
func MatchPath(path string, modules []registry.RemoteModule, goos string, expand ExpandFunc) []MatchResult {
	var results []MatchResult
	absPath := filepath.Clean(path)

	for _, mod := range modules {
		for _, item := range mod.Items {
			if item.File == "" && item.Directory == "" {
				continue
			}
			dest := item.Destination.ForOS(goos)
			if dest == "" {
				continue
			}

			if item.File != "" {
				target := resolveFileTarget(dest, item.File, expand)
				if absPath == target {
					results = append(results, MatchResult{
						ModuleName: mod.Name,
						ItemFile:   item.File,
					})
				}
				continue
			}

			// For directory items, match if the path equals or is under
			// the destination directory.
			if item.Directory != "" {
				expandedDest := filepath.Clean(expand(dest))
				if absPath == expandedDest || strings.HasPrefix(absPath, expandedDest+string(filepath.Separator)) {
					results = append(results, MatchResult{
						ModuleName: mod.Name,
						ItemFile:   item.Directory,
					})
				}
			}
		}
	}
	return results
}
