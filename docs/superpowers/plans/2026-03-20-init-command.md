# `dotular init` & `dotular add` Improvements — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `dotular init` command that scans the machine against the module registry and suggests modules to adopt via an interactive picker, and reorder `dotular add` args to `<path> [module]` with optional module name inference.

**Architecture:** A new `internal/scanner/` package provides path matching and installed-software scanning, depending on `internal/registry` for fetching and `internal/actions` for package checking. The `init` command uses `charmbracelet/huh` for the interactive picker. The `add` command's arg order is reversed and module inference is added using the same scanner infrastructure.

**Tech Stack:** Go 1.22, Cobra, charmbracelet/huh, gopkg.in/yaml.v3

**Spec:** `docs/superpowers/specs/2026-03-20-init-command-design.md`

---

### Task 1: Add `charmbracelet/huh` dependency

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Add the dependency**

Run: `go get github.com/charmbracelet/huh@latest`

- [ ] **Step 2: Tidy modules**

Run: `go mod tidy`

- [ ] **Step 3: Verify build still passes**

Run: `go build ./...`
Expected: Clean build, no errors

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add charmbracelet/huh for interactive terminal forms"
```

---

### Task 2: Export `CheckArgs` in `internal/actions/package.go`

The scanner needs to check if packages are installed without constructing a full `PackageAction`. Export the `checkArgs` function.

**Files:**
- Modify: `internal/actions/package.go:65`
- Modify: `internal/actions/package_test.go` (add test for exported function)

- [ ] **Step 1: Write test for `CheckArgs`**

In `internal/actions/package_test.go`, add:

```go
func TestCheckArgs(t *testing.T) {
	tests := []struct {
		manager string
		pkg     string
		wantNil bool
		want0   string // first element of returned slice
	}{
		{"brew", "git", false, "brew"},
		{"brew-cask", "wezterm", false, "brew"},
		{"apt", "curl", false, "dpkg"},
		{"unknown-mgr", "foo", true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.manager+"/"+tt.pkg, func(t *testing.T) {
			got := CheckArgs(tt.manager, tt.pkg)
			if tt.wantNil {
				if got != nil {
					t.Errorf("CheckArgs(%q, %q) = %v, want nil", tt.manager, tt.pkg, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("CheckArgs(%q, %q) = nil, want non-nil", tt.manager, tt.pkg)
			}
			if got[0] != tt.want0 {
				t.Errorf("CheckArgs(%q, %q)[0] = %q, want %q", tt.manager, tt.pkg, got[0], tt.want0)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/actions/ -run TestCheckArgs -v`
Expected: FAIL — `CheckArgs` is not defined

- [ ] **Step 3: Rename `checkArgs` to `CheckArgs`**

In `internal/actions/package.go`, rename the function from `checkArgs` to `CheckArgs` (line 65). Update the call site in `IsApplied` (line 47) to use `CheckArgs`.

Change line 47:
```go
// old
args := checkArgs(a.Manager, a.Package)
// new
args := CheckArgs(a.Manager, a.Package)
```

Change line 65:
```go
// old
func checkArgs(manager, pkg string) []string {
// new
// CheckArgs returns the command to test whether a package is installed.
// Returns nil when no check is defined for the manager.
func CheckArgs(manager, pkg string) []string {
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/actions/ -run TestCheckArgs -v`
Expected: PASS

Run: `go test ./internal/actions/ -v`
Expected: All existing tests still pass

- [ ] **Step 5: Commit**

```bash
git add internal/actions/package.go internal/actions/package_test.go
git commit -m "refactor(actions): export CheckArgs for use by scanner package"
```

---

### Task 3: Create `modules/index.yaml`

Create the initial registry index file listing available modules.

**Files:**
- Create: `modules/index.yaml`

- [ ] **Step 1: Read existing module files**

Run: `ls modules/*.yaml` to see what modules exist.

- [ ] **Step 2: Create the index file**

Create `modules/index.yaml` by reading the `name` and `version` fields from each module YAML file in `modules/` (excluding `index.yaml` itself).

```yaml
modules:
  - name: wezterm
    version: "1.0.0"
```

- [ ] **Step 3: Commit**

```bash
git add modules/index.yaml
git commit -m "feat(registry): add modules/index.yaml registry index"
```

---

### Task 4: Add `FetchIndex` to `internal/registry/`

Add a function to fetch and parse the module index from the official registry.

**Files:**
- Modify: `internal/registry/fetch.go`
- Create: `internal/registry/index.go`
- Create: `internal/registry/index_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/registry/index_test.go`:

```go
package registry

import (
	"testing"
)

func TestParseIndex(t *testing.T) {
	data := []byte(`
modules:
  - name: wezterm
    version: "1.0.0"
  - name: neovim
    version: "2.0.0"
`)
	entries, err := ParseIndex(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Name != "wezterm" {
		t.Errorf("entries[0].Name = %q", entries[0].Name)
	}
	if entries[1].Version != "2.0.0" {
		t.Errorf("entries[1].Version = %q", entries[1].Version)
	}
}

func TestParseIndexEmpty(t *testing.T) {
	data := []byte(`modules: []`)
	entries, err := ParseIndex(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestParseIndexInvalid(t *testing.T) {
	_, err := ParseIndex([]byte("{{invalid"))
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestIndexURL(t *testing.T) {
	got := IndexURL()
	want := "https://raw.githubusercontent.com/atomikpanda/dotular/main/modules/index.yaml"
	if got != want {
		t.Errorf("IndexURL() = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/registry/ -run TestParseIndex -v`
Expected: FAIL — `ParseIndex` not defined

- [ ] **Step 3: Implement `internal/registry/index.go`**

```go
package registry

import (
	"context"
	"fmt"

	"github.com/atomikpanda/dotular/internal/ui"
	"gopkg.in/yaml.v3"
)

// IndexEntry represents a single module in the registry index.
type IndexEntry struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}

type indexFile struct {
	Modules []IndexEntry `yaml:"modules"`
}

// IndexURL returns the URL to the official registry index.
func IndexURL() string {
	return "https://raw.githubusercontent.com/" +
		"atomikpanda/dotular/main/modules/index.yaml"
}

// ParseIndex parses raw YAML bytes into a list of index entries.
func ParseIndex(data []byte) ([]IndexEntry, error) {
	var idx indexFile
	if err := yaml.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parse registry index: %w", err)
	}
	return idx.Modules, nil
}

// FetchIndex downloads and parses the official registry index.
// It uses the same download infrastructure as module fetching.
func FetchIndex(ctx context.Context, u *ui.UI) ([]IndexEntry, error) {
	data, err := download(ctx, IndexURL())
	if err != nil {
		return nil, fmt.Errorf("fetch registry index: %w", err)
	}
	return ParseIndex(data)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/registry/ -run "TestParseIndex|TestIndexURL" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/registry/index.go internal/registry/index_test.go
git commit -m "feat(registry): add FetchIndex to retrieve module index"
```

---

### Task 5: Create `internal/scanner/` — types and path matching

**Files:**
- Create: `internal/scanner/scanner.go`
- Create: `internal/scanner/scanner_test.go`

- [ ] **Step 1: Write failing tests for `resolveFileTarget` and `MatchPath`**

Create `internal/scanner/scanner_test.go`:

```go
package scanner

import (
	"testing"

	"github.com/atomikpanda/dotular/internal/config"
	"github.com/atomikpanda/dotular/internal/registry"
)

func TestResolveFileTarget(t *testing.T) {
	expand := func(p string) string {
		if len(p) > 1 && p[:2] == "~/" {
			return "/home/user" + p[1:]
		}
		return p
	}

	tests := []struct {
		name     string
		dest     string
		fileName string
		want     string
	}{
		{
			name:     "dest with extension is complete path",
			dest:     "~/.wezterm.lua",
			fileName: "wezterm.lua",
			want:     "/home/user/.wezterm.lua",
		},
		{
			name:     "dest without extension is directory",
			dest:     "~/",
			fileName: ".zshrc",
			want:     "/home/user/.zshrc",
		},
		{
			name:     "dest directory no trailing slash",
			dest:     "~/.config/nvim",
			fileName: "init.lua",
			want:     "/home/user/.config/nvim/init.lua",
		},
		{
			name:     "dest with trailing slash forces directory",
			dest:     "~/.wezterm.lua/",
			fileName: "wezterm.lua",
			want:     "/home/user/.wezterm.lua/wezterm.lua",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveFileTarget(tt.dest, tt.fileName, expand)
			if got != tt.want {
				t.Errorf("resolveFileTarget(%q, %q) = %q, want %q", tt.dest, tt.fileName, got, tt.want)
			}
		})
	}
}

func TestMatchPath(t *testing.T) {
	modules := []registry.RemoteModule{
		{
			Name: "wezterm",
			Items: []config.Item{
				{
					File:        "wezterm.lua",
					Destination: config.PlatformMap{MacOS: "~/.wezterm.lua", Linux: "~/.wezterm.lua"},
				},
			},
		},
		{
			Name: "nvim",
			Items: []config.Item{
				{
					Directory:   "nvim",
					Destination: config.PlatformMap{MacOS: "~/.config/nvim", Linux: "~/.config/nvim"},
				},
			},
		},
	}

	home := "/Users/testuser"
	expand := func(p string) string {
		if len(p) > 1 && p[:2] == "~/" {
			return home + p[1:]
		}
		return p
	}

	// Exact file match
	results := MatchPath(home+"/.wezterm.lua", modules, "darwin", expand)
	if len(results) != 1 {
		t.Fatalf("expected 1 match, got %d", len(results))
	}
	if results[0].ModuleName != "wezterm" {
		t.Errorf("ModuleName = %q", results[0].ModuleName)
	}

	// Prefix match (file under directory destination)
	results = MatchPath(home+"/.config/nvim/init.lua", modules, "darwin", expand)
	if len(results) != 1 {
		t.Fatalf("expected 1 match for prefix, got %d", len(results))
	}
	if results[0].ModuleName != "nvim" {
		t.Errorf("ModuleName = %q", results[0].ModuleName)
	}

	// No match
	results = MatchPath("/some/other/path", modules, "darwin", expand)
	if len(results) != 0 {
		t.Errorf("expected 0 matches, got %d", len(results))
	}

	// Wrong OS — wezterm has no windows destination
	results = MatchPath(home+"/.wezterm.lua", modules, "windows", expand)
	if len(results) != 0 {
		t.Errorf("expected 0 matches for wrong OS, got %d", len(results))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/scanner/ -run TestMatchPath -v`
Expected: FAIL — package doesn't exist

- [ ] **Step 3: Implement `internal/scanner/scanner.go`**

```go
// Package scanner matches local files and packages against registry modules.
package scanner

import (
	"path/filepath"
	"strings"

	"github.com/atomikpanda/dotular/internal/config"
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/scanner/ -run TestMatchPath -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/scanner/scanner.go internal/scanner/scanner_test.go
git commit -m "feat(scanner): add MatchPath for registry-based path matching"
```

---

### Task 6: Add `ScanInstalled` to `internal/scanner/`

**Files:**
- Modify: `internal/scanner/scanner.go`
- Modify: `internal/scanner/scanner_test.go`

- [ ] **Step 1: Write failing tests for `ScanInstalled`**

Add to `internal/scanner/scanner_test.go`:

```go
func TestScanInstalled(t *testing.T) {
	modules := []registry.RemoteModule{
		{
			Name: "wezterm",
			Items: []config.Item{
				{Package: "wezterm", Via: "brew-cask"},
				{
					File:        "wezterm.lua",
					Destination: config.PlatformMap{MacOS: "~/.wezterm.lua"},
				},
			},
		},
		{
			Name: "empty",
			Items: []config.Item{
				{Package: "nonexistent", Via: "brew"},
			},
		},
	}

	home := "/Users/testuser"
	expand := func(p string) string {
		if len(p) > 1 && p[:2] == "~/" {
			return home + p[1:]
		}
		return p
	}

	// Mock: wezterm package is installed, file exists
	fileExists := func(path string) bool {
		return path == home+"/.wezterm.lua"
	}
	pkgInstalled := func(manager, pkg string) bool {
		return manager == "brew-cask" && pkg == "wezterm"
	}

	results := ScanInstalled(modules, "darwin", expand, fileExists, pkgInstalled)

	// wezterm: 2/2 matched
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	var wez, empty ScanResult
	for _, r := range results {
		switch r.Module.Name {
		case "wezterm":
			wez = r
		case "empty":
			empty = r
		}
	}

	if wez.TotalItems != 2 {
		t.Errorf("wezterm TotalItems = %d, want 2", wez.TotalItems)
	}
	if len(wez.MatchedItems) != 2 {
		t.Errorf("wezterm matched = %d, want 2", len(wez.MatchedItems))
	}
	if wez.Score != 1.0 {
		t.Errorf("wezterm score = %f, want 1.0", wez.Score)
	}

	if empty.TotalItems != 1 {
		t.Errorf("empty TotalItems = %d, want 1", empty.TotalItems)
	}
	if len(empty.MatchedItems) != 0 {
		t.Errorf("empty matched = %d, want 0", len(empty.MatchedItems))
	}
}

func TestScanInstalledSkipsWrongOS(t *testing.T) {
	modules := []registry.RemoteModule{
		{
			Name: "macos-only",
			Items: []config.Item{
				{Package: "mas-app", Via: "mas"},
				{
					File:        ".rc",
					Destination: config.PlatformMap{MacOS: "~/"},
				},
			},
		},
	}

	expand := func(p string) string { return p }
	fileExists := func(string) bool { return false }
	pkgInstalled := func(string, string) bool { return false }

	// Scan as Linux — mas is darwin-only, file has no linux destination
	results := ScanInstalled(modules, "linux", expand, fileExists, pkgInstalled)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	// Both items should be excluded from total (wrong OS)
	if results[0].TotalItems != 0 {
		t.Errorf("TotalItems = %d, want 0 (all items excluded for wrong OS)", results[0].TotalItems)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/scanner/ -run TestScanInstalled -v`
Expected: FAIL — `ScanInstalled` not defined

- [ ] **Step 3: Implement `ScanInstalled`**

Add to `internal/scanner/scanner.go`:

```go
import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/atomikpanda/dotular/internal/config"
	"github.com/atomikpanda/dotular/internal/platform"
	"github.com/atomikpanda/dotular/internal/registry"
)

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
				// Check if the package manager is available on this OS.
				mgOS := platform.PackageManagerOS(item.Via)
				if mgOS != "" && mgOS != goos {
					continue // wrong OS, exclude from total
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
					continue // no destination for this OS
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
				// script, binary, run, setting — skip for scanning purposes
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

	// Sort by score descending, then alphabetically.
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Module.Name < results[j].Module.Name
	})

	return results
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/scanner/ -v`
Expected: All tests PASS

- [ ] **Step 5: Run full test suite**

Run: `go test ./...`
Expected: All tests pass

- [ ] **Step 6: Commit**

```bash
git add internal/scanner/scanner.go internal/scanner/scanner_test.go
git commit -m "feat(scanner): add ScanInstalled for registry-driven machine scanning"
```

---

### Task 7: Implement `dotular init` command

**Files:**
- Modify: `cmd/dotular/main.go`

- [ ] **Step 1: Add the `init` command to `buildRoot()`**

In `cmd/dotular/main.go`, add `initCmd()` to the `root.AddCommand(...)` call (line 56).

- [ ] **Step 2: Implement `initCmd()`**

Add the following function to `cmd/dotular/main.go`:

```go
func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Scan this machine and suggest modules from the registry",
		Long: `Scans your machine for installed packages and config files, matches
them against the official module registry, and lets you pick which
modules to add to your dotular.yaml.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			u := ui.New(os.Stdout, os.Stderr)

			// 1. Fetch the registry index.
			u.Info("Fetching module registry...")
			entries, err := registry.FetchIndex(ctx, u)
			if err != nil {
				return fmt.Errorf("fetch registry index: %w", err)
			}
			if len(entries) == 0 {
				u.Info("No modules found in registry.")
				return nil
			}

			// 2. Fetch all module definitions.
			lockPath := registry.LockPath(configFile)
			lock, err := registry.LoadLock(lockPath)
			if err != nil {
				return err
			}

			var modules []registry.RemoteModule
			for _, entry := range entries {
				mod, _, fetchErr := registry.Fetch(ctx, entry.Name, lock, noCache, u)
				if fetchErr != nil {
					u.Warn(fmt.Sprintf("skipping %s: %v", entry.Name, fetchErr))
					continue
				}
				modules = append(modules, *mod)
			}
			if len(modules) == 0 {
				u.Info("No modules could be fetched from registry.")
				return nil
			}

			// Save updated lock file.
			if err := registry.SaveLock(lockPath, lock); err != nil {
				u.Warn(fmt.Sprintf("could not save lock file: %v", err))
			}

			// 3. Scan the machine.
			u.Info("Scanning installed software...")
			expand := platform.ExpandPath
			fileExists := func(path string) bool {
				_, err := os.Stat(path)
				return err == nil
			}
			pkgInstalled := func(manager, pkg string) bool {
				args := actions.CheckArgs(manager, pkg)
				if args == nil {
					return false
				}
				cmd := exec.CommandContext(ctx, args[0], args[1:]...)
				return cmd.Run() == nil
			}

			results := scanner.ScanInstalled(modules, platform.Current(), expand, fileExists, pkgInstalled)

			// Filter to results that have at least one match.
			var matched []scanner.ScanResult
			for _, r := range results {
				if len(r.MatchedItems) > 0 {
					matched = append(matched, r)
				}
			}

			if len(matched) == 0 {
				u.Info("No matching modules found on this machine.")
				return nil
			}

			// 4. Interactive picker or auto-select.
			var selected []scanner.ScanResult
			if isTerminal() {
				selected, err = runPicker(matched)
				if err != nil {
					return err
				}
			} else {
				// Non-interactive: auto-select full matches.
				for _, r := range matched {
					if r.Score == 1.0 {
						selected = append(selected, r)
						u.Info(fmt.Sprintf("auto-selected: %s (%d/%d items matched)",
							r.Module.Name, len(r.MatchedItems), r.TotalItems))
					}
				}
			}

			if len(selected) == 0 {
				u.Info("No modules selected.")
				return nil
			}

			// 5. Load or create config, merge selections.
			cfg, loadErr := loadConfig()
			if loadErr != nil && !os.IsNotExist(loadErr) {
				return loadErr
			}

			// Normalize existing from: refs for dedup comparison.
			existingURLs := make(map[string]bool)
			for _, mod := range cfg.Modules {
				if mod.From != "" {
					ref := registry.ParseRef(mod.From)
					existingURLs[ref.FetchURL] = true
				}
			}

			added := 0
			for _, r := range selected {
				fromRef := r.Module.Name // bare name expands to official registry
				ref := registry.ParseRef(fromRef)
				if existingURLs[ref.FetchURL] {
					u.Warn(fmt.Sprintf("skipping %s (already in config)", fromRef))
					continue
				}
				cfg.Modules = append(cfg.Modules, config.Module{
					From: fromRef,
				})
				added++
			}

			if added == 0 {
				u.Info("All selected modules are already in your config.")
				return nil
			}

			// 6. Write config.
			if err := config.Save(configFile, cfg); err != nil {
				return err
			}

			u.Success(fmt.Sprintf("Added %d module(s) to %s", added, configFile))
			u.Info(fmt.Sprintf("\nNext: run %s to apply", color.Bold("dotular apply")))
			return nil
		},
	}
}
```

- [ ] **Step 3: Add `runPicker` and `isTerminal` helpers**

Add to `cmd/dotular/main.go`:

```go
import (
	"os/exec"

	"github.com/charmbracelet/huh"

	"github.com/atomikpanda/dotular/internal/actions"
	"github.com/atomikpanda/dotular/internal/scanner"
)

func isTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func runPicker(results []scanner.ScanResult) ([]scanner.ScanResult, error) {
	options := make([]huh.Option[int], len(results))
	for i, r := range results {
		label := fmt.Sprintf("%s (%d/%d items matched)",
			r.Module.Name, len(r.MatchedItems), r.TotalItems)
		options[i] = huh.NewOption(label, i)
	}

	var selectedIndices []int

	// Pre-select full matches.
	for i, r := range results {
		if r.Score == 1.0 {
			selectedIndices = append(selectedIndices, i)
		}
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[int]().
				Title("Select modules to add").
				Options(options...).
				Value(&selectedIndices),
		),
	)

	if err := form.Run(); err != nil {
		return nil, err
	}

	var selected []scanner.ScanResult
	for _, idx := range selectedIndices {
		selected = append(selected, results[idx])
	}
	return selected, nil
}
```

- [ ] **Step 4: Verify build**

Run: `go build ./cmd/dotular/`
Expected: Clean build

- [ ] **Step 5: Run full test suite**

Run: `go test ./...`
Expected: All tests pass

- [ ] **Step 6: Commit**

```bash
git add cmd/dotular/main.go
git commit -m "feat(cmd): add dotular init command with registry-driven machine scanning"
```

---

### Task 8: Reorder `dotular add` args and add module inference

**Files:**
- Modify: `cmd/dotular/main.go` (the `addCmd()` function, lines 102-223)

- [ ] **Step 1: Update `addCmd` — change Use string and arg handling**

Change the `addCmd` function:

```go
func addCmd() *cobra.Command {
	var link bool
	var direction string

	cmd := &cobra.Command{
		Use:   "add <path> [module]",
		Short: "Add a file or directory to a module",
		Long: `Adds a file or directory to a named module. If the module doesn't exist
it is created. Copies (or symlinks with --link) the path into the module's
managed store and records it in the config YAML.

If the module name is omitted, dotular checks the registry for a matching
module and suggests it. If no match is found, you are prompted to enter
a module name.`,
		Example: `  dotular add ~/.config/nvim nvim
  dotular add ~/.config/nvim/init.lua nvim --link
  dotular add ~/.zshrc --direction sync`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			srcPath := platform.ExpandPath(args[0])

			// Resolve the source to an absolute path.
			absSrc, err := filepath.Abs(srcPath)
			if err != nil {
				return fmt.Errorf("resolve path: %w", err)
			}

			info, err := os.Stat(absSrc)
			if err != nil {
				return fmt.Errorf("stat %q: %w", absSrc, err)
			}

			var moduleName string
			if len(args) >= 2 {
				moduleName = args[1]
			} else {
				// Try to infer from registry.
				moduleName, err = inferModuleName(cmd.Context(), absSrc)
				if err != nil {
					return err
				}
			}

			isDir := info.IsDir()
			baseName := filepath.Base(absSrc)

			// (rest of the function is the same as before, using moduleName)
			cfgDir := filepath.Dir(configFile)
			if !filepath.IsAbs(cfgDir) {
				cfgDir, _ = filepath.Abs(cfgDir)
			}
			moduleDir := filepath.Join(cfgDir, moduleName)

			if err := os.MkdirAll(moduleDir, 0o755); err != nil {
				return fmt.Errorf("create module directory: %w", err)
			}

			dest := filepath.Join(moduleDir, baseName)

			if isDir {
				if err := copyDirRecursive(absSrc, dest); err != nil {
					return fmt.Errorf("copy directory: %w", err)
				}
			} else {
				if err := copyFileSimple(absSrc, dest); err != nil {
					return fmt.Errorf("copy file: %w", err)
				}
			}

			srcParent := filepath.Dir(absSrc)
			pmap := config.PlatformMap{}
			switch platform.Current() {
			case "darwin":
				pmap.MacOS = srcParent
			case "windows":
				pmap.Windows = srcParent
			case "linux":
				pmap.Linux = srcParent
			}

			cfg, err := loadConfig()
			if err != nil && !os.IsNotExist(err) {
				return err
			}

			item := config.Item{
				Destination: pmap,
				Direction:   direction,
				Link:        link,
			}
			if isDir {
				item.Directory = baseName
			} else {
				item.File = baseName
			}

			mod := cfg.Module(moduleName)
			if mod == nil {
				cfg.Modules = append(cfg.Modules, config.Module{
					Name:  moduleName,
					Items: []config.Item{item},
				})
			} else {
				mod.Items = append(mod.Items, item)
			}

			if err := config.Save(configFile, cfg); err != nil {
				return err
			}

			typeStr := "file"
			if isDir {
				typeStr = "directory"
			}
			u := ui.New(os.Stdout, os.Stderr)
			u.Success(fmt.Sprintf("added %s %q to module %q", typeStr, baseName, moduleName))
			u.Info(fmt.Sprintf("  store: %s", dest))
			u.Info(fmt.Sprintf("  config: %s", configFile))
			return nil
		},
	}

	cmd.Flags().BoolVar(&link, "link", false, "use symlink instead of copy at apply time")
	cmd.Flags().StringVar(&direction, "direction", "push", "file direction: push, pull, or sync")
	return cmd
}
```

- [ ] **Step 2: Add `inferModuleName` helper**

Add to `cmd/dotular/main.go`:

```go
func inferModuleName(ctx context.Context, absPath string) (string, error) {
	u := ui.New(os.Stdout, os.Stderr)

	// Try registry-based inference.
	entries, err := registry.FetchIndex(ctx, u)
	if err == nil && len(entries) > 0 {
		lockPath := registry.LockPath(configFile)
		lock, lockErr := registry.LoadLock(lockPath)
		if lockErr == nil {
			var modules []registry.RemoteModule
			for _, entry := range entries {
				mod, _, fetchErr := registry.Fetch(ctx, entry.Name, lock, noCache, u)
				if fetchErr == nil {
					modules = append(modules, *mod)
				}
			}
			if len(modules) > 0 {
				matches := scanner.MatchPath(absPath, modules, platform.Current(), platform.ExpandPath)
				if len(matches) == 1 {
					u.Info(fmt.Sprintf("Matched registry module: %s", matches[0].ModuleName))
					return matches[0].ModuleName, nil
				}
				if len(matches) > 1 {
					u.Info("Multiple registry modules match this path:")
					for _, m := range matches {
						u.Info(fmt.Sprintf("  - %s", m.ModuleName))
					}
				}
			}
		}
	}

	// Prompt the user.
	if !isTerminal() {
		return "", fmt.Errorf("module name required when stdin is not a terminal; use: dotular add <path> <module>")
	}

	var name string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Module name").
				Description("Enter a name for the module").
				Value(&name),
		),
	)
	if err := form.Run(); err != nil {
		return "", err
	}
	if name == "" {
		return "", fmt.Errorf("module name cannot be empty")
	}
	return name, nil
}
```

- [ ] **Step 3: Update existing `add` command tests**

Read `cmd/dotular/main_test.go` and find all existing `add` tests. They use the old arg order `"add", "mymod", srcFile`. Swap to `"add", srcFile, "mymod"` (path first, module second). For example:

```go
// old:
root.SetArgs([]string{"add", "--config", cfgPath, "mymod", srcFile})
// new:
root.SetArgs([]string{"add", "--config", cfgPath, srcFile, "mymod"})
```

Update all instances throughout the test file.

- [ ] **Step 4: Verify build**

Run: `go build ./cmd/dotular/`
Expected: Clean build

- [ ] **Step 5: Run full test suite**

Run: `go test ./...`
Expected: All tests pass

- [ ] **Step 6: Commit**

```bash
git add cmd/dotular/main.go cmd/dotular/main_test.go
git commit -m "feat(cmd): reorder add args to <path> [module] with inference"
```

---

### Task 9: Add tests for `init` and updated `add` commands

**Files:**
- Modify: `cmd/dotular/main_test.go`

- [ ] **Step 1: Read existing main_test.go**

Read `cmd/dotular/main_test.go` to understand the current test patterns.

- [ ] **Step 2: Add tests for the `init` and `add` command changes**

Add tests that verify:
- `init` command exists and has correct `Use` string
- `add` command accepts 1 or 2 args (new `RangeArgs(1, 2)`)
- `add` command's `Use` string is updated

These are structural tests. Full integration tests with mock registries would require significant HTTP mocking infrastructure — keep it simple for now.

```go
func TestInitCmdExists(t *testing.T) {
	root := buildRoot()
	cmd, _, err := root.Find([]string{"init"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Use != "init" {
		t.Errorf("init command Use = %q", cmd.Use)
	}
}

func TestAddCmdAcceptsNewArgOrder(t *testing.T) {
	root := buildRoot()
	cmd, _, err := root.Find([]string{"add"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Use != "add <path> [module]" {
		t.Errorf("add command Use = %q, want %q", cmd.Use, "add <path> [module]")
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./cmd/dotular/ -v`
Expected: PASS

- [ ] **Step 4: Run full test suite with coverage**

Run: `go test -race -coverprofile=coverage.out -covermode=atomic ./...`
Expected: All tests pass, coverage meets 80% minimum

- [ ] **Step 5: Commit**

```bash
git add cmd/dotular/main_test.go
git commit -m "test(cmd): add tests for init command and add arg reorder"
```

---

### Task 10: Create GitHub Action for `modules/index.yaml` generation

**Files:**
- Create: `.github/workflows/update-index.yaml`

- [ ] **Step 1: Create the workflow file**

```yaml
name: Update module index

on:
  push:
    branches: [main]
    paths:
      - 'modules/*.yaml'
      - '!modules/index.yaml'

jobs:
  update-index:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Generate modules/index.yaml
        run: |
          echo "modules:" > modules/index.yaml
          for f in modules/*.yaml; do
            [ "$(basename "$f")" = "index.yaml" ] && continue
            name=$(grep '^name:' "$f" | head -1 | sed 's/name: *//;s/"//g')
            version=$(grep '^version:' "$f" | head -1 | sed 's/version: *//;s/"//g')
            echo "  - name: $name" >> modules/index.yaml
            echo "    version: \"$version\"" >> modules/index.yaml
          done

      - name: Check for changes
        id: changes
        run: |
          git diff --quiet modules/index.yaml || echo "changed=true" >> "$GITHUB_OUTPUT"

      - name: Commit and push
        if: steps.changes.outputs.changed == 'true'
        run: |
          git config user.name "github-actions[bot]"
          git config user.email "github-actions[bot]@users.noreply.github.com"
          git add modules/index.yaml
          git commit -m "chore: update modules/index.yaml [skip ci]"
          git push
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/update-index.yaml
git commit -m "ci: add GitHub Action to auto-generate modules/index.yaml"
```

---

### Task 11: Update CLAUDE.md with new commands

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Add `init` and update `add` documentation**

Add `dotular init` to the CLI commands section. Update the `add` command syntax to show the new arg order.

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: add init command and update add syntax in CLAUDE.md"
```
