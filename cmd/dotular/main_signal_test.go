package main

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestSignalControllerCancelsThenHardExitsDuringRollback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 2)
	firstNotificationStopped := make(chan struct{})
	hardExit := make(chan int, 1)

	controller := newSignalController(
		cancel,
		signals,
		func() { close(firstNotificationStopped) },
		func() {},
		func(code int) { hardExit <- code },
	)
	defer controller.Stop()
	controller.RollbackCapable()

	signals <- os.Interrupt
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("first signal did not cancel forward context")
	}
	select {
	case <-firstNotificationStopped:
	case <-time.After(time.Second):
		t.Fatal("first signal did not stop the root notification context")
	}
	select {
	case code := <-hardExit:
		t.Fatalf("first signal hard-exited with code %d before rollback", code)
	default:
	}

	controller.RollbackStarted()
	signals <- os.Interrupt
	select {
	case code := <-hardExit:
		if code == 0 {
			t.Error("hard exit code = 0, want non-zero")
		}
	case <-time.After(time.Second):
		t.Fatal("second signal during rollback did not hard-exit")
	}
}

func TestSignalControllerHardExitsSecondSignalWhenRollbackUnavailable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 2)
	hardExit := make(chan int, 1)
	controller := newSignalController(cancel, signals, func() {}, func() {}, func(code int) {
		hardExit <- code
	})
	defer controller.Stop()

	signals <- os.Interrupt
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("first signal did not cancel forward context")
	}
	signals <- os.Interrupt
	select {
	case code := <-hardExit:
		if code == 0 {
			t.Error("hard exit code = 0, want non-zero")
		}
	case <-time.After(time.Second):
		t.Fatal("second signal did not hard-exit when rollback is unavailable")
	}
}

func TestSignalControllerGatesExtraPreRollbackSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal)
	hardExit := make(chan int, 1)
	controller := newSignalController(cancel, signals, func() {}, func() {}, func(code int) {
		hardExit <- code
	})
	defer controller.Stop()
	controller.RollbackCapable()

	sendSignal := func() {
		t.Helper()
		sent := make(chan struct{})
		go func() {
			signals <- os.Interrupt
			close(sent)
		}()
		select {
		case <-sent:
		case <-time.After(time.Second):
			t.Fatal("signal controller did not receive signal")
		}
	}

	sendSignal()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("first signal did not cancel forward context")
	}
	sendSignal()
	select {
	case code := <-hardExit:
		t.Fatalf("pre-rollback signal hard-exited with code %d", code)
	default:
	}

	controller.RollbackStarted()
	sendSignal()
	select {
	case code := <-hardExit:
		if code == 0 {
			t.Error("hard exit code = 0, want non-zero")
		}
	case <-time.After(time.Second):
		t.Fatal("signal during rollback did not hard-exit")
	}
}

func TestSignalControllerStopCleansUpNotificationsAndWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 1)
	var firstStops atomic.Int32
	var channelStops atomic.Int32
	var hardExits atomic.Int32

	controller := newSignalController(
		cancel,
		signals,
		func() { firstStops.Add(1) },
		func() { channelStops.Add(1) },
		func(int) { hardExits.Add(1) },
	)
	controller.Stop()
	controller.Stop()

	if ctx.Err() != context.Canceled {
		t.Fatalf("context error = %v, want context.Canceled", ctx.Err())
	}
	if got := firstStops.Load(); got != 1 {
		t.Errorf("root notification stops = %d, want 1", got)
	}
	if got := channelStops.Load(); got != 1 {
		t.Errorf("signal channel stops = %d, want 1", got)
	}
	if got := hardExits.Load(); got != 0 {
		t.Errorf("hard exits = %d, want 0", got)
	}
	select {
	case <-controller.finished:
	default:
		t.Fatal("signal worker still running after Stop")
	}
}
