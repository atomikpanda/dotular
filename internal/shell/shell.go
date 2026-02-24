// Package shell provides helpers for evaluating user-supplied shell commands
// (skip_if, verify, hooks).
package shell

import (
	"context"
	"os/exec"
	"runtime"
)

// Run executes command in a shell and returns an error if the exit code is non-zero.
func Run(ctx context.Context, command string) error {
	cmd := shellCmd(ctx, command)
	return cmd.Run()
}

// Eval executes command and returns true when it exits 0 (success).
// A non-zero exit is not treated as a Go error; only execution failures are.
func Eval(ctx context.Context, command string) (exitsZero bool, err error) {
	cmd := shellCmd(ctx, command)
	runErr := cmd.Run()
	if runErr == nil {
		return true, nil
	}
	if _, ok := runErr.(*exec.ExitError); ok {
		return false, nil // non-zero exit is expected and not an error
	}
	return false, runErr // real execution failure (binary not found, etc.)
}

func shellCmd(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "powershell", "-Command", command)
	}
	return exec.CommandContext(ctx, "sh", "-c", command)
}
