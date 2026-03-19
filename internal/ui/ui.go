package ui

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/atomikpanda/dotular/internal/color"
)

// UI provides formatted terminal output for dotular commands.
type UI struct {
	Out io.Writer
	Err io.Writer
}

// New creates a UI that writes to the given output and error writers.
func New(out, err io.Writer) *UI {
	return &UI{Out: out, Err: err}
}

// syms holds a set of display symbols.
type syms struct {
	Check  string
	Cross  string
	Arrow  string
	Dash   string
	Warn   string
	Bullet string
}

// symbols returns unicode symbols when color is enabled, ASCII fallbacks otherwise.
func (u *UI) symbols() syms {
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

// formatDuration formats a duration for display.
//   - < 1s:   (42ms)
//   - 1s-60s: (3.2s)
//   - >= 60s: (2m 15s)
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("(%dms)", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("(%.1fs)", d.Seconds())
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("(%dm %ds)", m, s)
}

// Header writes a module header line to Out.
func (u *UI) Header(name string) {
	fmt.Fprintf(u.Out, "\n%s\n", color.BoldCyan("==> "+name))
}

// SkipHeader writes a dimmed skip header line to Out.
func (u *UI) SkipHeader(name, reason string) {
	fmt.Fprintf(u.Out, "\n%s\n", color.Dim("==> "+name+"  [skip: "+reason+"]"))
}

// Item writes a pending item line to Out.
func (u *UI) Item(desc string) {
	s := u.symbols()
	fmt.Fprintf(u.Out, "  %s %s\n", color.Dim(s.Arrow), desc)
}

// ItemResult writes a completed item line with duration and status to Out.
func (u *UI) ItemResult(desc string, dur time.Duration, err error) {
	s := u.symbols()
	d := color.Dim(formatDuration(dur))
	if err != nil {
		fmt.Fprintf(u.Out, "  %s %s %s\n", color.BoldRed(s.Cross), desc, d)
	} else {
		fmt.Fprintf(u.Out, "  %s %s %s\n", color.Green(s.Check), desc, d)
	}
}

// Skip writes a skipped item line to Out.
func (u *UI) Skip(reason, desc string) {
	s := u.symbols()
	fmt.Fprintf(u.Out, "  %s\n", color.Dim(s.Dash+" skip ["+reason+"] "+desc))
}

// DryRun writes a dry-run item line to Out.
func (u *UI) DryRun(desc string) {
	s := u.symbols()
	fmt.Fprintf(u.Out, "  %s\n", color.Dim(s.Arrow+" [dry-run] "+desc))
}

// Warn writes a warning message to Err.
func (u *UI) Warn(msg string) {
	s := u.symbols()
	fmt.Fprintf(u.Err, "%s\n", color.BoldYellow(s.Warn+" "+msg))
}

// Success writes a success message to Out.
func (u *UI) Success(msg string) {
	s := u.symbols()
	fmt.Fprintf(u.Out, "%s\n", color.Green(s.Check+" "+msg))
}

// Info writes a plain message to Out.
func (u *UI) Info(msg string) {
	fmt.Fprintf(u.Out, "%s\n", msg)
}

// summaryIcon returns the appropriate icon and color function for a summary line.
func (u *UI) summaryIcon(applied, failed int) (string, func(string) string) {
	s := u.symbols()
	if failed > 0 {
		return s.Cross, color.BoldRed
	}
	if applied == 0 {
		return s.Dash, color.Dim
	}
	return s.Check, color.Green
}

// Summary writes a final summary line with counts and elapsed time to Out.
func (u *UI) Summary(applied, skipped, failed int, elapsed time.Duration) {
	icon, colorFn := u.summaryIcon(applied, failed)
	body := fmt.Sprintf("%s %d applied, %d skipped, %d failed %s",
		icon, applied, skipped, failed, formatDuration(elapsed))
	fmt.Fprintf(u.Out, "\n%s\n", colorFn(body))
}

// ModuleSummary writes an indented summary line for a single module to Out.
func (u *UI) ModuleSummary(applied, skipped, failed int) {
	icon, colorFn := u.summaryIcon(applied, failed)
	body := fmt.Sprintf("%s %d applied, %d skipped, %d failed",
		icon, applied, skipped, failed)
	fmt.Fprintf(u.Out, "  %s\n", colorFn(body))
}

// formatRow formats a row of values into fixed-width columns with optional color.
func formatRow(vals []string, widths []int, colorFns []func(string) string) string {
	var b strings.Builder
	for i, v := range vals {
		if i > 0 {
			b.WriteString("  ")
		}
		cell := v
		if colorFns != nil && i < len(colorFns) && colorFns[i] != nil {
			cell = colorFns[i](v)
		}
		// Pad based on the raw value length, not the colored length.
		pad := 0
		if i < len(widths) {
			pad = widths[i] - len(v)
		}
		b.WriteString(cell)
		for j := 0; j < pad; j++ {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// Table writes a formatted table with auto-width columns to Out.
// colColors is optional; if non-nil, each function is applied to data cells in that column.
func (u *UI) Table(headers []string, rows [][]string, colColors []func(string) string) {
	s := u.symbols()

	// Compute column widths from headers and data.
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

	// Header line (bold, no column colors).
	boldFns := make([]func(string) string, len(headers))
	for i := range boldFns {
		boldFns[i] = color.Bold
	}
	fmt.Fprintln(u.Out, formatRow(headers, widths, boldFns))

	// Separator line.
	sepParts := make([]string, len(headers))
	for i, w := range widths {
		sep := ""
		for j := 0; j < w; j++ {
			sep += s.Dash
		}
		sepParts[i] = sep
	}
	dimFns := make([]func(string) string, len(headers))
	for i := range dimFns {
		dimFns[i] = color.Dim
	}
	fmt.Fprintln(u.Out, formatRow(sepParts, widths, dimFns))

	// Data rows.
	for _, row := range rows {
		fmt.Fprintln(u.Out, formatRow(row, widths, colColors))
	}
}
