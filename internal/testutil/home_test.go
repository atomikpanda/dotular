package testutil

import (
	"os"
	"testing"
)

// Exercises IsolateHome itself: every assertion below depends on it having run.
func TestMain(m *testing.M) {
	os.Exit(IsolateHome(m))
}

func TestIsolateHomeRedirectsEveryManagedVar(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	// The guarantee that matters: os.UserHomeDir agrees with every variable it
	// might consult, so the isolation holds whichever one this platform reads.
	for _, key := range homeEnvVars {
		if got := os.Getenv(key); got != home {
			t.Errorf("%s = %q, want %q", key, got, home)
		}
	}
	if _, err := os.Stat(home); err != nil {
		t.Errorf("isolated home should exist: %v", err)
	}
}

func TestSetHomeRedirectsEveryManagedVar(t *testing.T) {
	dir := t.TempDir()
	SetHome(t, dir)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if home != dir {
		t.Errorf("os.UserHomeDir() = %q, want %q", home, dir)
	}
	for _, key := range homeEnvVars {
		if got := os.Getenv(key); got != dir {
			t.Errorf("%s = %q, want %q", key, got, dir)
		}
	}
}
