package ui

import (
	"fmt"
	"io"
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
