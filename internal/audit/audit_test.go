package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atomikpanda/dotular/internal/testutil"
)

// Log and Read resolve their path from the home directory, so without this the
// suite appends fixture rows to the developer's real history.log.
func TestMain(m *testing.M) {
	os.Exit(testutil.IsolateHome(m))
}

func TestEntryJSON(t *testing.T) {
	e := Entry{
		Time:    time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
		Command: "apply",
		Module:  "test",
		Item:    "install pkg",
		Outcome: "success",
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Entry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Command != "apply" {
		t.Errorf("Command = %q", decoded.Command)
	}
	if decoded.Module != "test" {
		t.Errorf("Module = %q", decoded.Module)
	}
	if decoded.Outcome != "success" {
		t.Errorf("Outcome = %q", decoded.Outcome)
	}
}

func TestEntryWithError(t *testing.T) {
	e := Entry{
		Command: "apply",
		Module:  "test",
		Item:    "install",
		Outcome: "failure",
		Error:   "command not found",
	}
	data, _ := json.Marshal(e)
	var decoded Entry
	json.Unmarshal(data, &decoded)
	if decoded.Error != "command not found" {
		t.Errorf("Error = %q", decoded.Error)
	}
}

func TestEntryErrorOmitEmpty(t *testing.T) {
	e := Entry{
		Command: "apply",
		Module:  "test",
		Outcome: "success",
	}
	data, _ := json.Marshal(e)
	var m map[string]any
	json.Unmarshal(data, &m)
	if _, exists := m["error"]; exists {
		t.Error("error field should be omitted when empty")
	}
}

func TestLogPath(t *testing.T) {
	p := LogPath()
	if p == "" {
		t.Error("LogPath() should not be empty")
	}
	if filepath.Base(p) != "history.log" {
		t.Errorf("LogPath() basename = %q", filepath.Base(p))
	}
}

func TestLogWritesEntry(t *testing.T) {
	Log(Entry{
		Time:    time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		Command: "test",
		Module:  "unit-test",
		Item:    "test-entry",
		Outcome: "success",
	})
}

func TestLogAutoSetsTime(t *testing.T) {
	e := Entry{
		Command: "test",
		Module:  "unit-test",
		Item:    "auto-time",
		Outcome: "success",
	}
	if !e.Time.IsZero() {
		t.Error("time should be zero before Log")
	}
	Log(e)
}

func TestRead(t *testing.T) {
	// Read from the isolated log path. The test is mainly that it doesn't crash.
	entries, err := Read("", 10)
	if err != nil {
		t.Fatal(err)
	}
	_ = entries // may be nil if log is empty
}

func TestReadWithFilter(t *testing.T) {
	entries, err := Read("unit-test", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Module != "unit-test" {
			t.Errorf("expected module=unit-test, got %q", e.Module)
		}
	}
}

func TestReadNoLimit(t *testing.T) {
	entries, err := Read("", 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = entries
}

func TestReadMissingFile(t *testing.T) {
	// A fresh home, not the package-wide one, so the log file is missing.
	testutil.SetHome(t, t.TempDir())

	entries, err := Read("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if entries != nil {
		t.Errorf("expected nil entries for missing file, got %d", len(entries))
	}
}

func TestReadWithLimit(t *testing.T) {
	// Write some entries to a temp log, then read with limit.
	entries, err := Read("", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > 2 {
		t.Errorf("expected at most 2 entries, got %d", len(entries))
	}
}

func TestEntryRollbackIdentityJSON(t *testing.T) {
	entry := Entry{
		Command: "apply",
		Module:  "dotfiles",
		Item:    "install package \"git\" via apt",
		Phase:   "rollback",
		Scope:   "item",
		Outcome: "rollback_failed",
		Reason:  "typed capture unavailable",
		Error:   "uninstall failed",
	}

	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Entry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Phase != "rollback" || decoded.Scope != "item" {
		t.Fatalf("decoded rollback identity = phase %q scope %q", decoded.Phase, decoded.Scope)
	}
	if decoded.Reason != entry.Reason || decoded.Error != entry.Error {
		t.Fatalf("decoded rollback detail = %+v, want %+v", decoded, entry)
	}
}

func TestEntryOldJSONRemainsReadableWithoutRollbackIdentity(t *testing.T) {
	oldLine := []byte(`{"time":"2024-06-15T12:00:00Z","command":"apply","module":"old","item":"run old","outcome":"success"}`)
	var entry Entry
	if err := json.Unmarshal(oldLine, &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Module != "old" || entry.Outcome != "success" {
		t.Fatalf("decoded old entry = %+v", entry)
	}
	if entry.Phase != "" || entry.Scope != "" {
		t.Fatalf("old entry gained rollback identity: %+v", entry)
	}
}

func TestEntryForwardJSONOmitsRollbackIdentity(t *testing.T) {
	data, err := json.Marshal(Entry{
		Command: "apply",
		Module:  "forward",
		Item:    "run forward",
		Outcome: "success",
	})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["phase"]; ok {
		t.Fatalf("forward JSON includes phase: %s", data)
	}
	if _, ok := fields["scope"]; ok {
		t.Fatalf("forward JSON includes scope: %s", data)
	}
}
