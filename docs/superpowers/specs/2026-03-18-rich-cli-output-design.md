# Rich CLI Output Design

## Goal

Improve dotular CLI output with consistent formatting, color, light unicode, item timing, and result summaries across all commands.

## Approach

Create a new `internal/ui` package that centralizes all user-facing output formatting. Update all call sites in `cmd/dotular/main.go`, `internal/runner/runner.go`, and `internal/registry/` to use `ui.*` helpers instead of raw `fmt.Printf` + `color.*` calls.

## `internal/ui` Package API

All functions respect `color.Enabled`. Unicode characters degrade to ASCII when color is disabled (no-color environments often have limited unicode support).

| Function | Output | Example |
|----------|--------|---------|
| `Header(name)` | Bold cyan section header | `==> nvim` |
| `SkipHeader(name, reason)` | Dim skipped section | `==> nvim  [skip: tag mismatch]` |
| `Item(desc)` | Action in progress | `  -> description` (unicode arrow when enabled) |
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

## Runner Changes (`internal/runner/runner.go`)

### Counters and Timing

- Add per-module counters: `applied`, `skipped`, `failed` (local variables in `ApplyModule`)
- Add global counters in `ApplyAll` accumulated from per-module results
- Time each item with `time.Now()` / `time.Since()`
- Time each module and the overall apply run

### Output Mapping

| Current | New |
|---------|-----|
| `fmt.Printf("==> %s\n", BoldCyan(name))` | `ui.Header(name)` |
| `fmt.Printf("  -> %s\n", Dim(desc))` | `ui.Item(desc)` then `ui.ItemResult(desc, dur, err)` on completion |
| Dim skip messages | `ui.Skip(reason, desc)` |
| Dry-run `[dry-run]` messages | `ui.DryRun(desc)` |
| `BoldYellow` rollback messages | `ui.Warn("restoring snapshot...")` |
| Verbose hook messages | `ui.Info()` or `ui.DryRun()` |
| Verify `PASS`/`FAIL` | `ui.ItemResult()` with nil/non-nil error |
| (none) | `ui.ModuleSummary()` after each module |
| (none) | `ui.Summary()` after all modules |

### ApplyModule Return Value

`ApplyModule` should return counts (`applied`, `skipped`, `failed`) so `ApplyAll` can accumulate totals. This may require changing the return signature from `error` to a struct or multiple returns.

## Command Changes (`cmd/dotular/main.go`)

| Command | Change |
|---------|--------|
| `add` | `ui.Success("added file ...")` + indented `ui.Info()` lines for store/config paths |
| `list` | Bold module name, dim count, item type breakdown: `nvim  3 items (2 files, 1 package)` |
| `status` | Automatic via runner changes (dryRun=true) |
| `platform` | `ui.Info("os: darwin")` |
| `verify` | Failure: `ui.Warn("some verify checks failed")` |
| `encrypt`/`decrypt` | `ui.Info("encrypting src → dst")` |
| `tag list` | Bold header, dim `(no tags)`, `  · tag` bullets |
| `tag add` | `ui.Success("added tag \"...\"")`  |
| `log` | `ui.Table()` with `─` separator, existing column colors |
| `registry list` | `ui.Table()` with trust level column colors |
| `registry clear`/`update` | `ui.Success()` |

## Registry Changes (`internal/registry/`)

| Current | New |
|---------|-----|
| `fmt.Fprintf(os.Stderr, "  warning: ...")` | `ui.Warn(msg)` |
| `fmt.Fprintf(os.Stderr, "  [community] ...")` | `ui.Info()` to stderr (or new `ui.Notice()`) |

## Testing

- **`internal/ui` tests**: Capture stdout via pipe, verify formatting with `color.Enabled = true` and `false`. Test `Table()` alignment with varying widths. Test `Summary()` color changes based on failure count.
- **Existing tests**: No assertions on stdout formatting, so changes are safe. Full `go test -race ./...` verification.
- **No mocks needed**: Output functions write directly to stdout/stderr.

## Files Modified

- **New**: `internal/ui/ui.go`, `internal/ui/ui_test.go`
- **Modified**: `cmd/dotular/main.go`, `internal/runner/runner.go`, `internal/registry/resolver.go`, `internal/registry/fetch.go`
- **Unchanged**: `internal/color/color.go` (still used by `ui` package internally), all action files (dry-run output moves to runner/ui layer)

## Open Question: Action Dry-Run Output

Currently each action's `Run()` method prints its own `[dry-run]` line. Two options:

1. **Move dry-run output to runner** — runner calls `ui.DryRun(action.Describe())` before skipping `Run()` in dry-run mode. Actions no longer print anything in dry-run. Cleaner but changes action contract.
2. **Keep in actions** — actions continue to print dry-run output, but use `ui.DryRun()` instead of raw `fmt.Printf`. More scattered but less refactoring.

Recommendation: Option 1 — the runner already knows about dry-run mode and controls whether `Run()` is called. Centralizing dry-run output there is cleaner and means actions don't need to import `ui`.

However: the runner currently *does* call `Run(ctx, true)` for dry-run, letting actions format their own message. Option 1 would mean the runner calls `ui.DryRun(action.Describe())` and skips calling `Run()` entirely in dry-run mode. This requires that `Describe()` provides enough detail (it currently does for all action types).
