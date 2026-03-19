package ui

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atomikpanda/dotular/internal/color"
)

func saveColor() bool {
	old := color.Enabled
	return old
}

func TestSymbolsColorEnabled(t *testing.T) {
	old := saveColor()
	defer func() { color.Enabled = old }()
	color.Enabled = true

	u := New(&bytes.Buffer{}, &bytes.Buffer{})
	s := u.symbols()

	if s.Check != "✓" {
		t.Errorf("Check = %q, want %q", s.Check, "✓")
	}
	if s.Cross != "✗" {
		t.Errorf("Cross = %q, want %q", s.Cross, "✗")
	}
	if s.Arrow != "→" {
		t.Errorf("Arrow = %q, want %q", s.Arrow, "→")
	}
	if s.Dash != "─" {
		t.Errorf("Dash = %q, want %q", s.Dash, "─")
	}
	if s.Warn != "⚠" {
		t.Errorf("Warn = %q, want %q", s.Warn, "⚠")
	}
	if s.Bullet != "·" {
		t.Errorf("Bullet = %q, want %q", s.Bullet, "·")
	}
}

func TestSymbolsColorDisabled(t *testing.T) {
	old := saveColor()
	defer func() { color.Enabled = old }()
	color.Enabled = false

	u := New(&bytes.Buffer{}, &bytes.Buffer{})
	s := u.symbols()

	if s.Check != "[ok]" {
		t.Errorf("Check = %q, want %q", s.Check, "[ok]")
	}
	if s.Cross != "[FAIL]" {
		t.Errorf("Cross = %q, want %q", s.Cross, "[FAIL]")
	}
	if s.Arrow != "->" {
		t.Errorf("Arrow = %q, want %q", s.Arrow, "->")
	}
	if s.Dash != "-" {
		t.Errorf("Dash = %q, want %q", s.Dash, "-")
	}
	if s.Warn != "[!]" {
		t.Errorf("Warn = %q, want %q", s.Warn, "[!]")
	}
	if s.Bullet != "-" {
		t.Errorf("Bullet = %q, want %q", s.Bullet, "-")
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"42ms", 42 * time.Millisecond, "(42ms)"},
		{"999ms", 999 * time.Millisecond, "(999ms)"},
		{"1s", 1 * time.Second, "(1.0s)"},
		{"3.2s", 3200 * time.Millisecond, "(3.2s)"},
		{"59.9s", 59900 * time.Millisecond, "(59.9s)"},
		{"1m1s", 61 * time.Second, "(1m 1s)"},
		{"2m15s", 135 * time.Second, "(2m 15s)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDuration(tt.d)
			if got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestHeader(t *testing.T) {
	old := saveColor()
	defer func() { color.Enabled = old }()
	color.Enabled = false

	var out bytes.Buffer
	u := New(&out, &bytes.Buffer{})
	u.Header("nvim")
	got := out.String()
	if !strings.Contains(got, "==> nvim") {
		t.Errorf("Header output = %q, want to contain %q", got, "==> nvim")
	}
}

func TestSkipHeader(t *testing.T) {
	old := saveColor()
	defer func() { color.Enabled = old }()
	color.Enabled = false

	var out bytes.Buffer
	u := New(&out, &bytes.Buffer{})
	u.SkipHeader("nvim", "tag mismatch")
	got := out.String()
	if !strings.Contains(got, "nvim") {
		t.Errorf("SkipHeader output = %q, want to contain %q", got, "nvim")
	}
	if !strings.Contains(got, "tag mismatch") {
		t.Errorf("SkipHeader output = %q, want to contain %q", got, "tag mismatch")
	}
}

func TestItem(t *testing.T) {
	old := saveColor()
	defer func() { color.Enabled = old }()
	color.Enabled = false

	var out bytes.Buffer
	u := New(&out, &bytes.Buffer{})
	u.Item("install neovim")
	got := out.String()
	if !strings.Contains(got, "->") {
		t.Errorf("Item output = %q, want to contain %q", got, "->")
	}
	if !strings.Contains(got, "install neovim") {
		t.Errorf("Item output = %q, want to contain %q", got, "install neovim")
	}
}

func TestItemResultSuccess(t *testing.T) {
	old := saveColor()
	defer func() { color.Enabled = old }()
	color.Enabled = false

	var out bytes.Buffer
	u := New(&out, &bytes.Buffer{})
	u.ItemResult("install neovim", 3200*time.Millisecond, nil)
	got := out.String()
	if !strings.Contains(got, "[ok]") {
		t.Errorf("ItemResult output = %q, want to contain %q", got, "[ok]")
	}
	if !strings.Contains(got, "(3.2s)") {
		t.Errorf("ItemResult output = %q, want to contain %q", got, "(3.2s)")
	}
}

func TestItemResultFailure(t *testing.T) {
	old := saveColor()
	defer func() { color.Enabled = old }()
	color.Enabled = false

	var out bytes.Buffer
	u := New(&out, &bytes.Buffer{})
	u.ItemResult("install neovim", 300*time.Millisecond, errors.New("not found"))
	got := out.String()
	if !strings.Contains(got, "[FAIL]") {
		t.Errorf("ItemResult output = %q, want to contain %q", got, "[FAIL]")
	}
	if !strings.Contains(got, "(300ms)") {
		t.Errorf("ItemResult output = %q, want to contain %q", got, "(300ms)")
	}
}

func TestSkip(t *testing.T) {
	old := saveColor()
	defer func() { color.Enabled = old }()
	color.Enabled = false

	var out bytes.Buffer
	u := New(&out, &bytes.Buffer{})
	u.Skip("already applied", "install neovim")
	got := out.String()
	if !strings.Contains(got, "skip") {
		t.Errorf("Skip output = %q, want to contain %q", got, "skip")
	}
	if !strings.Contains(got, "already applied") {
		t.Errorf("Skip output = %q, want to contain %q", got, "already applied")
	}
}

func TestDryRun(t *testing.T) {
	old := saveColor()
	defer func() { color.Enabled = old }()
	color.Enabled = false

	var out bytes.Buffer
	u := New(&out, &bytes.Buffer{})
	u.DryRun("install neovim")
	got := out.String()
	if !strings.Contains(got, "[dry-run]") {
		t.Errorf("DryRun output = %q, want to contain %q", got, "[dry-run]")
	}
}

func TestWarn(t *testing.T) {
	old := saveColor()
	defer func() { color.Enabled = old }()
	color.Enabled = false

	var out, errBuf bytes.Buffer
	u := New(&out, &errBuf)
	u.Warn("something is wrong")
	if out.Len() != 0 {
		t.Errorf("Warn wrote to Out: %q", out.String())
	}
	got := errBuf.String()
	if !strings.Contains(got, "[!]") {
		t.Errorf("Warn output = %q, want to contain %q", got, "[!]")
	}
}

func TestSuccess(t *testing.T) {
	old := saveColor()
	defer func() { color.Enabled = old }()
	color.Enabled = false

	var out bytes.Buffer
	u := New(&out, &bytes.Buffer{})
	u.Success("all done")
	got := out.String()
	if !strings.Contains(got, "[ok]") {
		t.Errorf("Success output = %q, want to contain %q", got, "[ok]")
	}
}

func TestInfo(t *testing.T) {
	old := saveColor()
	defer func() { color.Enabled = old }()
	color.Enabled = false

	var out bytes.Buffer
	u := New(&out, &bytes.Buffer{})
	u.Info("hello world")
	got := out.String()
	if got != "hello world\n" {
		t.Errorf("Info output = %q, want %q", got, "hello world\n")
	}
}

func TestSummarySuccess(t *testing.T) {
	old := saveColor()
	defer func() { color.Enabled = old }()
	color.Enabled = false

	var out bytes.Buffer
	u := New(&out, &bytes.Buffer{})
	u.Summary(10, 2, 0, 3*time.Second)
	got := out.String()
	for _, want := range []string{"10 applied", "2 skipped", "0 failed", "(3.0s)", "[ok]"} {
		if !strings.Contains(got, want) {
			t.Errorf("Summary output = %q, want to contain %q", got, want)
		}
	}
}

func TestSummaryWithFailures(t *testing.T) {
	old := saveColor()
	defer func() { color.Enabled = old }()
	color.Enabled = false

	var out bytes.Buffer
	u := New(&out, &bytes.Buffer{})
	u.Summary(5, 1, 2, 10*time.Second)
	got := out.String()
	if !strings.Contains(got, "2 failed") {
		t.Errorf("Summary output = %q, want to contain %q", got, "2 failed")
	}
	if !strings.Contains(got, "[FAIL]") {
		t.Errorf("Summary output = %q, want to contain %q", got, "[FAIL]")
	}
}

func TestSummaryAllSkipped(t *testing.T) {
	old := saveColor()
	defer func() { color.Enabled = old }()
	color.Enabled = false

	var out bytes.Buffer
	u := New(&out, &bytes.Buffer{})
	u.Summary(0, 5, 0, 1*time.Second)
	got := out.String()
	// Should use dash icon when applied == 0
	if !strings.Contains(got, "-") {
		t.Errorf("Summary output = %q, want to contain dash icon", got)
	}
}

func TestModuleSummary(t *testing.T) {
	old := saveColor()
	defer func() { color.Enabled = old }()
	color.Enabled = false

	var out bytes.Buffer
	u := New(&out, &bytes.Buffer{})
	u.ModuleSummary(3, 1, 0)
	got := out.String()
	if !strings.Contains(got, "3 applied") {
		t.Errorf("ModuleSummary output = %q, want to contain %q", got, "3 applied")
	}
	if !strings.Contains(got, "1 skipped") {
		t.Errorf("ModuleSummary output = %q, want to contain %q", got, "1 skipped")
	}
}

func TestTable(t *testing.T) {
	old := saveColor()
	defer func() { color.Enabled = old }()
	color.Enabled = false

	var out bytes.Buffer
	u := New(&out, &bytes.Buffer{})
	headers := []string{"NAME", "ITEMS", "TAGS"}
	rows := [][]string{
		{"nvim", "5", "dev"},
		{"git", "12", "all"},
	}
	u.Table(headers, rows, nil)
	got := out.String()
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) < 4 {
		t.Fatalf("Table output has %d lines, want at least 4:\n%s", len(lines), got)
	}
	// Header line should contain all headers
	for _, h := range headers {
		if !strings.Contains(lines[0], h) {
			t.Errorf("header line = %q, want to contain %q", lines[0], h)
		}
	}
	// Separator line
	if !strings.Contains(lines[1], "-") {
		t.Errorf("separator line = %q, want to contain dashes", lines[1])
	}
	// Data rows
	if !strings.Contains(lines[2], "nvim") {
		t.Errorf("row 1 = %q, want to contain %q", lines[2], "nvim")
	}
	if !strings.Contains(lines[3], "git") {
		t.Errorf("row 2 = %q, want to contain %q", lines[3], "git")
	}
	// Check alignment: "5" and "12" should be in the same column position
	idx1 := strings.Index(lines[2], "5")
	idx2 := strings.Index(lines[3], "12")
	if idx1 == -1 || idx2 == -1 {
		t.Fatalf("could not find data values in rows")
	}
	// Both should start at the same column (ITEMS column)
	if idx1 != idx2 {
		t.Errorf("column alignment: '5' at %d, '12' at %d", idx1, idx2)
	}
}

func TestTableEmpty(t *testing.T) {
	old := saveColor()
	defer func() { color.Enabled = old }()
	color.Enabled = false

	var out bytes.Buffer
	u := New(&out, &bytes.Buffer{})
	headers := []string{"NAME", "ITEMS"}
	u.Table(headers, nil, nil)
	got := out.String()
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("Table empty output has %d lines, want 2:\n%s", len(lines), got)
	}
	if !strings.Contains(lines[0], "NAME") {
		t.Errorf("header line = %q, want to contain %q", lines[0], "NAME")
	}
	if !strings.Contains(lines[1], "-") {
		t.Errorf("separator line = %q, want to contain dashes", lines[1])
	}
}
