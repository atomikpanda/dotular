package actions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestScriptActionDescribe(t *testing.T) {
	a := &ScriptAction{Script: "install.sh", Via: "local"}
	got := a.Describe()
	want := `run script "install.sh" (via local)`
	if got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}
}

func TestScriptActionDryRun(t *testing.T) {
	a := &ScriptAction{Script: "install.sh", Via: "local"}
	if err := a.Run(context.Background(), true); err != nil {
		t.Errorf("dry run error: %v", err)
	}
}

func TestScriptActionRunLocal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "test.sh")
	os.WriteFile(script, []byte("#!/bin/bash\ntrue\n"), 0o755)

	a := &ScriptAction{Script: script, Via: "local"}
	if err := a.Run(context.Background(), false); err != nil {
		t.Errorf("Run error: %v", err)
	}
}

func TestScriptActionRunRemote(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("#!/bin/bash\ntrue\n"))
	}))
	defer srv.Close()

	a := &ScriptAction{Script: srv.URL + "/install.sh", Via: "remote"}
	if err := a.Run(context.Background(), false); err != nil {
		t.Errorf("remote script error: %v", err)
	}
}

func TestScriptActionRunRemoteDefaultVia(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "test.sh")
	os.WriteFile(script, []byte("#!/bin/bash\ntrue\n"), 0o755)

	a := &ScriptAction{Script: script, Via: ""}
	if err := a.Run(context.Background(), false); err != nil {
		t.Errorf("default via error: %v", err)
	}
}

func TestScriptActionUnknownVia(t *testing.T) {
	a := &ScriptAction{Script: "test.sh", Via: "unknown"}
	if err := a.Run(context.Background(), false); err == nil {
		t.Error("expected error for unknown via")
	}
}
