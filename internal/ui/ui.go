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
