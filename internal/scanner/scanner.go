// Package scanner matches local files and packages against registry modules.
package scanner

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/atomikpanda/dotular/internal/config"
	"github.com/atomikpanda/dotular/internal/platform"
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

// ScanResult holds the match results for a single registry module.
type ScanResult struct {
	Module       registry.RemoteModule
	MatchedItems []MatchedItem
	TotalItems   int
	Score        float64 // matched / total, 0 if total is 0
}

// MatchedItem describes a single matched item within a module.
type MatchedItem struct {
	Item        config.Item
	Description string // e.g., "package installed", "config exists"
}

// FileExistsFunc checks if a path exists on the filesystem.
type FileExistsFunc func(path string) bool

// PkgInstalledFunc checks if a package is installed via a given manager.
type PkgInstalledFunc func(manager, pkg string) bool

// ScanInstalled checks which items from each registry module are present on
// the local machine. Items for package managers or destinations not applicable
// to the current OS are excluded from the total count.
func ScanInstalled(
	modules []registry.RemoteModule,
	goos string,
	expand ExpandFunc,
	fileExists FileExistsFunc,
	pkgInstalled PkgInstalledFunc,
) []ScanResult {
	var results []ScanResult

	for _, mod := range modules {
		var matched []MatchedItem
		total := 0

		for _, item := range mod.Items {
			switch {
			case item.Package != "":
				mgOS := platform.PackageManagerOS(item.Via)
				if mgOS != "" && mgOS != goos {
					continue
				}
				total++
				if pkgInstalled(item.Via, item.Package) {
					matched = append(matched, MatchedItem{
						Item:        item,
						Description: "package installed",
					})
				}

			case item.File != "":
				dest := item.Destination.ForOS(goos)
				if dest == "" {
					continue
				}
				total++
				target := resolveFileTarget(dest, item.File, expand)
				if fileExists(target) {
					matched = append(matched, MatchedItem{
						Item:        item,
						Description: "config exists",
					})
				}

			case item.Directory != "":
				dest := item.Destination.ForOS(goos)
				if dest == "" {
					continue
				}
				total++
				if fileExists(expand(dest)) {
					matched = append(matched, MatchedItem{
						Item:        item,
						Description: "config exists",
					})
				}

			default:
				continue
			}
		}

		score := 0.0
		if total > 0 {
			score = float64(len(matched)) / float64(total)
		}

		results = append(results, ScanResult{
			Module:       mod,
			MatchedItems: matched,
			TotalItems:   total,
			Score:        score,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Module.Name < results[j].Module.Name
	})

	return results
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
