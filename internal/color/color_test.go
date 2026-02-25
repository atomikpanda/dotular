package color

import (
	"os"
	"testing"
)

func TestColorDisabled(t *testing.T) {
	Enabled = false
	if got := Bold("hello"); got != "hello" {
		t.Errorf("Bold() with Enabled=false = %q, want %q", got, "hello")
	}
	if got := Red("test"); got != "test" {
		t.Errorf("Red() with Enabled=false = %q", got)
	}
	if got := Dim("dim"); got != "dim" {
		t.Errorf("Dim() with Enabled=false = %q", got)
	}
}

func TestColorEnabled(t *testing.T) {
	Enabled = true
	defer func() { Enabled = false }()

	if got := Bold("hello"); got != "\x1b[1mhello\x1b[0m" {
		t.Errorf("Bold() = %q", got)
	}
	if got := Red("r"); got != "\x1b[31mr\x1b[0m" {
		t.Errorf("Red() = %q", got)
	}
	if got := Green("g"); got != "\x1b[32mg\x1b[0m" {
		t.Errorf("Green() = %q", got)
	}
	if got := Yellow("y"); got != "\x1b[33my\x1b[0m" {
		t.Errorf("Yellow() = %q", got)
	}
	if got := Cyan("c"); got != "\x1b[36mc\x1b[0m" {
		t.Errorf("Cyan() = %q", got)
	}
	if got := Dim("d"); got != "\x1b[2md\x1b[0m" {
		t.Errorf("Dim() = %q", got)
	}
	if got := BoldRed("br"); got != "\x1b[1;31mbr\x1b[0m" {
		t.Errorf("BoldRed() = %q", got)
	}
	if got := BoldGreen("bg"); got != "\x1b[1;32mbg\x1b[0m" {
		t.Errorf("BoldGreen() = %q", got)
	}
	if got := BoldYellow("by"); got != "\x1b[1;33mby\x1b[0m" {
		t.Errorf("BoldYellow() = %q", got)
	}
	if got := BoldCyan("bc"); got != "\x1b[1;36mbc\x1b[0m" {
		t.Errorf("BoldCyan() = %q", got)
	}
}

func TestColorEmptyString(t *testing.T) {
	Enabled = true
	defer func() { Enabled = false }()

	if got := Bold(""); got != "" {
		t.Errorf("Bold('') = %q, want empty", got)
	}
}

func TestInitNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	Enabled = false
	Init()
	// NO_COLOR causes Init() to return early without enabling color.
	if Enabled {
		t.Error("Init() should not enable color when NO_COLOR is set")
	}
}

func TestInitTermDumb(t *testing.T) {
	os.Unsetenv("NO_COLOR")
	t.Setenv("TERM", "dumb")
	Enabled = false
	Init()
	// TERM=dumb causes Init() to return early without enabling color.
	if Enabled {
		t.Error("Init() should not enable color when TERM=dumb")
	}
}

func TestInitNormal(t *testing.T) {
	os.Unsetenv("NO_COLOR")
	t.Setenv("TERM", "xterm")
	Enabled = false
	Init()
	// In test environments stdout is usually piped, so Enabled should be false.
	// We just verify Init() doesn't panic.
}
