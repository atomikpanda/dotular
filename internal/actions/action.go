package actions

import "context"

// Action is a single executable step produced from a config item.
type Action interface {
	// Describe returns a human-readable summary of the action.
	Describe() string
	// Run executes the action. When dryRun is true it only prints what would happen.
	Run(ctx context.Context, dryRun bool) error
}
