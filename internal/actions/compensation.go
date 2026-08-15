package actions

import (
	"context"
	"os"
	"os/exec"
)

// Compensation reverses a successfully applied action.
type Compensation interface {
	Describe() string
	Run(context.Context) error
}

// CompensationPreparation records the action state captured before execution.
type CompensationPreparation struct {
	AlreadyApplied    bool
	Compensation      Compensation
	UnavailableReason string
}

// commandExecutor is the narrow process seam shared by typed compensations.
// Capture callers receive stdout and can inspect an exec.ExitError's stderr.
type commandExecutor func(context.Context, []string, bool) ([]byte, error)

func executeCommand(ctx context.Context, args []string, captureOutput bool) ([]byte, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	if captureOutput {
		output, err := cmd.Output()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return output, ctxErr
		}
		return output, err
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	return nil, nil
}
