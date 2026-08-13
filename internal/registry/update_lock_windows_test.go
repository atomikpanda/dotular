//go:build windows

package registry

import (
	"runtime"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsMutexUsesGlobalCaseInsensitiveName(t *testing.T) {
	upper := windowsMutexObjectName(`C:\Users\Test\dotular.lock.yaml`)
	lower := windowsMutexObjectName(`c:\users\test\dotular.lock.yaml`)
	if upper != lower {
		t.Errorf("mutex names differ only by path case: %q != %q", upper, lower)
	}
	if !strings.HasPrefix(upper, `Global\dotular-registry-`) {
		t.Errorf("mutex name = %q, want Global namespace", upper)
	}
}

func TestWindowsMutexKeepsOwnerOnOneOSThread(t *testing.T) {
	release, err := acquirePlatformWriterLock(
		`C:\Users\Test\dotular.lock.yaml`,
		`C:\Users\Test\dotular.yaml`,
	)
	if err != nil {
		t.Fatal(err)
	}
	owner := windows.GetCurrentThreadId()
	for range 100 {
		runtime.Gosched()
		if got := windows.GetCurrentThreadId(); got != owner {
			t.Fatalf("mutex owner moved from OS thread %d to %d", owner, got)
		}
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}
