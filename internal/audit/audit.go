// Package audit provides an append-only structured log of every dotular operation.
package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Entry records a single operation.
type Entry struct {
	Time    time.Time `json:"time"`
	Command string    `json:"command"` // "apply" | "pull" | "sync" | "verify"
	Module  string    `json:"module"`
	Item    string    `json:"item"`
	Outcome string    `json:"outcome"` // "success" | "skipped" | "failure"
	Error   string    `json:"error,omitempty"`
}

// Log appends e to the audit log. Errors are silently ignored so that logging
// never halts normal operation.
func Log(e Entry) {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	path, err := logPath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	line, _ := json.Marshal(e)
	f.WriteString(string(line) + "\n")
}

// Read loads log entries, optionally filtered by module name.
// It returns the last limit entries (all if limit <= 0).
func Read(moduleFilter string, limit int) ([]Entry, error) {
	path, err := logPath()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // skip malformed lines
		}
		if moduleFilter != "" && e.Module != moduleFilter {
			continue
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

// LogPath returns the path of the audit log file.
func LogPath() string {
	p, _ := logPath()
	return p
}

func logPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".local", "share", "dotular", "history.log"), nil
}
