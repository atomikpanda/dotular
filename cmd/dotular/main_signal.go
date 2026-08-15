package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"

	"github.com/spf13/cobra"
)

type rollbackCapableContextKey struct{}
type rollbackStartedContextKey struct{}

type signalController struct {
	cancel              context.CancelFunc
	signals             <-chan os.Signal
	stopFirst           func()
	stopSignals         func()
	hardExit            func(int)
	rollbackCapable     atomic.Bool
	rollbackStarted     chan chan struct{}
	done                chan struct{}
	finished            chan struct{}
	doneOnce            sync.Once
	rollbackStartedOnce sync.Once
	stopFirstOnce       sync.Once
	stopSignalsOnce     sync.Once
}

func newSignalController(
	cancel context.CancelFunc,
	signals <-chan os.Signal,
	stopFirst func(),
	stopSignals func(),
	hardExit func(int),
) *signalController {
	controller := &signalController{
		cancel:          cancel,
		signals:         signals,
		stopFirst:       stopFirst,
		stopSignals:     stopSignals,
		hardExit:        hardExit,
		rollbackStarted: make(chan chan struct{}),
		done:            make(chan struct{}),
		finished:        make(chan struct{}),
	}
	go controller.run()
	return controller
}

func (c *signalController) run() {
	defer close(c.finished)
	signalCount := 0
	rollbackStarted := false
	for {
		select {
		case received := <-c.signals:
			signalCount++
			if signalCount == 1 {
				c.cancel()
				c.stopFirstOnce.Do(c.stopFirst)
				continue
			}
			if !c.rollbackCapable.Load() || rollbackStarted {
				c.stopSignalsOnce.Do(c.stopSignals)
				c.hardExit(signalExitCode(received))
				return
			}
		case acknowledged := <-c.rollbackStarted:
			rollbackStarted = true
			close(acknowledged)
		case <-c.done:
			return
		}
	}
}

func (c *signalController) RollbackCapable() {
	c.rollbackCapable.Store(true)
}

func (c *signalController) RollbackStarted() {
	c.rollbackStartedOnce.Do(func() {
		acknowledged := make(chan struct{})
		select {
		case c.rollbackStarted <- acknowledged:
		case <-c.finished:
			return
		}
		select {
		case <-acknowledged:
		case <-c.finished:
		}
	})
}

func (c *signalController) Stop() {
	c.doneOnce.Do(func() { close(c.done) })
	c.cancel()
	c.stopFirstOnce.Do(c.stopFirst)
	c.stopSignalsOnce.Do(c.stopSignals)
	<-c.finished
}

func executeRoot(root *cobra.Command, hardExit func(int)) error {
	signalsToHandle := terminationSignals()
	firstSignalCtx, stopFirst := signal.NotifyContext(context.Background(), signalsToHandle...)
	ctx, cancel := context.WithCancel(firstSignalCtx)
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, signalsToHandle...)

	controller := newSignalController(
		cancel,
		signals,
		stopFirst,
		func() { signal.Stop(signals) },
		hardExit,
	)
	defer controller.Stop()

	ctx = context.WithValue(ctx, rollbackCapableContextKey{}, controller.RollbackCapable)
	ctx = context.WithValue(ctx, rollbackStartedContextKey{}, controller.RollbackStarted)
	return root.ExecuteContext(ctx)
}

func markRollbackCapableFromContext(ctx context.Context) {
	if capable, _ := ctx.Value(rollbackCapableContextKey{}).(func()); capable != nil {
		capable()
	}
}

func rollbackStartedFromContext(ctx context.Context) func() {
	started, _ := ctx.Value(rollbackStartedContextKey{}).(func())
	return started
}
