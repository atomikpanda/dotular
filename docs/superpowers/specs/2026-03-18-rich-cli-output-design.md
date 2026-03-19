# Rich CLI Output Design

## Goal

Improve dotular CLI output with consistent formatting, color, light unicode, item timing, and result summaries across all commands.

## Approach

Create a new `internal/ui` package that centralizes all user-facing output formatting. Update all call sites in `cmd/dotular/main.go`, `internal/runner/runner.go`, and `internal/registry/` to use `ui.*` helpers instead of raw `fmt.Printf` + `color.*` calls.

## `internal/ui` Package API

### `UI` Struct

The `ui` package exposes a `UI` struct initialized with `io.Writer` targets:

```go
type UI struct {
    Out io.Writer // stdout (progress, results, tables)
    Err io.Writer // stderr (warnings, registry notices)
}

func New(out, err io.Writer) *UI
```

This preserves the existing `Runner.Out` abstraction. The runner constructs `ui.New(r.Out, os.Stderr)` and passes it through. Commands in `main.go` use `ui.New(os.Stdout, os.Stderr)`.

All methods respect `color.Enabled`. Unicode characters degrade to ASCII when color is disabled (no-color environments often have limited unicode support).

### Output Destinations

| Function | Writer | Rationale |
|----------|--------|-----------|
| `Header`, `SkipHeader`, `Item`, `ItemResult`, `Skip`, `DryRun` | `Out` | Core progress output |
| `Summary`, `ModuleSummary` | `Out` | Result totals |
| `Table` | `Out` | Structured data display |
| `Success`, `Info` | `Out` | Confirmations and info |
| `Warn` | `Err` | Warnings (suppressible via `2>/dev/null`) |

### Method Signatures

| Method | Output | Example |
|--------|--------|---------|
| `Header(name)` | Bold cyan section header | `==> nvim` |
| `SkipHeader(name, reason)` | Dim skipped section | `==> nvim  [skip: tag mismatch]` |
| `Item(desc)` | Action in progress | `  → description` |
| `ItemResult(desc, dur, err)` | Completed item | `  ✓ install "neovim" via brew (3.2s)` or `  ✗ ... (0.3s)` |
| `Skip(reason, desc)` | Skipped item | `  ─ skip [already applied] description` |
| `DryRun(desc)` | Dry-run preview | `  → [dry-run] description` |
| `Summary(applied, skipped, failed, elapsed)` | Final totals | `✓ 12 applied, 2 skipped, 0 failed (3.4s)` |
| `ModuleSummary(applied, skipped, failed)` | Per-module counts | `  3 applied, 1 skipped` |
| `Table(headers, rows, colColors)` | Aligned columns with `─` separator | See log/registry commands |
| `Warn(msg)` | Yellow warning to stderr | `⚠ warning message` |
| `Success(msg)` | Green confirmation | `✓ message` |
| `Info(msg)` | Plain informational | `message` |

### Unicode/ASCII Fallback

| Enabled | Arrow | Check | Cross | Dash | Warn | Bullet |
|---------|-------|-------|-------|------|------|--------|
| true | `→` | `✓` | `✗` | `─` | `⚠` | `·` |
| false | `->` | `[ok]` | `[FAIL]` | `-` | `[!]` | `-` |

### Duration Formatting

A helper `formatDuration(d time.Duration) string` with these rules:

| Range | Format | Example |
|-------|--------|---------|
| < 1s | milliseconds | `(42ms)` |
| 1s - 60s | one decimal second | `(3.2s)` |
| > 60s | minutes + seconds | `(2m 15s)` |

### Summary Color Logic

| Condition | Color | Icon |
|-----------|-------|------|
| `failed > 0` | Red | `✗` |
| `failed == 0 && applied > 0` | Green | `✓` |
| `failed == 0 && applied == 0` (all skipped) | Dim | `─` |

Same rules apply to `ModuleSummary`.

### Table Column Width

`Table()` does not attempt terminal width detection. It calculates column widths from the data (max width per column) with a minimum padding of 2 spaces between columns. Long values are not truncated. This matches the current behavior and avoids complexity.

## Runner Changes (`internal/runner/runner.go`)

### `ModuleResult` Type

```go
type ModuleResult struct {
    Applied int
    Skipped int
    Failed  int
    Err     error // first error encountered, if any
}
```

`ApplyModule` returns `ModuleResult` instead of `error`. `ApplyAll` accumulates results across modules.

### Error Handling in `ApplyAll`

Current behavior: `ApplyAll` returns on the first module error. New behavior:

- On module error, `ApplyAll` still stops (no change to fail-fast semantics)
- `Summary()` is printed in a `defer` so it always runs, even on early termination
- The summary reflects actual counts at the point of termination

### Counters and Timing

- Per-module counters: `applied`, `skipped`, `failed` (local variables in `ApplyModule`, returned via `ModuleResult`)
- Global counters in `ApplyAll` accumulated from `ModuleResult` values
- `time.Now()` / `time.Since()` per item and per `ApplyAll` invocation

### UI Integration

The `Runner` struct gets a `UI *ui.UI` field, constructed from `r.Out` in the runner's constructor. All `fmt.Fprintf(r.Out, ...)` calls are replaced with `r.UI.*()` calls.

### Apply Output Mapping

| Current | New |
|---------|-----|
| `fmt.Fprintf(r.Out, "==> %s\n", BoldCyan(name))` | `r.UI.Header(name)` |
| `fmt.Fprintf(r.Out, "  -> %s\n", Dim(desc))` | `r.UI.ItemResult(desc, dur, err)` after item completes |
| Dim skip messages | `r.UI.Skip(reason, desc)` |
| Dry-run: runner calls `ui.DryRun(action.Describe())` | See "Dry-Run Output" section below |
| `BoldYellow` rollback messages | `r.UI.Warn("restoring snapshot...")` |
| Verbose hook messages | `r.UI.Info()` or `r.UI.DryRun()` |
| (none) | `r.UI.ModuleSummary()` after each module |
| (none) | `r.UI.Summary()` after all modules (via defer) |

### Verify Output Mapping

| Current | New |
|---------|-----|
| `fmt.Fprintf(r.Out, "==> %s\n", BoldCyan(name))` | `r.UI.Header(name)` |
| `fmt.Fprintf(r.Out, "  %s  %s\n", BoldRed("FAIL"), desc)` | `r.UI.ItemResult(desc, dur, errFromVerify)` |
| `fmt.Fprintf(r.Out, "  %s  %s\n", BoldGreen("PASS"), desc)` | `r.UI.ItemResult(desc, dur, nil)` |
| Dim skip messages in verify | `r.UI.Skip(reason, desc)` |

## Dry-Run Output (Decision: Move to Runner)

**Decision: Option 1 — move dry-run output to the runner.**

Currently each action's `Run(ctx, true)` prints its own `[dry-run]` line via `fmt.Printf` (hardcoded `os.Stdout`, bypassing `r.Out`). This is an existing inconsistency — the runner uses `r.Out` but actions write directly to `os.Stdout`.

New behavior:
- In dry-run mode, the runner calls `r.UI.DryRun(action.Describe())` and **does not** call `action.Run()` at all
- The `dryRun bool` parameter remains on the `Action` interface for now (removing it would be a larger interface change out of scope for this spec), but actions' dry-run code paths become dead code that can be removed in a follow-up
- `Describe()` already provides sufficient detail for all action types

This centralizes all output through the `UI` struct and eliminates the `os.Stdout` bypass.

## Command Changes (`cmd/dotular/main.go`)

Commands construct `u := ui.New(os.Stdout, os.Stderr)` and use it throughout.

| Command | Change |
|---------|--------|
| `add` | `u.Success("added file ...")` + indented `u.Info()` lines for store/config paths |
| `list` | Bold module name, dim count, item type breakdown: `nvim  3 items (2 files, 1 package)` |
| `status` | Automatic via runner changes (dryRun=true) |
| `platform` | `u.Info("os: darwin")` |
| `verify` | Failure: `u.Warn("some verify checks failed")` |
| `encrypt`/`decrypt` | `u.Info("encrypting src → dst")` |
| `tag list` | Bold header, dim `(no tags)`, `  · tag` bullets |
| `tag add` | `u.Success("added tag \"...\"")`  |
| `log` | `u.Table()` with `─` separator, existing column colors |
| `registry list` | `u.Table()` with trust level column colors |
| `registry clear`/`update` | `u.Success()` |

## Registry Changes (`internal/registry/`)

Registry functions that emit warnings/notices should accept a `*ui.UI` parameter (or the resolver/fetcher structs should hold a reference).

| Current | New |
|---------|-----|
| `fmt.Fprintf(os.Stderr, "  warning: ...")` | `u.Warn(msg)` |
| `fmt.Fprintf(os.Stderr, "  [community] ...")` | `u.Warn("[community] ref — unverified third-party module")` |

## Testing

- **`internal/ui` tests**: Construct `ui.New(&buf, &errBuf)` with `bytes.Buffer` writers. Verify formatting with `color.Enabled = true` and `false`. Test `Table()` alignment with varying widths. Test `Summary()` color logic for all three conditions (failures, success, all-skipped). Test `formatDuration` across all ranges.
- **Runner tests**: Existing tests already inject `Out` via `bytes.Buffer`. The new `UI` field will be constructed from the same writer, so existing tests continue to work.
- **Existing action tests**: Unaffected — actions' dry-run code paths become dead code but still compile and pass.
- **Full suite**: `go test -race ./...` after all changes.

## Files Modified

- **New**: `internal/ui/ui.go`, `internal/ui/ui_test.go`
- **Modified**: `cmd/dotular/main.go`, `internal/runner/runner.go`, `internal/registry/resolver.go`, `internal/registry/fetch.go`
- **Unchanged**: `internal/color/color.go` (still used by `ui` package internally), all action files (dry-run output moves to runner/ui layer; actions' dry-run code paths become dead code)

## Notes

- `FORCE_COLOR` / `CLICOLOR_FORCE`: The existing `color.Init()` handles `NO_COLOR` and `TERM=dumb`. Unicode is tied to `color.Enabled`, so forced-color CI environments get unicode. This is intentional — environments that force color generally support unicode.
- The `Info(msg)` function exists for consistency — all user-facing output goes through `UI` methods, making it easy to add structured/JSON output mode later without hunting for raw `fmt.Printf` calls.
