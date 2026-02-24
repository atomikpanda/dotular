// Package color provides ANSI colour helpers for terminal output.
// All functions are no-ops when Enabled is false, so callers need not
// guard their output — just call Init once at program start.
package color

import "os"

// Enabled is true when ANSI colour output is supported.
// Call Init once at program start to auto-detect the capability.
var Enabled bool

// Init detects whether os.Stdout is a colour-capable terminal and sets Enabled.
// Colour is suppressed when:
//   - NO_COLOR env var is set (https://no-color.org)
//   - TERM=dumb
//   - stdout is not a character device (piped, redirected, etc.)
func Init() {
	if os.Getenv("NO_COLOR") != "" {
		return
	}
	if os.Getenv("TERM") == "dumb" {
		return
	}
	stat, err := os.Stdout.Stat()
	if err != nil {
		return
	}
	Enabled = stat.Mode()&os.ModeCharDevice != 0
}

func seq(code, s string) string {
	if !Enabled || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func Bold(s string) string       { return seq("1", s) }
func Dim(s string) string        { return seq("2", s) }
func Red(s string) string        { return seq("31", s) }
func Green(s string) string      { return seq("32", s) }
func Yellow(s string) string     { return seq("33", s) }
func Cyan(s string) string       { return seq("36", s) }
func BoldRed(s string) string    { return seq("1;31", s) }
func BoldGreen(s string) string  { return seq("1;32", s) }
func BoldYellow(s string) string { return seq("1;33", s) }
func BoldCyan(s string) string   { return seq("1;36", s) }
