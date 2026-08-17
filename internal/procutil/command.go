package procutil

import (
	"context"
	"os/exec"
)

// CommandContext creates a command whose cancellation terminates its process tree.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	configureCommand(cmd)
	return cmd
}
