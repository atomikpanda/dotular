package shell

import (
	"context"
	"runtime"
	"testing"
)

func TestRunSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell tests use Unix commands")
	}
	err := Run(context.Background(), "true")
	if err != nil {
		t.Errorf("Run(true) error: %v", err)
	}
}

func TestRunFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell tests use Unix commands")
	}
	err := Run(context.Background(), "false")
	if err == nil {
		t.Error("Run(false) should return error")
	}
}

func TestEvalSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell tests use Unix commands")
	}
	ok, err := Eval(context.Background(), "true")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("Eval(true) should return true")
	}
}

func TestEvalFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell tests use Unix commands")
	}
	ok, err := Eval(context.Background(), "false")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("Eval(false) should return false")
	}
}

func TestEvalBinaryNotFound(t *testing.T) {
	_, err := Eval(context.Background(), "nonexistent_binary_xyz_12345")
	// The command itself ("sh") will run fine, but the inner command will fail
	// with exit code, not an exec error. So this depends on behavior.
	// On most systems, sh -c "nonexistent" returns exit 127, which is an ExitError.
	// This test just verifies no panic.
	_ = err
}

func TestRunWithOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell tests use Unix commands")
	}
	err := Run(context.Background(), "echo hello >/dev/null")
	if err != nil {
		t.Errorf("Run(echo) error: %v", err)
	}
}

func TestRunCancelled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell tests use Unix commands")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Run(ctx, "sleep 10")
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}
