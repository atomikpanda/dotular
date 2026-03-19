# Rich CLI Output Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create an `internal/ui` package that centralizes all user-facing terminal output with consistent color, unicode, timing, and result summaries, then migrate all output call sites to use it.

**Architecture:** A `UI` struct wraps `io.Writer` pair (Out + Err) and provides methods for every output pattern (headers, items, skips, summaries, tables, warnings). The runner and commands construct a `UI` and route all output through it. Dry-run output moves from actions to the runner.

**Tech Stack:** Go standard library (`io`, `fmt`, `time`, `strings`), existing `internal/color` package.

**Spec:** `docs/superpowers/specs/2026-03-18-rich-cli-output-design.md`

---

## File Structure

| File | Responsibility |
|------|---------------|
| `internal/ui/ui.go` | `UI` struct, all output methods, unicode/ASCII symbols, `formatDuration`, `Table` |
| `internal/ui/ui_test.go` | Tests for every method with color on/off, duration formatting, table alignment, summary color logic |
| `internal/runner/runner.go` | Replace all `fmt.Fprintf(r.Out, ...)` with `r.UI.*()` calls, add `ModuleResult` type, counters, timing, defer summary |
| `internal/runner/runner_test.go` | Update `newTestRunner` to initialize `UI` field |
| `cmd/dotular/main.go` | Replace all `fmt.Printf`/`fmt.Println` with `u.*()` calls, add item type breakdown to list |
| `internal/registry/resolver.go` | Accept `*ui.UI`, replace `fmt.Fprintf(os.Stderr, ...)` with `u.Warn()` |
| `internal/registry/fetch.go` | Accept `*ui.UI`, replace `fmt.Fprintf(os.Stderr, ...)` with `u.Warn()` |

---

## Task 1: Create `internal/ui` — symbols and formatDuration

**Files:**
- Create: `internal/ui/ui.go`
- Create: `internal/ui/ui_test.go`

- [ ] **Step 1: Write failing tests for symbol selection and formatDuration**

```go
// internal/ui/ui_test.go
package ui

import (
	"testing"
	"time"

	"github.com/atomikpanda/dotular/internal/color"
)

func TestSymbolsColorEnabled(t *testing.T) {
	color.Enabled = true
	s := symbols()
	if s.Check != "✓" {
		t.Errorf("Check = %q, want ✓", s.Check)
	}
	if s.Cross != "✗" {
		t.Errorf("Cross = %q, want ✗", s.Cross)
	}
	if s.Arrow != "→" {
		t.Errorf("Arrow = %q, want →", s.Arrow)
	}
	if s.Dash != "─" {
		t.Errorf("Dash = %q, want ─", s.Dash)
	}
	if s.Warn != "⚠" {
		t.Errorf("Warn = %q, want ⚠", s.Warn)
	}
	if s.Bullet != "·" {
		t.Errorf("Bullet = %q, want ·", s.Bullet)
	}
}

func TestSymbolsColorDisabled(t *testing.T) {
	color.Enabled = false
	defer func() { color.Enabled = true }()
	s := symbols()
	if s.Check != "[ok]" {
		t.Errorf("Check = %q, want [ok]", s.Check)
	}
	if s.Cross != "[FAIL]" {
		t.Errorf("Cross = %q, want [FAIL]", s.Cross)
	}
	if s.Arrow != "->" {
		t.Errorf("Arrow = %q, want ->", s.Arrow)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{42 * time.Millisecond, "(42ms)"},
		{999 * time.Millisecond, "(999ms)"},
		{1 * time.Second, "(1.0s)"},
		{3200 * time.Millisecond, "(3.2s)"},
		{59900 * time.Millisecond, "(59.9s)"},
		{61 * time.Second, "(1m 1s)"},
		{135 * time.Second, "(2m 15s)"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatDuration(tt.d)
			if got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -v`
Expected: Build fails — `package ui` doesn't exist yet.

- [ ] **Step 3: Implement symbols and formatDuration**

```go
// internal/ui/ui.go
package ui

import (
	"fmt"
	"io"
	"time"

	"github.com/atomikpanda/dotular/internal/color"
)

// UI centralizes all user-facing terminal output.
type UI struct {
	Out io.Writer // stdout: progress, results, tables
	Err io.Writer // stderr: warnings, notices
}

// New creates a UI with the given writers.
func New(out, err io.Writer) *UI {
	return &UI{Out: out, Err: err}
}

type syms struct {
	Check, Cross, Arrow, Dash, Warn, Bullet string
}

func symbols() syms {
	if color.Enabled {
		return syms{
			Check:  "✓",
			Cross:  "✗",
			Arrow:  "→",
			Dash:   "─",
			Warn:   "⚠",
			Bullet: "·",
		}
	}
	return syms{
		Check:  "[ok]",
		Cross:  "[FAIL]",
		Arrow:  "->",
		Dash:   "-",
		Warn:   "[!]",
		Bullet: "-",
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("(%dms)", d.Milliseconds())
	}
	if d < 60*time.Second {
		return fmt.Sprintf("(%.1fs)", d.Seconds())
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("(%dm %ds)", m, s)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ui/ -v`
Expected: All PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/ui.go internal/ui/ui_test.go
git commit -m "feat(ui): add UI struct, symbols, and formatDuration"
```

---

## Task 2: UI output methods — Header, Item, Skip, DryRun, Warn, Success, Info

**Files:**
- Modify: `internal/ui/ui.go`
- Modify: `internal/ui/ui_test.go`

- [ ] **Step 1: Write failing tests for output methods**

```go
// Append to internal/ui/ui_test.go
import (
	"bytes"
	"fmt"
	"strings"
)

func TestHeader(t *testing.T) {
	color.Enabled = false
	defer func() { color.Enabled = true }()
	var buf bytes.Buffer
	u := New(&buf, &bytes.Buffer{})
	u.Header("nvim")
	if !strings.Contains(buf.String(), "==> nvim") {
		t.Errorf("Header output = %q", buf.String())
	}
}

func TestSkipHeader(t *testing.T) {
	color.Enabled = false
	defer func() { color.Enabled = true }()
	var buf bytes.Buffer
	u := New(&buf, &bytes.Buffer{})
	u.SkipHeader("nvim", "tag mismatch")
	out := buf.String()
	if !strings.Contains(out, "nvim") || !strings.Contains(out, "tag mismatch") {
		t.Errorf("SkipHeader output = %q", out)
	}
}

func TestItem(t *testing.T) {
	color.Enabled = false
	defer func() { color.Enabled = true }()
	var buf bytes.Buffer
	u := New(&buf, &bytes.Buffer{})
	u.Item("install \"git\" via brew")
	out := buf.String()
	if !strings.Contains(out, "->") || !strings.Contains(out, "install") {
		t.Errorf("Item output = %q", out)
	}
}

func TestItemResultSuccess(t *testing.T) {
	color.Enabled = false
	defer func() { color.Enabled = true }()
	var buf bytes.Buffer
	u := New(&buf, &bytes.Buffer{})
	u.ItemResult("install \"git\"", 3200*time.Millisecond, nil)
	out := buf.String()
	if !strings.Contains(out, "[ok]") || !strings.Contains(out, "(3.2s)") {
		t.Errorf("ItemResult success = %q", out)
	}
}

func TestItemResultFailure(t *testing.T) {
	color.Enabled = false
	defer func() { color.Enabled = true }()
	var buf bytes.Buffer
	u := New(&buf, &bytes.Buffer{})
	u.ItemResult("install \"git\"", 300*time.Millisecond, fmt.Errorf("fail"))
	out := buf.String()
	if !strings.Contains(out, "[FAIL]") || !strings.Contains(out, "(300ms)") {
		t.Errorf("ItemResult failure = %q", out)
	}
}

func TestSkip(t *testing.T) {
	color.Enabled = false
	defer func() { color.Enabled = true }()
	var buf bytes.Buffer
	u := New(&buf, &bytes.Buffer{})
	u.Skip("already applied", "install \"git\"")
	out := buf.String()
	if !strings.Contains(out, "skip") || !strings.Contains(out, "already applied") {
		t.Errorf("Skip output = %q", out)
	}
}

func TestDryRun(t *testing.T) {
	color.Enabled = false
	defer func() { color.Enabled = true }()
	var buf bytes.Buffer
	u := New(&buf, &bytes.Buffer{})
	u.DryRun("install \"git\" via brew")
	out := buf.String()
	if !strings.Contains(out, "[dry-run]") || !strings.Contains(out, "install") {
		t.Errorf("DryRun output = %q", out)
	}
}

func TestWarn(t *testing.T) {
	color.Enabled = false
	defer func() { color.Enabled = true }()
	var out, errBuf bytes.Buffer
	u := New(&out, &errBuf)
	u.Warn("something bad")
	if out.Len() > 0 {
		t.Error("Warn should write to Err, not Out")
	}
	if !strings.Contains(errBuf.String(), "[!]") || !strings.Contains(errBuf.String(), "something bad") {
		t.Errorf("Warn output = %q", errBuf.String())
	}
}

func TestSuccess(t *testing.T) {
	color.Enabled = false
	defer func() { color.Enabled = true }()
	var buf bytes.Buffer
	u := New(&buf, &bytes.Buffer{})
	u.Success("done")
	if !strings.Contains(buf.String(), "[ok]") || !strings.Contains(buf.String(), "done") {
		t.Errorf("Success output = %q", buf.String())
	}
}

func TestInfo(t *testing.T) {
	var buf bytes.Buffer
	u := New(&buf, &bytes.Buffer{})
	u.Info("os: darwin")
	if !strings.Contains(buf.String(), "os: darwin") {
		t.Errorf("Info output = %q", buf.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -v`
Expected: FAIL — methods don't exist yet.

- [ ] **Step 3: Implement the output methods**

Add to `internal/ui/ui.go`:

```go
// Header prints a bold cyan section header: ==> name
func (u *UI) Header(name string) {
	fmt.Fprintf(u.Out, "\n%s\n", color.BoldCyan("==> "+name))
}

// SkipHeader prints a dim skipped section header.
func (u *UI) SkipHeader(name, reason string) {
	fmt.Fprintf(u.Out, "\n%s\n", color.Dim("==> "+name+"  [skip: "+reason+"]"))
}

// Item prints an action in progress.
func (u *UI) Item(desc string) {
	s := symbols()
	fmt.Fprintf(u.Out, "  %s %s\n", color.Dim(s.Arrow), desc)
}

// ItemResult prints a completed item with duration and pass/fail icon.
func (u *UI) ItemResult(desc string, dur time.Duration, err error) {
	s := symbols()
	durStr := formatDuration(dur)
	if err != nil {
		fmt.Fprintf(u.Out, "  %s %s %s\n", color.BoldRed(s.Cross), desc, color.Dim(durStr))
	} else {
		fmt.Fprintf(u.Out, "  %s %s %s\n", color.Green(s.Check), desc, color.Dim(durStr))
	}
}

// Skip prints a skipped item with reason.
func (u *UI) Skip(reason, desc string) {
	s := symbols()
	fmt.Fprintf(u.Out, "  %s\n", color.Dim(s.Dash+" skip ["+reason+"] "+desc))
}

// DryRun prints a dry-run preview.
func (u *UI) DryRun(desc string) {
	s := symbols()
	fmt.Fprintf(u.Out, "  %s %s\n", color.Dim(s.Arrow), color.Dim("[dry-run] "+desc))
}

// Warn prints a yellow warning to stderr.
func (u *UI) Warn(msg string) {
	s := symbols()
	fmt.Fprintf(u.Err, "%s %s\n", color.BoldYellow(s.Warn), msg)
}

// Success prints a green confirmation.
func (u *UI) Success(msg string) {
	s := symbols()
	fmt.Fprintf(u.Out, "%s %s\n", color.Green(s.Check), msg)
}

// Info prints a plain informational message.
func (u *UI) Info(msg string) {
	fmt.Fprintf(u.Out, "%s\n", msg)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ui/ -v`
Expected: All PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/
git commit -m "feat(ui): add Header, Item, Skip, DryRun, Warn, Success, Info methods"
```

---

## Task 3: UI Summary and ModuleSummary methods

**Files:**
- Modify: `internal/ui/ui.go`
- Modify: `internal/ui/ui_test.go`

- [ ] **Step 1: Write failing tests for Summary and ModuleSummary**

```go
// Append to internal/ui/ui_test.go

func TestSummarySuccess(t *testing.T) {
	color.Enabled = false
	defer func() { color.Enabled = true }()
	var buf bytes.Buffer
	u := New(&buf, &bytes.Buffer{})
	u.Summary(10, 2, 0, 3*time.Second)
	out := buf.String()
	if !strings.Contains(out, "10 applied") || !strings.Contains(out, "2 skipped") || !strings.Contains(out, "0 failed") {
		t.Errorf("Summary = %q", out)
	}
	if !strings.Contains(out, "(3.0s)") {
		t.Errorf("Summary missing duration: %q", out)
	}
	if !strings.Contains(out, "[ok]") {
		t.Errorf("Summary should have check icon: %q", out)
	}
}

func TestSummaryWithFailures(t *testing.T) {
	color.Enabled = false
	defer func() { color.Enabled = true }()
	var buf bytes.Buffer
	u := New(&buf, &bytes.Buffer{})
	u.Summary(5, 1, 2, 2*time.Second)
	out := buf.String()
	if !strings.Contains(out, "2 failed") {
		t.Errorf("Summary = %q", out)
	}
	if !strings.Contains(out, "[FAIL]") {
		t.Errorf("Summary should have cross icon: %q", out)
	}
}

func TestSummaryAllSkipped(t *testing.T) {
	color.Enabled = false
	defer func() { color.Enabled = true }()
	var buf bytes.Buffer
	u := New(&buf, &bytes.Buffer{})
	u.Summary(0, 5, 0, 500*time.Millisecond)
	out := buf.String()
	if !strings.Contains(out, "-") {
		t.Errorf("Summary should have dash icon for all-skipped: %q", out)
	}
}

func TestModuleSummary(t *testing.T) {
	color.Enabled = false
	defer func() { color.Enabled = true }()
	var buf bytes.Buffer
	u := New(&buf, &bytes.Buffer{})
	u.ModuleSummary(3, 1, 0)
	out := buf.String()
	if !strings.Contains(out, "3 applied") || !strings.Contains(out, "1 skipped") {
		t.Errorf("ModuleSummary = %q", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run 'TestSummary|TestModuleSummary' -v`
Expected: FAIL — methods don't exist.

- [ ] **Step 3: Implement Summary and ModuleSummary**

Add to `internal/ui/ui.go`:

```go
// Summary prints a final totals line after all modules.
func (u *UI) Summary(applied, skipped, failed int, elapsed time.Duration) {
	s := symbols()
	icon := s.Check
	colorFn := color.Green
	if failed > 0 {
		icon = s.Cross
		colorFn = color.BoldRed
	} else if applied == 0 {
		icon = s.Dash
		colorFn = color.Dim
	}
	line := fmt.Sprintf("%d applied, %d skipped, %d failed %s",
		applied, skipped, failed, formatDuration(elapsed))
	fmt.Fprintf(u.Out, "\n%s %s\n", colorFn(icon), colorFn(line))
}

// ModuleSummary prints a per-module count line.
func (u *UI) ModuleSummary(applied, skipped, failed int) {
	s := symbols()
	icon := s.Check
	colorFn := color.Green
	if failed > 0 {
		icon = s.Cross
		colorFn = color.BoldRed
	} else if applied == 0 {
		icon = s.Dash
		colorFn = color.Dim
	}
	line := fmt.Sprintf("%d applied, %d skipped, %d failed", applied, skipped, failed)
	fmt.Fprintf(u.Out, "  %s %s\n", colorFn(icon), color.Dim(line))
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ui/ -run 'TestSummary|TestModuleSummary' -v`
Expected: All PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/
git commit -m "feat(ui): add Summary and ModuleSummary methods"
```

---

## Task 4: UI Table method

**Files:**
- Modify: `internal/ui/ui.go`
- Modify: `internal/ui/ui_test.go`

- [ ] **Step 1: Write failing tests for Table**

```go
// Append to internal/ui/ui_test.go

func TestTable(t *testing.T) {
	color.Enabled = false
	defer func() { color.Enabled = true }()
	var buf bytes.Buffer
	u := New(&buf, &bytes.Buffer{})
	headers := []string{"NAME", "COUNT"}
	rows := [][]string{
		{"short", "1"},
		{"a-longer-name", "42"},
	}
	u.Table(headers, rows, nil)
	out := buf.String()
	// Header present
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "COUNT") {
		t.Errorf("missing headers: %q", out)
	}
	// Separator present
	if !strings.Contains(out, "-") {
		t.Errorf("missing separator: %q", out)
	}
	// Data present
	if !strings.Contains(out, "a-longer-name") {
		t.Errorf("missing data: %q", out)
	}
}

func TestTableEmpty(t *testing.T) {
	var buf bytes.Buffer
	u := New(&buf, &bytes.Buffer{})
	u.Table([]string{"A"}, nil, nil)
	// Should still print header + separator even with no rows
	if !strings.Contains(buf.String(), "A") {
		t.Errorf("empty table = %q", buf.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run TestTable -v`
Expected: FAIL — `Table` method doesn't exist.

- [ ] **Step 3: Implement Table**

Add to `internal/ui/ui.go`:

```go
// Table prints aligned columns with a separator line.
// colColors is optional — if provided, colColors[i] is applied to column i in each data row.
// Pass nil for no per-column coloring.
func (u *UI) Table(headers []string, rows [][]string, colColors []func(string) string) {
	s := symbols()

	// Calculate column widths from headers + data.
	widths := make([]int, len(headers))
	for i, h := range headers {
		if len(h) > widths[i] {
			widths[i] = len(h)
		}
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	// Print header.
	headerLine := formatRow(headers, widths)
	fmt.Fprintln(u.Out, color.Bold(headerLine))

	// Separator.
	totalWidth := 0
	for _, w := range widths {
		totalWidth += w
	}
	totalWidth += (len(widths) - 1) * 2 // 2-space gaps
	sep := strings.Repeat(s.Dash, totalWidth)
	fmt.Fprintln(u.Out, color.Dim(sep))

	// Rows.
	for _, row := range rows {
		cells := make([]string, len(headers))
		for i := range headers {
			val := ""
			if i < len(row) {
				val = row[i]
			}
			padded := fmt.Sprintf("%-*s", widths[i], val)
			if colColors != nil && i < len(colColors) && colColors[i] != nil {
				padded = colColors[i](padded)
			}
			cells[i] = padded
		}
		fmt.Fprintln(u.Out, strings.Join(cells, "  "))
	}
}

func formatRow(cells []string, widths []int) string {
	padded := make([]string, len(cells))
	for i, c := range cells {
		padded[i] = fmt.Sprintf("%-*s", widths[i], c)
	}
	return strings.Join(padded, "  ")
}
```

(Add `"strings"` to the import block.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ui/ -run TestTable -v`
Expected: All PASS.

- [ ] **Step 5: Run all ui tests**

Run: `go test ./internal/ui/ -v`
Expected: All PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/ui/
git commit -m "feat(ui): add Table method with auto-width columns"
```

---

## Task 5: Add `ModuleResult` type and wire `UI` into Runner

**Files:**
- Modify: `internal/runner/runner.go`
- Modify: `internal/runner/runner_test.go`

- [ ] **Step 1: Write failing test for ModuleResult and UI field**

Add import for `"github.com/atomikpanda/dotular/internal/ui"` to `runner_test.go`.

Replace `newTestRunner`:

```go
func newTestRunner(cfg config.Config) *Runner {
	var buf bytes.Buffer
	return &Runner{
		Config:      cfg,
		DryRun:      true,
		Verbose:     true,
		Atomic:      false,
		OS:          "darwin",
		MachineTags: []string{"darwin", "amd64", "testhost"},
		Out:         &buf,
		UI:          ui.New(&buf, &bytes.Buffer{}),
		Command:     "apply",
	}
}
```

**CRITICAL:** Many existing tests create their own `bytes.Buffer` and assign it to `r.Out` after calling `newTestRunner`. After the UI migration (Task 6), all output goes through `r.UI`, not `r.Out`. All such tests must be updated to also re-create `r.UI` from the same buffer. Replace this pattern:

```go
var buf bytes.Buffer
r.Out = &buf
```

With:

```go
var buf bytes.Buffer
r.Out = &buf
r.UI = ui.New(&buf, &bytes.Buffer{})
```

This applies to: `TestApplyAllTagFilter`, `TestApplyItemSkipIf`, `TestApplyItemVerify`, `TestApplyModuleDryRunWithHooks`, `TestApplyModuleDryRunWithSyncHooks`, `TestApplyModuleSkipsOSMismatch`, `TestApplyItemWithItemHooks`, `TestApplyModuleNonDryRun`, `TestApplyModuleWithAtomic`, `TestApplyModuleAtomicRollback`, `TestApplyModuleWithHooksNonDryRun`, `TestVerifyModuleDryRun`, `TestVerifyAllDryRun`, `TestRunHookDryRun`, `TestRunHookVerbose`, `TestVerifyModuleFailure`, `TestApplyModuleFileItemWithSnapshot`, `TestApplyModuleDirItemWithSnapshot`.

Also add a test assertion to `TestNewRunner`:
```go
if r.UI == nil {
    t.Error("expected UI to be initialized")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/runner/ -v`
Expected: Build fails — `Runner` has no `UI` field.

- [ ] **Step 3: Add UI field and ModuleResult to Runner**

In `internal/runner/runner.go`:

1. Add import `"github.com/atomikpanda/dotular/internal/ui"`
2. Add `UI *ui.UI` field to `Runner` struct
3. Initialize it in `New()`: `r.UI = ui.New(r.Out, os.Stderr)`
4. Add `ModuleResult` type:

```go
// ModuleResult holds counters from applying a single module.
type ModuleResult struct {
	Applied int
	Skipped int
	Failed  int
	Err     error
}
```

Do NOT change any output calls yet — just add the field and type.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/runner/ -v`
Expected: All PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runner/
git commit -m "feat(runner): add UI field and ModuleResult type"
```

---

## Task 6: Migrate Runner output to UI — ApplyModule and applyItem

This is the largest task. Replace all `fmt.Fprintf(r.Out, ...)` calls in the apply path with `r.UI.*()` calls, add timing and counters.

**Files:**
- Modify: `internal/runner/runner.go`

- [ ] **Step 1: Migrate `ApplyAll`**

Replace `ApplyAll` body. Key changes:
- Add `totalApplied, totalSkipped, totalFailed` counters
- Record `start := time.Now()` at the beginning
- `defer r.UI.Summary(...)` to always print summary
- Change `ApplyModule` call to return `ModuleResult`, accumulate counters
- For tag-skip verbose output: replace `fmt.Fprintf(r.Out, ...)` with `r.UI.SkipHeader(mod.Name, "tag mismatch")`
- On module error, break instead of return (so defer runs)

```go
func (r *Runner) ApplyAll(ctx context.Context) error {
	start := time.Now()
	var totalApplied, totalSkipped, totalFailed int
	var firstErr error

	defer func() {
		r.UI.Summary(totalApplied, totalSkipped, totalFailed, time.Since(start))
	}()

	for _, mod := range r.Config.Modules {
		if !r.matchesTags(mod) {
			if r.Verbose {
				r.UI.SkipHeader(mod.Name, "tag mismatch")
			}
			continue
		}
		result := r.ApplyModule(ctx, mod)
		totalApplied += result.Applied
		totalSkipped += result.Skipped
		totalFailed += result.Failed
		if result.Err != nil {
			firstErr = result.Err
			break
		}
	}
	return firstErr
}
```

- [ ] **Step 2: Change `ApplyModule` return type to `ModuleResult`**

Replace `ApplyModule` signature and body:
- Return `ModuleResult` instead of `error`
- Replace `fmt.Fprintf(r.Out, ...)` header with `r.UI.Header(mod.Name)`
- Replace rollback `fmt.Fprintf` with `r.UI.Warn(...)`
- Call `r.UI.ModuleSummary(...)` at end
- Count items via `applyItems` (which now returns counts)

```go
func (r *Runner) ApplyModule(ctx context.Context, mod config.Module) ModuleResult {
	r.UI.Header(mod.Name)

	if err := r.runHook(ctx, mod.Hooks.BeforeApply, "module", mod.Name, "before_apply"); err != nil {
		return ModuleResult{Err: err}
	}

	var snap *snapshot.Snapshot
	if r.Atomic && !r.DryRun {
		var err error
		snap, err = snapshot.New()
		if err != nil {
			return ModuleResult{Err: fmt.Errorf("module %q: create snapshot: %w", mod.Name, err)}
		}
	}

	applied, skipped, failed, applyErr := r.applyItems(ctx, mod, snap)

	if applyErr != nil && snap != nil {
		r.UI.Warn(fmt.Sprintf("[rollback] restoring snapshot after failure in %q", mod.Name))
		if restoreErr := snap.Restore(); restoreErr != nil {
			r.UI.Warn(fmt.Sprintf("[rollback] restore error: %v", restoreErr))
		}
		snap.Discard()
		r.UI.ModuleSummary(applied, skipped, failed)
		return ModuleResult{Applied: applied, Skipped: skipped, Failed: failed, Err: applyErr}
	}
	if snap != nil {
		snap.Discard()
	}

	if applyErr != nil {
		r.UI.ModuleSummary(applied, skipped, failed)
		return ModuleResult{Applied: applied, Skipped: skipped, Failed: failed, Err: applyErr}
	}

	if err := r.runHook(ctx, mod.Hooks.AfterApply, "module", mod.Name, "after_apply"); err != nil {
		r.UI.ModuleSummary(applied, skipped, failed)
		return ModuleResult{Applied: applied, Skipped: skipped, Failed: failed, Err: err}
	}

	r.UI.ModuleSummary(applied, skipped, failed)
	return ModuleResult{Applied: applied, Skipped: skipped, Failed: failed}
}
```

- [ ] **Step 3: Update `applyItems` to return counters**

Change signature to `func (r *Runner) applyItems(ctx context.Context, mod config.Module, snap *snapshot.Snapshot) (applied, skipped, failed int, err error)` and accumulate counts from `applyItem`.

- [ ] **Step 4: Migrate `applyItem` with timing and UI calls**

Change `applyItem` signature to return an outcome enum so `applyItems` can count:

```go
type itemOutcome int
const (
	outcomeApplied itemOutcome = iota
	outcomeSkipped
	outcomeFailed
)

func (r *Runner) applyItem(ctx context.Context, mod config.Module, item config.Item, snap *snapshot.Snapshot) (itemOutcome, error) {
```

In `applyItems`, count outcomes:
```go
for _, item := range mod.Items {
    outcome, err := r.applyItem(ctx, mod, item, snap)
    switch outcome {
    case outcomeApplied:
        applied++
    case outcomeSkipped:
        skipped++
    case outcomeFailed:
        failed++
    }
    if err != nil {
        return applied, skipped, failed, err
    }
}
```

Key changes inside `applyItem`:
- Record `start := time.Now()` before the run
- In dry-run mode: call `r.UI.DryRun(action.Describe())` and return `(outcomeApplied, nil)` — do NOT call `action.Run(ctx, true)`
- On skip (OS mismatch, skip_if, already applied): return `(outcomeSkipped, nil)` and call `r.UI.Skip(reason, desc)` instead of `fmt.Fprintf`
- After successful `action.Run(ctx, false)`: call `r.UI.ItemResult(desc, time.Since(start), nil)` and return `(outcomeApplied, nil)`
- On run error: call `r.UI.ItemResult(desc, time.Since(start), runErr)` and return `(outcomeFailed, err)`
- For `FileAction` with `Permissions`, print permissions status via `r.UI.Info("     " + ps)` after ItemResult

- [ ] **Step 5: Update `runHook` to use UI**

Replace `fmt.Fprintf(r.Out, ...)` calls in `runHook` with:
- Dry-run: `r.UI.DryRun(fmt.Sprintf("hook %s.%s: %s", hookName, scope, cmd))`
- Verbose: `r.UI.Info(fmt.Sprintf("  hook %s (%s %q)", hookName, scope, name))` — note: Info writes to Out, keep indentation

- [ ] **Step 6: Update `main.go` call sites for `ApplyModule` return type (MUST be in same commit)**

**CRITICAL:** `ApplyModule` now returns `ModuleResult` instead of `error`. `cmd/dotular/main.go` will not compile until its call sites are updated. Do this in the same step as the runner changes.

In `applyCmd`: Replace `if err := r.ApplyModule(ctx, *mod); err != nil {` with:
```go
result := r.ApplyModule(ctx, *mod)
if result.Err != nil {
    return result.Err
}
```

In `directionCmd`: Same pattern.

Also update `verifyCmd` to use `u.Warn(...)`:
```go
u := ui.New(os.Stdout, os.Stderr)
// ...
if !allPassed {
    u.Warn("some verify checks failed")
    os.Exit(1)
}
```

Add import `"github.com/atomikpanda/dotular/internal/ui"` to `main.go`.

- [ ] **Step 7: Run tests**

Run: `go test ./internal/runner/ -v`
Expected: All PASS. Some tests check for string content in output — verify `containsStr` assertions still match (e.g., "skip", "rollback", "hook" should still appear in output). Dry-run output format changes from action's own format to `r.UI.DryRun()` — tests that assert dry-run content may need their string match updated.

- [ ] **Step 8: Run full suite**

Run: `go test -race ./...`
Expected: All PASS (including `cmd/dotular` package).

- [ ] **Step 9: Commit**

```bash
git add internal/runner/ cmd/dotular/main.go
git commit -m "feat(runner): migrate all output to UI methods, add counters and timing"
```

---

## Task 7: Migrate Runner verify output to UI

**Files:**
- Modify: `internal/runner/runner.go`

- [ ] **Step 1: Migrate `VerifyModule` output**

Replace:
- `fmt.Fprintf(r.Out, "\n%s\n", color.BoldCyan("==> "+mod.Name))` → `r.UI.Header(mod.Name)`
- `fmt.Fprintf(r.Out, "  %s\n", color.Dim(...no verify...))` → `r.UI.Skip("no verify", item.Type())`
- `fmt.Fprintf(r.Out, "  %s  %s\n", color.BoldRed("FAIL"), ...)` → `r.UI.ItemResult(action.Describe(), dur, verifyErr)`
- `fmt.Fprintf(r.Out, "  %s  %s\n", color.BoldGreen("PASS"), ...)` → `r.UI.ItemResult(action.Describe(), dur, nil)`
- Add `time.Now()` / `time.Since()` around the `shell.Run(ctx, item.Verify)` call

- [ ] **Step 2: Run tests**

Run: `go test ./internal/runner/ -run TestVerify -v`
Expected: All PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/runner/
git commit -m "feat(runner): migrate verify output to UI methods"
```

---

## Task 8: Migrate remaining `main.go` commands to UI

**Files:**
- Modify: `cmd/dotular/main.go`

- [ ] **Step 1: Create UI instance and migrate add command**

At the start of each command's RunE, create `u := ui.New(os.Stdout, os.Stderr)`. Migrate:

- `add`: Replace `fmt.Printf("added %s ...")` with `u.Success(fmt.Sprintf("added %s %q to module %q", typeStr, baseName, moduleName))` and `u.Info(fmt.Sprintf("  store: %s", dest))` and `u.Info(fmt.Sprintf("  config: %s", configFile))`

- [ ] **Step 2: Migrate list command**

Replace the `fmt.Fprintf` in list with item type breakdown:

```go
for _, mod := range cfg.Modules {
    counts := make(map[string]int)
    for _, item := range mod.Items {
        counts[item.Type()]++
    }
    total := len(mod.Items)
    breakdown := formatTypeCounts(counts)
    fmt.Fprintf(os.Stdout, "%s  %s\n",
        color.Bold(fmt.Sprintf("%-30s", mod.Name)),
        color.Dim(fmt.Sprintf("%d items (%s)", total, breakdown)))
}
```

Add helper:
```go
func formatTypeCounts(counts map[string]int) string {
    // Sort keys for deterministic output
    types := []string{"package", "file", "directory", "script", "binary", "run", "setting"}
    var parts []string
    for _, t := range types {
        if n, ok := counts[t]; ok && n > 0 {
            label := t
            if n != 1 {
                label += "s"
            }
            parts = append(parts, fmt.Sprintf("%d %s", n, label))
        }
    }
    return strings.Join(parts, ", ")
}
```

- [ ] **Step 3: Migrate platform, encrypt, decrypt commands**

- `platform`: `u.Info(fmt.Sprintf("os: %s", platform.Current()))`
- `encrypt`: `u.Info(fmt.Sprintf("encrypting %s → %s", src, dst))` (unicode arrow — `symbols().Arrow`)
- `decrypt`: `u.Info(fmt.Sprintf("decrypting %s → %s", src, dst))`

- [ ] **Step 4: Migrate tag commands**

- `tag list`: `u.Info(color.Bold(fmt.Sprintf("machine config: %s", tags.ConfigPath())))`, dim `(no tags)`, bullets with `s.Bullet`
- `tag add`: `u.Success(fmt.Sprintf("added tag %q", args[0]))`

- [ ] **Step 5: Migrate log command to use ui.Table**

Replace the manual table formatting with:

```go
u := ui.New(os.Stdout, os.Stderr)
headers := []string{"TIME", "COMMAND", "MODULE", "OUTCOME", "ITEM"}
var rows [][]string
var colColors []func(string) string
// ... build rows and set colColors[3] for outcome column ...
u.Table(headers, rows, colColors)
u.Info(fmt.Sprintf("\nlog: %s", audit.LogPath()))
```

Note: outcome coloring needs per-row logic. Since `Table`'s colColors applies uniformly per column, we need to pre-color the outcome cell values instead. Pass `nil` for colColors and apply color to the cell string directly before adding to rows.

- [ ] **Step 6: Migrate registry commands**

- `registry list`: Same `Table` approach as log, with trust column pre-colored
- `registry clear`: `u.Success("registry cache cleared")`
- `registry update`: `u.Success("registry modules updated")`

- [ ] **Step 7: Remove `repeatStr` helper** (no longer needed — Table handles separators)

- [ ] **Step 8: Run full test suite**

Run: `go test -race ./...`
Expected: All PASS.

- [ ] **Step 9: Run `go vet`**

Run: `go vet ./...`
Expected: No errors.

- [ ] **Step 10: Commit**

```bash
git add cmd/dotular/main.go
git commit -m "feat(cmd): migrate all command output to UI methods"
```

---

## Task 9: Migrate registry package to UI

**Files:**
- Modify: `internal/registry/resolver.go`
- Modify: `internal/registry/fetch.go`

- [ ] **Step 1: Add UI parameter to Resolve and Fetch**

Update `Resolve` signature to accept `*ui.UI`:
```go
func Resolve(ctx context.Context, cfg config.Config, configPath string, noCache bool, u *ui.UI) (config.Config, error)
```

Replace:
- `fmt.Fprintf(os.Stderr, "  [community] %s ...")` → `u.Warn(fmt.Sprintf("[community] %s — unverified third-party module", mod.From))`
- `fmt.Fprintf(os.Stderr, "  [private]   %s\n", ...)` → `u.Warn(fmt.Sprintf("[private] %s", mod.From))`
- `fmt.Fprintf(os.Stderr, "  warning: could not save lockfile: ...")` → `u.Warn(fmt.Sprintf("could not save lockfile: %v", err))`

Update `Fetch` similarly for the cache warning:
- `fmt.Fprintf(os.Stderr, "  warning: could not cache registry module: ...")` → `u.Warn(...)`

- [ ] **Step 2: Update ALL call sites in `main.go`**

There are **two** call sites for `registry.Resolve` in `main.go`:

1. `loadAndResolveConfig` (line ~90):
```go
func loadAndResolveConfig(ctx context.Context) (config.Config, error) {
	cfg, err := loadConfig()
	if err != nil {
		return config.Config{}, err
	}
	u := ui.New(os.Stdout, os.Stderr)
	return registry.Resolve(ctx, cfg, configFile, noCache, u)
}
```

2. `registry update` command (line ~650) — **do not forget this one:**
```go
u := ui.New(os.Stdout, os.Stderr)
_, err = registry.Resolve(ctx, cfg, configFile, true, u)
```

- [ ] **Step 3: Update registry tests**

Registry tests that call `Resolve` or `Fetch` need to pass a `*ui.UI`. Use `ui.New(&bytes.Buffer{}, &bytes.Buffer{})` in tests.

- [ ] **Step 4: Run full test suite**

Run: `go test -race ./...`
Expected: All PASS.

- [ ] **Step 5: Run `go vet`**

Run: `go vet ./...`
Expected: No errors.

- [ ] **Step 6: Commit**

```bash
git add internal/registry/ cmd/dotular/main.go
git commit -m "feat(registry): route warnings through UI"
```

---

## Task 10: Final verification and cleanup

**Files:**
- All modified files

- [ ] **Step 1: Remove unused imports**

Check all modified files for unused imports (especially `color` in `main.go` if all color calls now go through `ui`). The `color` package may still be needed for `color.Init()` in `main()` and for `color.Bold`/`color.Dim` in a few places in `main.go` (like the list command's `color.Bold` for module names).

- [ ] **Step 2: Run full test suite with race detector**

Run: `go test -race ./...`
Expected: All PASS.

- [ ] **Step 3: Run vet**

Run: `go vet ./...`
Expected: Clean.

- [ ] **Step 4: Verify coverage**

Run: `go test -coverprofile=coverage.out -covermode=atomic ./... && go tool cover -func coverage.out | tail -1`
Expected: >= 80% total coverage.

- [ ] **Step 5: Manual smoke test**

Run: `go build -o build/dotular ./cmd/dotular && ./build/dotular list`
Expected: Colored, formatted output with item type breakdown.

Run: `./build/dotular status`
Expected: Colored headers, dry-run items with unicode arrows, summary line at end.

Run: `NO_COLOR=1 ./build/dotular list`
Expected: ASCII fallback, no ANSI codes.

- [ ] **Step 6: Commit any cleanup**

```bash
git add -A
git commit -m "chore: cleanup unused imports after UI migration"
```
