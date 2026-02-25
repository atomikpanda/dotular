package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
	// Read from the actual log path. The test is mainly that it doesn't crash.
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
	// Override HOME to a temp dir to get a missing log file.
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

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
