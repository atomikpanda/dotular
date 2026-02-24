package actions

import "context"

// Action is a single executable step produced from a config item.
type Action interface {
	// Describe returns a human-readable summary of the action.
	Describe() string
	// Run executes the action. When dryRun is true it only prints what would happen.
	Run(ctx context.Context, dryRun bool) error
}

// Idempotent is optionally implemented by actions that can self-check whether
// they have already been applied. The runner uses this for automatic skip logic.
//
// Idempotency contracts per action type:
//   - PackageAction: queries the package manager to determine whether the
//     package is already installed. Guaranteed to be side-effect free.
//   - FileAction (link): checks that the symlink at the destination already
//     exists and resolves to the correct absolute source path.
//   - FileAction (push/pull/sync), ScriptAction, SettingAction: do not
//     implement Idempotent; use skip_if for custom idempotency guards.
type Idempotent interface {
	// IsApplied returns true when the action's desired state is already in
	// place and the action can safely be skipped.
	IsApplied(ctx context.Context) (bool, error)
}
