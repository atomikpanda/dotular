package actions

import (
	"context"
	"errors"
)

// ErrSkipped is returned by an action's Run method when the action cannot
// proceed but the failure is not an error (e.g. pulling a file that does not
// exist on the system). The runner treats this as a skip rather than a failure.
var ErrSkipped = errors.New("skipped")

// Action is a single executable step produced from a config item.
type Action interface {
	// Describe returns a human-readable summary of the action.
	Describe() string
	// Run executes the action. When dryRun is true it only prints what would happen.
	Run(ctx context.Context, dryRun bool) error
}

// CompensationPreparer is optionally implemented by actions whose pre-state
// can be captured as a typed rollback before the action runs.
type CompensationPreparer interface {
	PrepareCompensation(ctx context.Context) (CompensationPreparation, error)
}

// Idempotent is optionally implemented by actions that can self-check whether
// they have already been applied. The runner uses this for automatic skip logic.
//
// Idempotency contracts per action type:
//   - PackageAction: queries the package manager to determine whether the
//     package is already installed. Guaranteed to be side-effect free.
//   - FileAction (link): checks that the symlink at the destination already
//     exists and resolves to the correct absolute source path.
//   - DirectoryAction (link): same check against the destination directory
//     symlink.
//   - FileAction and DirectoryAction (push/pull/sync): implement the
//     interface but always report false — only link items self-check.
//   - ScriptAction, SettingAction, BinaryAction, RunAction: do not implement
//     Idempotent at all; use skip_if for custom idempotency guards.
type Idempotent interface {
	// IsApplied returns true when the action's desired state is already in
	// place and the action can safely be skipped.
	IsApplied(ctx context.Context) (bool, error)
}

// DirectionAware is optionally implemented by actions that move data between the
// repo and the machine, and can therefore say which way they move it.
//
// The runner uses it as the eligibility test for the pull and sync verbs: both
// reconcile the repo against the machine, so an action that is not
// DirectionAware has nothing to reconcile and is skipped rather than run.
// Without it, `dotular pull` installed packages, executed scripts, downloaded
// binaries and wrote system settings.
//
// This is deliberately not PathWriter, and the two must not be merged.
// PathWriter answers "which paths may I overwrite", which is a different
// question: BinaryAction overwrites its install target and so should implement
// PathWriter for snapshot coverage — the moment it does, testing for PathWriter
// here would silently make binary items pull-eligible again.
type DirectionAware interface {
	// EffectiveDirection returns the direction the action will actually operate
	// in: "push", "pull", or "sync".
	EffectiveDirection() string
}

// PathWriter is optionally implemented by actions that overwrite existing paths.
// The runner snapshots those paths before running the action so a later failure
// in the module can be rolled back. The action declares them rather than the
// runner guessing, because which side of a file or directory item gets written
// depends on its direction.
type PathWriter interface {
	// WritePaths returns every path Run may create or overwrite.
	WritePaths() []string
}
