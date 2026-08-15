package actions

import "context"

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
