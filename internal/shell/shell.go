// Package shell provides helpers for evaluating user-supplied shell commands
// (skip_if, verify, hooks).
package shell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
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
	if contextErr := ctx.Err(); contextErr != nil {
		return false, contextErr
	}
	if runErr == nil {
		return true, nil
	}
	if _, ok := runErr.(*exec.ExitError); ok {
		return false, nil // non-zero exit is expected and not an error
	}
	return false, runErr // real execution failure (binary not found, etc.)
}

var errBlankCommand = errors.New("shell command must not be blank")

// Validate parses command with the current platform shell without executing
// its body.
func Validate(ctx context.Context, command string) error {
	if strings.TrimSpace(command) == "" {
		return errBlankCommand
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	cmd := shellValidationCmd(ctx, command)
	if err := cmd.Run(); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return fmt.Errorf("validate shell command: %w", err)
	}
	return nil
}

func shellValidationCmd(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		cmd := exec.CommandContext(
			ctx,
			"powershell",
			"-NoProfile",
			"-NonInteractive",
			"-Command",
			`try { [scriptblock]::Create($env:DOTULAR_VALIDATE_COMMAND) | Out-Null } catch { Write-Error $_; exit 1 }`,
		)
		cmd.Env = append(os.Environ(), "DOTULAR_VALIDATE_COMMAND="+command)
		return cmd
	}
	return exec.CommandContext(ctx, "sh", "-n", "-c", command)
}

func shellCmd(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "powershell", "-Command", command)
	}
	return exec.CommandContext(ctx, "sh", "-c", command)
}
