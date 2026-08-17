//go:build windows

package main

import (
	"os"
	"reflect"
	"testing"
)

func TestTerminationSignalsAreWindowsSafe(t *testing.T) {
	if got, want := terminationSignals(), []os.Signal{os.Interrupt}; !reflect.DeepEqual(got, want) {
		t.Fatalf("terminationSignals() = %v, want %v", got, want)
	}
}
