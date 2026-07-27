// Package testutil provides helpers shared across the test suites.
package testutil

import (
	"fmt"
	"os"
	"testing"
)

// homeEnvVars lists every variable os.UserHomeDir consults: it reads $HOME on
// Unix but %USERPROFILE% on Windows. All of them must be redirected together or
// the isolation silently does nothing on one of the two platforms.
var homeEnvVars = []string{"HOME", "USERPROFILE"}

// IsolateHome points the home directory at a fresh temp dir, runs m, removes the
// temp dir and returns m's exit code, for use as:
//
//	func TestMain(m *testing.M) { os.Exit(testutil.IsolateHome(m)) }
//
// Call it from any package whose tests resolve paths through os.UserHomeDir.
// dotular keeps its audit log, machine tags and registry cache under the home
// directory, so without this the suite mutates the developer's real files.
func IsolateHome(m *testing.M) int {
	home, err := os.MkdirTemp("", "dotular-test-home")
	if err != nil {
		fmt.Fprintln(os.Stderr, "isolate home dir:", err)
		os.Exit(1)
	}
	for _, key := range homeEnvVars {
		os.Setenv(key, home)
	}
	code := m.Run()
	os.RemoveAll(home)
	return code
}

// SetHome points the home directory at dir for the duration of t. Use it for a
// test that needs a home of its own rather than the package-wide one that
// IsolateHome provides — typically to assert on an empty one.
func SetHome(t *testing.T, dir string) {
	t.Helper()
	for _, key := range homeEnvVars {
		t.Setenv(key, dir)
	}
}
