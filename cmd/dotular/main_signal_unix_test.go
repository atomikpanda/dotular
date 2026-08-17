//go:build !windows

package main

import (
	"os"
	"slices"
	"syscall"
	"testing"
)

func TestTerminationSignalsIncludeInterruptAndSIGTERM(t *testing.T) {
	signals := terminationSignals()
	for _, want := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		if !slices.Contains(signals, want) {
			t.Errorf("terminationSignals() = %v, want %v", signals, want)
		}
	}
}
