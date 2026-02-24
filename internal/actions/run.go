package actions

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/atomikpanda/dotular/internal/color"
)

// RunAction executes an inline shell command declared directly in the module.
// It is semantically equivalent to ScriptAction with via=local, but the
// command is written inline rather than as a script file path.
//
// The After field records which item type this step logically follows. It is
// informational only in v1 — ordering is determined by the item's position in
// the items list. Module authors should place run items after their
// dependencies in the declaration order.
//
// Idempotency: RunAction does not implement Idempotent. Use skip_if for
// custom guards.
type RunAction struct {
	Command string
	After   string // informational dependency annotation
}

func (a *RunAction) Describe() string {
	after := ""
	if a.After != "" {
		after = fmt.Sprintf(" (after %s)", a.After)
	}
	return fmt.Sprintf("run %q%s", a.Command, after)
}

func (a *RunAction) Run(ctx context.Context, dryRun bool) error {
	if dryRun {
		fmt.Printf("    %s\n", color.Dim("[dry-run] "+a.Describe()))
		return nil
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "powershell", "-Command", a.Command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", a.Command)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
