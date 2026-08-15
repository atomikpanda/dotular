package runner

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atomikpanda/dotular/internal/actions"
)

type testCompensation struct {
	description string
	run         func(context.Context) error
}

func (c testCompensation) Describe() string {
	return c.description
}

func (c testCompensation) Run(ctx context.Context) error {
	return c.run(ctx)
}

type testSnapshot struct {
	restore func() error
	discard func() error
}

func (s testSnapshot) Restore() error {
	return s.restore()
}

func (s testSnapshot) Discard() error {
	return s.discard()
}

func activateTestEntry(t *testing.T, transaction *moduleTransaction, identity operationIdentity, compensation, fallback actions.Compensation) *journalEntry {
	t.Helper()
	entry, err := transaction.activate(journalEntry{
		identity:     identity,
		compensation: compensation,
		fallback:     fallback,
	})
	if err != nil {
		t.Fatalf("activate() error = %v", err)
	}
	return entry
}

func recordingCompensation(order *[]string, name string, err error) testCompensation {
	return testCompensation{
		description: name,
		run: func(context.Context) error {
			*order = append(*order, name)
			return err
		},
	}
}

func TestModuleTransactionRollsBackEveryAttemptInLIFOOrder(t *testing.T) {
	var order []string
	transaction := newModuleTransactionWithSnapshot(testSnapshot{
		restore: func() error {
			order = append(order, "1")
			return nil
		},
		discard: func() error { return nil },
	})

	for i, operation := range []string{
		"module before_apply",
		"item before_apply",
		"item action",
		"item after_apply",
		"module after_apply",
	} {
		name := string(rune('2' + i))
		activateTestEntry(t, transaction, operationIdentity{
			scope:     "module",
			target:    "example",
			operation: operation,
		}, recordingCompensation(&order, name, nil), nil)
	}

	forwardErr := errors.New("module after_apply failed")
	report := transaction.rollback(context.Background(), forwardErr)

	if want := []string{"6", "5", "4", "3", "2", "1"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("rollback order = %v, want %v", order, want)
	}
	if len(report.results) != 6 {
		t.Fatalf("len(results) = %d, want 6", len(report.results))
	}
	for i, result := range report.results {
		if result.outcome != rollbackOutcomeRolledBack {
			t.Errorf("result %d outcome = %q, want %q", i, result.outcome, rollbackOutcomeRolledBack)
		}
	}
	if !errors.Is(report.err, forwardErr) {
		t.Errorf("rollback error %v does not contain forward error", report.err)
	}
}

func TestModuleTransactionContinuesAfterFailuresAndJoinsEveryError(t *testing.T) {
	var order []string
	firstErr := errors.New("first compensation failed")
	middleErr := errors.New("middle compensation failed")
	lastErr := errors.New("last compensation failed")
	attempts := make(map[string]int)
	transaction := newModuleTransaction()
	for _, step := range []struct {
		name string
		err  error
	}{
		{name: "first", err: firstErr},
		{name: "middle", err: middleErr},
		{name: "last", err: lastErr},
	} {
		step := step
		activateTestEntry(t, transaction, operationIdentity{scope: "item", target: step.name, operation: "action"}, testCompensation{
			description: step.name,
			run: func(context.Context) error {
				attempts[step.name]++
				order = append(order, step.name)
				return step.err
			},
		}, nil)
	}

	report := transaction.rollback(context.Background(), nil)

	if want := []string{"last", "middle", "first"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("rollback order = %v, want %v", order, want)
	}
	for _, wantErr := range []error{firstErr, middleErr, lastErr} {
		if !errors.Is(report.err, wantErr) {
			t.Errorf("rollback error %v does not contain %v", report.err, wantErr)
		}
	}
	for name, count := range attempts {
		if count != 1 {
			t.Errorf("%s attempts = %d, want 1", name, count)
		}
	}
}

func TestModuleTransactionUsesExplicitRollbackOnlyWhenTypedCompensationUnavailable(t *testing.T) {
	t.Run("typed unavailable uses explicit", func(t *testing.T) {
		var order []string
		transaction := newModuleTransaction()
		activateTestEntry(t, transaction, operationIdentity{scope: "item", target: "package", operation: "action"}, nil, recordingCompensation(&order, "explicit", nil))

		report := transaction.rollback(context.Background(), nil)

		if want := []string{"explicit"}; !reflect.DeepEqual(order, want) {
			t.Fatalf("rollback order = %v, want %v", order, want)
		}
		if report.results[0].outcome != rollbackOutcomeRolledBack {
			t.Fatalf("outcome = %q, want %q", report.results[0].outcome, rollbackOutcomeRolledBack)
		}
	})

	t.Run("typed failure does not use explicit", func(t *testing.T) {
		var order []string
		typedErr := errors.New("typed compensation failed")
		transaction := newModuleTransaction()
		activateTestEntry(t, transaction, operationIdentity{scope: "item", target: "package", operation: "action"}, recordingCompensation(&order, "typed", typedErr), recordingCompensation(&order, "explicit", nil))

		report := transaction.rollback(context.Background(), nil)

		if want := []string{"typed"}; !reflect.DeepEqual(order, want) {
			t.Fatalf("rollback order = %v, want %v", order, want)
		}
		if !errors.Is(report.err, typedErr) {
			t.Errorf("rollback error %v does not contain typed error", report.err)
		}
	})
}

func TestModuleTransactionReportsUncompensatedEntry(t *testing.T) {
	transaction := newModuleTransaction()
	activateTestEntry(t, transaction, operationIdentity{scope: "item", target: "run command", operation: "action"}, nil, nil)

	report := transaction.rollback(context.Background(), nil)

	if len(report.results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(report.results))
	}
	if report.results[0].outcome != rollbackOutcomeUncompensated {
		t.Fatalf("outcome = %q, want %q", report.results[0].outcome, rollbackOutcomeUncompensated)
	}
}

func TestModuleTransactionDoesNotExecuteOrCountDeactivatedEntry(t *testing.T) {
	var order []string
	transaction := newModuleTransaction()
	activateTestEntry(t, transaction, operationIdentity{scope: "item", target: "attempted", operation: "action"}, recordingCompensation(&order, "attempted", nil), nil)
	skipped := activateTestEntry(t, transaction, operationIdentity{scope: "item", target: "skipped", operation: "action"}, recordingCompensation(&order, "skipped", nil), nil)
	if err := transaction.deactivate(skipped); err != nil {
		t.Fatalf("deactivate() error = %v", err)
	}

	report := transaction.rollback(context.Background(), nil)

	if want := []string{"attempted"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("rollback order = %v, want %v", order, want)
	}
	if len(report.results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(report.results))
	}
}

type controlledDeadlineContext struct {
	context.Context
	done    chan struct{}
	expired atomic.Bool
}

func newControlledDeadlineContext() *controlledDeadlineContext {
	return &controlledDeadlineContext{Context: context.Background(), done: make(chan struct{})}
}

func (c *controlledDeadlineContext) Deadline() (time.Time, bool) {
	return time.Time{}, false
}

func (c *controlledDeadlineContext) Done() <-chan struct{} {
	return c.done
}

func (c *controlledDeadlineContext) Err() error {
	if c.expired.Load() {
		return context.DeadlineExceeded
	}
	return nil
}

func (c *controlledDeadlineContext) expire() {
	c.expired.Store(true)
	close(c.done)
}

func TestModuleTransactionMarksDeadlineBlockedEntriesAndStillRestoresSnapshot(t *testing.T) {
	ctx := newControlledDeadlineContext()
	var order []string
	transaction := newModuleTransactionWithSnapshot(testSnapshot{
		restore: func() error {
			order = append(order, "snapshot")
			return nil
		},
		discard: func() error { return nil },
	})
	activateTestEntry(t, transaction, operationIdentity{scope: "item", target: "oldest", operation: "action"}, recordingCompensation(&order, "oldest", nil), nil)
	activateTestEntry(t, transaction, operationIdentity{scope: "item", target: "middle", operation: "action"}, recordingCompensation(&order, "middle", nil), nil)
	activateTestEntry(t, transaction, operationIdentity{scope: "item", target: "deadline", operation: "action"}, testCompensation{
		description: "deadline",
		run: func(context.Context) error {
			order = append(order, "deadline")
			ctx.expire()
			return nil
		},
	}, nil)

	report := transaction.rollback(ctx, nil)

	if want := []string{"deadline", "snapshot"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("rollback order = %v, want %v", order, want)
	}
	if want := []string{rollbackOutcomeRolledBack, rollbackOutcomeFailed, rollbackOutcomeFailed, rollbackOutcomeRolledBack}; !reflect.DeepEqual(resultOutcomes(report.results), want) {
		t.Fatalf("outcomes = %v, want %v", resultOutcomes(report.results), want)
	}
	for _, result := range report.results[1:3] {
		if !errors.Is(result.err, context.DeadlineExceeded) {
			t.Errorf("blocked result error %v does not contain deadline", result.err)
		}
	}
	if !errors.Is(report.err, context.DeadlineExceeded) {
		t.Errorf("rollback error %v does not contain deadline", report.err)
	}
}

func resultOutcomes(results []rollbackResult) []string {
	outcomes := make([]string, len(results))
	for i, result := range results {
		outcomes[i] = result.outcome
	}
	return outcomes
}

func TestModuleTransactionKeepsSnapshotRestoreAndDiscardFailuresSeparateAndJoined(t *testing.T) {
	var order []string
	restoreErr := errors.New("restore failed")
	discardErr := errors.New("discard failed")
	transaction := newModuleTransactionWithSnapshot(testSnapshot{
		restore: func() error {
			order = append(order, "restore")
			return restoreErr
		},
		discard: func() error {
			order = append(order, "discard")
			return discardErr
		},
	})

	report := transaction.rollback(context.Background(), nil)

	if want := []string{"restore", "discard"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("cleanup order = %v, want %v", order, want)
	}
	if !errors.Is(report.results[0].err, restoreErr) {
		t.Errorf("snapshot result error %v does not contain restore error", report.results[0].err)
	}
	if !errors.Is(report.cleanupErr, discardErr) {
		t.Errorf("cleanup error %v does not contain discard error", report.cleanupErr)
	}
	for _, wantErr := range []error{restoreErr, discardErr} {
		if !errors.Is(report.err, wantErr) {
			t.Errorf("rollback error %v does not contain %v", report.err, wantErr)
		}
	}
}

func TestModuleTransactionRollbackRunsOnlyOnceWhenCalledConcurrently(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var attempts atomic.Int32
	transaction := newModuleTransaction()
	activateTestEntry(t, transaction, operationIdentity{scope: "item", target: "one", operation: "action"}, testCompensation{
		description: "one",
		run: func(context.Context) error {
			attempts.Add(1)
			close(started)
			<-release
			return nil
		},
	}, nil)

	var reports [2]rollbackReport
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		reports[0] = transaction.rollback(context.Background(), nil)
	}()
	<-started
	wg.Add(1)
	go func() {
		defer wg.Done()
		reports[1] = transaction.rollback(context.Background(), nil)
	}()
	close(release)
	wg.Wait()

	if got := attempts.Load(); got != 1 {
		t.Fatalf("compensation attempts = %d, want 1", got)
	}
	for i, report := range reports {
		if len(report.results) != 1 || report.results[0].outcome != rollbackOutcomeRolledBack {
			t.Errorf("report %d = %+v, want one rolled_back result", i, report)
		}
	}
}

func TestModuleTransactionRecoversRollbackPanicsAndContinues(t *testing.T) {
	forwardErr := errors.New("forward failed")
	compensationPanic := errors.New("compensation panicked")
	restorePanic := errors.New("snapshot restore panicked")
	var order []string
	discarded := false
	transaction := newModuleTransactionWithSnapshot(testSnapshot{
		restore: func() error {
			order = append(order, "restore")
			panic(restorePanic)
		},
		discard: func() error {
			order = append(order, "discard")
			discarded = true
			return nil
		},
	})
	activateTestEntry(
		t,
		transaction,
		operationIdentity{scope: "item", target: "earlier", operation: "action"},
		recordingCompensation(&order, "earlier", nil),
		nil,
	)
	activateTestEntry(
		t,
		transaction,
		operationIdentity{scope: "item", target: "panicking", operation: "action"},
		testCompensation{
			description: "panicking compensation",
			run: func(context.Context) error {
				order = append(order, "panicking")
				panic(compensationPanic)
			},
		},
		nil,
	)

	report := transaction.rollback(context.Background(), forwardErr)

	wantOrder := []string{"panicking", "earlier", "restore", "discard"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("rollback order = %v, want %v", order, wantOrder)
	}
	if !discarded {
		t.Fatal("snapshot was not discarded after rollback panics")
	}
	if len(report.results) != 3 {
		t.Fatalf("len(results) = %d, want panicking, earlier, and snapshot results", len(report.results))
	}
	if report.results[0].outcome != rollbackOutcomeFailed ||
		!errors.Is(report.results[0].err, compensationPanic) {
		t.Fatalf("panicking compensation result = %+v", report.results[0])
	}
	if report.results[1].outcome != rollbackOutcomeRolledBack {
		t.Fatalf("earlier compensation result = %+v", report.results[1])
	}
	if report.results[2].outcome != rollbackOutcomeFailed ||
		!errors.Is(report.results[2].err, restorePanic) {
		t.Fatalf("snapshot panic result = %+v", report.results[2])
	}
	for _, wantErr := range []error{forwardErr, compensationPanic, restorePanic} {
		if !errors.Is(report.err, wantErr) {
			t.Fatalf("rollback error %v does not contain %v", report.err, wantErr)
		}
	}
}

type describePanicCompensation struct {
	panicValue error
	order      *[]string
}

func (c describePanicCompensation) Describe() string {
	*c.order = append(*c.order, "describe")
	panic(c.panicValue)
}

func (c describePanicCompensation) Run(context.Context) error {
	*c.order = append(*c.order, "run")
	return nil
}

func TestModuleTransactionDescribePanicStillRunsCompensationAndContinues(t *testing.T) {
	describePanic := errors.New("describe panicked")
	var order []string
	transaction := newModuleTransactionWithSnapshot(testSnapshot{
		restore: func() error {
			order = append(order, "restore")
			return nil
		},
		discard: func() error {
			order = append(order, "discard")
			return nil
		},
	})
	activateTestEntry(
		t,
		transaction,
		operationIdentity{scope: "item", target: "earlier", operation: "action"},
		recordingCompensation(&order, "earlier", nil),
		nil,
	)
	activateTestEntry(
		t,
		transaction,
		operationIdentity{scope: "item", target: "describe panic", operation: "action"},
		describePanicCompensation{panicValue: describePanic, order: &order},
		nil,
	)

	report := transaction.rollback(context.Background(), nil)

	wantOrder := []string{"describe", "run", "earlier", "restore", "discard"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("rollback order = %v, want %v", order, wantOrder)
	}
	if len(report.results) != 3 {
		t.Fatalf("len(results) = %d, want describe-panic, earlier, and snapshot", len(report.results))
	}
	describeResult := report.results[0]
	if describeResult.outcome != rollbackOutcomeFailed {
		t.Fatalf("describe-panic outcome = %q, want rollback_failed", describeResult.outcome)
	}
	if describeResult.compensation == "" {
		t.Fatal("describe-panic result has no placeholder compensation description")
	}
	if !errors.Is(describeResult.err, describePanic) || !errors.Is(report.err, describePanic) {
		t.Fatalf("describe panic missing from result/report: result=%v report=%v", describeResult.err, report.err)
	}
	if report.results[1].outcome != rollbackOutcomeRolledBack ||
		report.results[2].outcome != rollbackOutcomeRolledBack {
		t.Fatalf("later restoration did not continue: %+v", report.results)
	}
}
