//go:build !windows

package main

import (
	"os"
	"syscall"
)

func terminationSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

func signalExitCode(received os.Signal) int {
	if number, ok := received.(syscall.Signal); ok {
		return 128 + int(number)
	}
	return exitFailure
}
