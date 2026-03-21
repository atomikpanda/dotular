# Design: `dotular init` & `dotular add` improvements

## Summary

Two related features that share scanning infrastructure:

1. **`dotular init`** — Registry-driven machine scanner that discovers which official modules match the user's installed software and config files, presents an interactive picker, and generates a `dotular.yaml` with `from:` references.
2. **`dotular add` arg reorder** — Change from `dotular add <module> <path>` to `dotular add <path> [module]`, making the module name optional with registry-based inference and interactive prompting as fallback.

## Feature 1: `dotular init`

### Flow

1. Fetch `modules/index.yaml` from the official registry to get the list of all available modules.
2. Fetch each module's full YAML definition.
3. For each module, check which items are present on the local machine:
   - **File/directory items**: Expand `Destination.ForOS()` for the current OS via `platform.ExpandPath()`, check if the path exists on disk.
   - **Package items**: Query the relevant package manager to determine if the package is installed. The scanner package constructs `actions.PackageAction` instances and calls `IsApplied()` directly. The `checkArgs` function in `internal/actions/package.go` must be exported (renamed to `CheckArgs`) to support this.
4. Score each module by the number of matched items.
5. Present an interactive terminal picker (using `charmbracelet/huh`) showing matched modules sorted by match strength. Modules with all items matched are pre-selected; partial matches are unselected by default.
6. On confirmation, generate `dotular.yaml` with `from:` entries for selected modules.
7. If `dotular.yaml` already exists, merge new selections. Duplicate detection is by `from:` reference value — if a module with the same `from:` already exists, skip it. Inline modules (no `from:`) are never considered duplicates. The existing `age:` block and all other modules are preserved. Note: `config.Save()` does a full marshal, so YAML comments will be lost. This is an accepted limitation.
8. Print next steps: tell the user to run `dotular apply` to sync.

### Picker UX

```
$ dotular init

Scanning installed software...

Found 3 matching modules:

  [x] wezterm (2/2 items matched: package installed, config exists)
  [ ] neovim  (1/2 items matched: config exists, package not found)
  [ ] git     (1/3 items matched: package installed)

Use arrow keys to navigate, space to toggle, enter to confirm.
```

Modules with all items matched are pre-selected. Partial matches are shown but unselected. Sorted by score descending, then alphabetical as tiebreaker.

### Error handling

- **Empty registry / no modules in index**: Print a message ("No modules found in registry") and exit cleanly.
- **Network failure with no cache**: Print an error suggesting `--no-cache` or checking connectivity. Exit with non-zero status.
- **Partial fetch failure**: Skip modules that fail to fetch, warn the user, continue scanning with the rest.
- **No matches found**: Print "No matching modules found on this machine" and exit cleanly.

### Non-interactive mode

When stdin is not a terminal (piped, CI), skip the picker and auto-select all modules with a full match (score = 1.0). Print what was selected to stdout. This keeps `init` usable in scripts.

### No automatic apply

After generating the config, the user reviews it and decides when to run `dotular apply`. No side effects beyond writing/updating `dotular.yaml`.

## Feature 2: `dotular add` arg reorder

### Current syntax

```
dotular add <module> <path>
```

### New syntax

```
dotular add <path> [module]
```

Path is always the first argument (required). Module name is the optional second argument.

### When module name is omitted

1. Check registry modules — if the given path matches a known module's destination for the current OS, suggest that module name.
2. If no registry match, prompt the user to type a module name (plain text input, no autocomplete).
3. If stdin is not a terminal, error out with a message telling the user to provide the module name explicitly.

### When module name is provided

Behavior is unchanged from current implementation (just arg order reversed).

### Breaking change

This reverses the argument order. No backwards compatibility shim — the project is early enough to make this change cleanly.

## Shared infrastructure: `internal/scanner/`

Both `init` and `add` need to match local paths against registry modules. A new `internal/scanner/` package provides the matching and scoring logic. It depends on `internal/registry` for fetching and `internal/actions` for package checking.

### `FetchIndex(ctx) → []IndexEntry`

Fetches `modules/index.yaml` from the official registry at `https://raw.githubusercontent.com/atomikpanda/dotular/main/modules/index.yaml`. Returns a list of module names and versions. Uses `internal/registry` download/cache infrastructure — does not duplicate HTTP or caching logic.

### `FetchAllModules(ctx, index) → []registry.RemoteModule`

Fetches the full YAML for each module in the index using existing `registry.Fetch()` with bare-name references. Returns successfully fetched modules; logs warnings for fetch failures.

### `MatchPath(path string, modules []registry.RemoteModule, goos string) → []MatchResult`

Checks if a given filesystem path matches any registry module's file/directory destination for the specified OS. Returns all matches (not just the first) since multiple modules could reference the same path.

```go
type MatchResult struct {
    ModuleName string
    ItemFile   string // the matched item's file/directory field
}
```

**Matching semantics**: Paths are compared after expansion via `platform.ExpandPath()`. Both exact match and prefix match are supported — if the user's path is under a module's destination directory, that counts as a match. For example, `~/.config/nvim/init.lua` matches a module with destination `~/.config/nvim`.

### `ScanInstalled(ctx, modules []registry.RemoteModule, goos string) → []ScanResult`

For each registry module, checks which items are present on the machine:

```go
type ScanResult struct {
    Module       registry.RemoteModule
    MatchedItems []MatchedItem
    TotalItems   int
    Score        float64 // matched / total
}

type MatchedItem struct {
    Item        config.Item
    Description string // e.g., "package installed", "config exists"
}
```

**File/directory items**: Expands `Destination.ForOS(goos)` via `platform.ExpandPath()`, checks if the path exists on disk via `os.Stat()`. Items with no destination for the current OS are excluded from the total count.

**Package items**: Constructs `actions.PackageAction` and calls `IsApplied()`. Items for package managers not available on the current OS are excluded from the total count.

**Testability**: The scanner accepts interfaces for filesystem checking and package querying so unit tests can use test doubles without shelling out to real package managers.

## Registry module index: `modules/index.yaml`

### Format

```yaml
modules:
  - name: wezterm
    version: "1.0.0"
  - name: neovim
    version: "1.0.0"
```

Lightweight — just names and versions. Full module YAML fetched individually.

### Fetch URL

`https://raw.githubusercontent.com/atomikpanda/dotular/main/modules/index.yaml`

### Generation

A GitHub Action runs on push to `main` when `modules/*.yaml` changes. It globs the `modules/` directory (excluding `index.yaml` itself) and writes/updates `modules/index.yaml` by reading each module's `name` and `version` fields.

## New dependency

- `github.com/charmbracelet/huh` — terminal UI forms library for the interactive picker.

## Testing strategy

- **`internal/scanner/`**: Unit tests with mock registry data. Filesystem and package checks use interfaces so tests inject test doubles. Test path matching across OS variants (darwin, linux, windows). Test scoring with various match combinations.
- **`init` command**: Integration test that provides a mock registry and verifies generated YAML output. Test merge behavior with existing config.
- **`add` command**: Update existing tests for new arg order. Add test for module inference flow.
- **CI**: Existing 80% coverage requirement applies to new code.

## Out of scope

- Scanning for files/packages not in the registry (use `dotular add` for those).
- Automatic `dotular apply` after init.
- Non-official registry module discovery.
- Improving `mas list` performance (known limitation: runs full list per check).
