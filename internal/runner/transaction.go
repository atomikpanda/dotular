package runner

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/atomikpanda/dotular/internal/actions"
	"github.com/atomikpanda/dotular/internal/snapshot"
)

const (
	rollbackOutcomeRolledBack    = "rolled_back"
	rollbackOutcomeFailed        = "rollback_failed"
	rollbackOutcomeUncompensated = "uncompensated"
)

var (
	errTransactionCommitted  = errors.New("module transaction already committed")
	errTransactionRolledBack = errors.New("module transaction already rolled back")
	errUnknownJournalEntry   = errors.New("journal entry does not belong to module transaction")
)

type operationIdentity struct {
	scope     string
	target    string
	operation string
}

type journalEntry struct {
	identity          operationIdentity
	compensation      actions.Compensation
	fallback          actions.Compensation
	unavailableReason string
	active            bool
	contextFree       bool
}

type rollbackResult struct {
	identity     operationIdentity
	compensation string
	reason       string
	outcome      string
	err          error
}

type rollbackReport struct {
	results    []rollbackResult
	cleanupErr error
	err        error
}

type transactionState uint8

const (
	transactionOpen transactionState = iota
	transactionRolledBack
	transactionCommitted
)

type moduleSnapshot interface {
	Restore() error
	Discard() error
}

type snapshotRecorder interface {
	Record(string) error
}

type moduleTransaction struct {
	mu        sync.Mutex
	entries   []*journalEntry
	snapshot  moduleSnapshot
	state     transactionState
	report    rollbackReport
	commitErr error
}

type snapshotCompensation struct {
	snapshot moduleSnapshot
}

func (c snapshotCompensation) Describe() string {
	return "restore filesystem snapshot"
}

func (c snapshotCompensation) Run(context.Context) error {
	return c.snapshot.Restore()
}

func newModuleTransaction() *moduleTransaction {
	return &moduleTransaction{}
}

// captureModuleTransaction captures the module filesystem snapshot and makes
// its restore the oldest journal entry before any forward operation can run.
func captureModuleTransaction() (*moduleTransaction, snapshotRecorder, error) {
	snap, err := snapshot.New()
	if err != nil {
		return nil, nil, err
	}
	return newModuleTransactionWithSnapshot(snap), snap, nil
}

func newModuleTransactionWithSnapshot(snap moduleSnapshot) *moduleTransaction {
	transaction := newModuleTransaction()
	transaction.snapshot = snap
	transaction.entries = append(transaction.entries, &journalEntry{
		identity: operationIdentity{
			scope:     "module",
			target:    "filesystem",
			operation: "snapshot restore",
		},
		compensation: snapshotCompensation{snapshot: snap},
		active:       true,
		contextFree:  true,
	})
	return transaction
}

// activate appends an active entry immediately before its forward operation is
// attempted. fallback is considered only when compensation is unavailable.
func (t *moduleTransaction) activate(entry journalEntry) (*journalEntry, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	switch t.state {
	case transactionCommitted:
		return nil, errTransactionCommitted
	case transactionRolledBack:
		return nil, errTransactionRolledBack
	}

	entry.active = true
	owned := &entry
	t.entries = append(t.entries, owned)
	return owned, nil
}

// deactivate removes a registered attempt that was proven skipped without any
// side effect. The entry remains in the journal so existing handles stay valid.
func (t *moduleTransaction) deactivate(entry *journalEntry) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	switch t.state {
	case transactionCommitted:
		return errTransactionCommitted
	case transactionRolledBack:
		return errTransactionRolledBack
	}
	for _, candidate := range t.entries {
		if candidate == entry {
			candidate.active = false
			return nil
		}
	}
	return errUnknownJournalEntry
}

// rollback executes the active journal once in reverse activation order. A
// snapshot restore and discard are context-free so cleanup is still attempted
// after the caller-supplied command cleanup context expires.
func (t *moduleTransaction) rollback(ctx context.Context, cause error) rollbackReport {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.state == transactionRolledBack {
		return cloneRollbackReport(t.report)
	}
	if t.state == transactionCommitted {
		return rollbackReport{err: errors.Join(cause, errTransactionCommitted)}
	}

	var failures []error
	if cause != nil {
		failures = append(failures, cause)
	}
	report := rollbackReport{}
	for i := len(t.entries) - 1; i >= 0; i-- {
		entry := t.entries[i]
		if !entry.active {
			continue
		}

		result := rollbackResult{identity: entry.identity}
		compensation := entry.compensation
		if compensation == nil {
			compensation = entry.fallback
		}
		if compensation == nil {
			result.outcome = rollbackOutcomeUncompensated
			result.reason = entry.unavailableReason
			report.results = append(report.results, result)
			continue
		}

		if !entry.contextFree {
			if deadlineErr := ctx.Err(); deadlineErr != nil {
				result.outcome = rollbackOutcomeFailed
				result.err = fmt.Errorf("rollback %s %q %s: %w", entry.identity.scope, entry.identity.target, entry.identity.operation, deadlineErr)
				report.results = append(report.results, result)
				failures = append(failures, result.err)
				continue
			}
		}

		description, compensationErr := runCompensation(ctx, compensation)
		result.compensation = description
		if compensationErr != nil {
			result.outcome = rollbackOutcomeFailed
			result.err = fmt.Errorf("rollback %s %q %s with %q: %w", entry.identity.scope, entry.identity.target, entry.identity.operation, result.compensation, compensationErr)
			failures = append(failures, result.err)
		} else {
			result.outcome = rollbackOutcomeRolledBack
		}
		report.results = append(report.results, result)
	}

	if t.snapshot != nil {
		if err := discardModuleSnapshot(t.snapshot); err != nil {
			report.cleanupErr = fmt.Errorf("discard filesystem snapshot: %w", err)
			failures = append(failures, report.cleanupErr)
		}
	}
	report.err = errors.Join(failures...)
	t.report = cloneRollbackReport(report)
	t.state = transactionRolledBack
	return cloneRollbackReport(report)
}

// commit closes the journal before discarding its snapshot. A discard failure
// does not reopen the transaction or permit a rollback of committed work.
func (t *moduleTransaction) commit() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	switch t.state {
	case transactionRolledBack:
		return errTransactionRolledBack
	case transactionCommitted:
		return t.commitErr
	}

	t.state = transactionCommitted
	snapshot := t.snapshot
	t.snapshot = nil
	t.entries = nil
	if snapshot != nil {
		if err := discardModuleSnapshot(snapshot); err != nil {
			t.commitErr = fmt.Errorf("discard committed filesystem snapshot: %w", err)
		}
	}
	return t.commitErr
}

func runCompensation(
	ctx context.Context,
	compensation actions.Compensation,
) (string, error) {
	description, descriptionErr := describeCompensation(compensation)
	runErr := executeCompensation(ctx, compensation)
	return description, errors.Join(descriptionErr, runErr)
}

func describeCompensation(compensation actions.Compensation) (
	description string,
	err error,
) {
	description = "compensation (description unavailable)"
	defer func() {
		if panicValue := recover(); panicValue != nil {
			err = recoveredPanicError("rollback compensation description", panicValue)
		}
	}()
	description = compensation.Describe()
	return description, nil
}

func executeCompensation(ctx context.Context, compensation actions.Compensation) (err error) {
	defer func() {
		if panicValue := recover(); panicValue != nil {
			err = recoveredPanicError("rollback compensation", panicValue)
		}
	}()
	return compensation.Run(ctx)
}

func discardModuleSnapshot(snap moduleSnapshot) (err error) {
	defer func() {
		if panicValue := recover(); panicValue != nil {
			err = recoveredPanicError("snapshot discard", panicValue)
		}
	}()
	return snap.Discard()
}

func recoveredPanicError(operation string, panicValue any) error {
	if err, ok := panicValue.(error); ok {
		return fmt.Errorf("%s panicked: %w", operation, err)
	}
	return fmt.Errorf("%s panicked: %v", operation, panicValue)
}

func cloneRollbackReport(report rollbackReport) rollbackReport {
	clone := report
	clone.results = append([]rollbackResult(nil), report.results...)
	return clone
}
