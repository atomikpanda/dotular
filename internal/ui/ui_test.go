package ui

import (
	"bytes"
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
