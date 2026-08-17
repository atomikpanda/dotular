package procutil

import (
	"context"
	"os/exec"
)

// CommandContext creates a command whose cancellation terminates its OS process
// group or job. On Unix, a child can deliberately escape by starting a new session.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	configureCommand(cmd)
	return cmd
}
