package actions

import (
	"context"
	"runtime"
	"testing"
)

func TestRunActionDescribe(t *testing.T) {
	a := &RunAction{Command: "echo hello"}
	got := a.Describe()
	if got != `run "echo hello"` {
		t.Errorf("Describe() = %q", got)
	}
}

func TestRunActionDescribeWithAfter(t *testing.T) {
	a := &RunAction{Command: "echo hello", After: "package"}
	got := a.Describe()
	if got != `run "echo hello" (after package)` {
		t.Errorf("Describe() = %q", got)
	}
}

func TestRunActionDryRun(t *testing.T) {
	a := &RunAction{Command: "echo hello"}
	if err := a.Run(context.Background(), true); err != nil {
		t.Errorf("dry run error: %v", err)
	}
}

func TestRunActionRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}
	a := &RunAction{Command: "true"}
	if err := a.Run(context.Background(), false); err != nil {
		t.Errorf("Run(true) error: %v", err)
	}
}

func TestRunActionRunFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}
	a := &RunAction{Command: "false"}
	if err := a.Run(context.Background(), false); err == nil {
		t.Error("expected error from Run(false)")
	}
}
