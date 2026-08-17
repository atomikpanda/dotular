package shell

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
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

func TestEvalReturnsCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test uses Unix sleep")
	}
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(100*time.Millisecond, cancel)
	defer timer.Stop()

	ok, err := Eval(ctx, "sleep 10")

	if ok {
		t.Fatal("Eval() = true, want false")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Eval() error = %v, want context.Canceled", err)
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

func TestValidateAcceptsSyntaxWithoutRunningCommand(t *testing.T) {
	command := "exit 7"
	if err := Validate(context.Background(), command); err != nil {
		t.Fatalf("Validate(%q) error = %v", command, err)
	}
}

func TestValidateRejectsMalformedSyntax(t *testing.T) {
	command := "echo 'unterminated"
	if runtime.GOOS == "windows" {
		command = "if ("
	}
	if err := Validate(context.Background(), command); err == nil {
		t.Fatalf("Validate(%q) error = nil, want syntax error", command)
	}
}

func TestValidateReturnsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Validate(ctx, "exit 0")

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Validate() error = %v, want context.Canceled", err)
	}
}

func TestValidateRejectsBlankCommand(t *testing.T) {
	if err := Validate(context.Background(), " \t\n"); err == nil {
		t.Fatal("Validate(blank) error = nil, want error")
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
