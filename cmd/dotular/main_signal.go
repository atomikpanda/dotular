package main

import (
	"context"
	"os"
	"os/signal"
	"sync"

	"github.com/spf13/cobra"
)

type signalController struct {
	cancel          context.CancelFunc
	signals         <-chan os.Signal
	stopFirst       func()
	stopSignals     func()
	hardExit        func(int)
	done            chan struct{}
	finished        chan struct{}
	doneOnce        sync.Once
	stopFirstOnce   sync.Once
	stopSignalsOnce sync.Once
}

func newSignalController(
	cancel context.CancelFunc,
	signals <-chan os.Signal,
	stopFirst func(),
	stopSignals func(),
	hardExit func(int),
) *signalController {
	controller := &signalController{
		cancel:      cancel,
		signals:     signals,
		stopFirst:   stopFirst,
		stopSignals: stopSignals,
		hardExit:    hardExit,
		done:        make(chan struct{}),
		finished:    make(chan struct{}),
	}
	go controller.run()
	return controller
}

func (c *signalController) run() {
	defer close(c.finished)
	signalCount := 0
	for {
		select {
		case received := <-c.signals:
			signalCount++
			if signalCount == 1 {
				c.cancel()
				c.stopFirstOnce.Do(c.stopFirst)
				continue
			}
			c.stopSignalsOnce.Do(c.stopSignals)
			c.hardExit(signalExitCode(received))
			return
		case <-c.done:
			return
		}
	}
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

	return root.ExecuteContext(ctx)
}
