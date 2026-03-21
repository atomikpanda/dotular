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
   - **File/directory items**: Expand `Destination.ForOS()` for the current OS, check if the path exists on disk.
   - **Package items**: Query the relevant package manager to determine if the package is installed (reuse `PackageAction.IsApplied()` logic).
4. Score each module by the number of matched items.
5. Present an interactive terminal picker (using `charmbracelet/huh`) showing matched modules sorted by match strength. Modules with all items matched are pre-selected; partial matches are unselected.
6. On confirmation, generate `dotular.yaml` with `from:` entries for selected modules.
7. If `dotular.yaml` already exists, append new selections and skip duplicates. Warn about additions.
8. Print next steps: tell the user to run `dotular apply` to sync.

### Picker UX

```
$ dotular init

Scanning installed software...

Found 3 matching modules:

  [x] wezterm (2/2 items matched: package installed, config exists)
  [x] neovim  (1/2 items matched: config exists, package not found)
  [ ] git     (1/3 items matched: package installed)

Use arrow keys to navigate, space to toggle, enter to confirm.
```

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
2. If no registry match, prompt the user to type a module name.

### When module name is provided

Behavior is unchanged from current implementation (just arg order reversed).

### Breaking change

This reverses the argument order. No backwards compatibility shim — the project is early enough to make this change cleanly.

## Shared infrastructure: `internal/scanner/`

Both `init` and `add` need to match local paths against registry modules. A new `internal/scanner/` package provides:

### `FetchIndex(ctx) → []IndexEntry`

Fetches `modules/index.yaml` from the official registry. Returns a list of module names and versions.

### `FetchAllModules(ctx, index) → []registry.RemoteModule`

Fetches the full YAML for each module in the index.

### `MatchPath(path string, modules []registry.RemoteModule, goos string) → (moduleName string, found bool)`

Checks if a given filesystem path matches any registry module's file/directory destination for the specified OS.

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

File/directory items: checks if the expanded destination path exists.
Package items: queries the package manager (reuses `PackageAction.IsApplied()` logic).

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

### Generation

A GitHub Action runs on push to `main` when `modules/*.yaml` changes. It globs the directory and writes/updates `modules/index.yaml`.

## New dependency

- `github.com/charmbracelet/huh` — terminal UI forms library for the interactive picker.

## Testing strategy

- **`internal/scanner/`**: Unit tests with mock registry data and mock filesystem checks. Test path matching across OS variants.
- **`init` command**: Integration test that provides a mock registry and verifies generated YAML output.
- **`add` command**: Update existing tests for new arg order. Add test for module inference flow.
- **CI**: Existing 80% coverage requirement applies to new code.

## Out of scope

- Scanning for files/packages not in the registry (use `dotular add` for those).
- Automatic `dotular apply` after init.
- Non-official registry module discovery.
