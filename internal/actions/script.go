package actions

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"

	"github.com/atomikpanda/dotular/internal/color"
	"github.com/atomikpanda/dotular/internal/httputil"
	"github.com/atomikpanda/dotular/internal/procutil"
)

// ScriptAction runs a shell script, either from a local path or a remote URL.
type ScriptAction struct {
	Script string
	Via    string // "remote" or "local"
}

func (a *ScriptAction) Describe() string {
	return fmt.Sprintf("run script %q (via %s)", a.Script, a.Via)
}

func (a *ScriptAction) Run(ctx context.Context, dryRun bool) error {
	if dryRun {
		fmt.Printf("    %s\n", color.Dim(fmt.Sprintf("[dry-run] run script: %s (via %s)", a.Script, a.Via)))
		return nil
	}
	switch a.Via {
	case "remote":
		return runRemoteScript(ctx, a.Script)
	case "local", "":
		return runLocalScript(ctx, a.Script)
	default:
		return fmt.Errorf("unknown script source %q; expected \"remote\" or \"local\"", a.Via)
	}
}

func runRemoteScript(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := httputil.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Without this an error page (404/500 HTML) would be executed as a script.
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	script, err := httputil.ReadBody(resp.Body)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp("", "dotular-*.sh")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(script); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}

	return execScript(ctx, tmp.Name())
}

func runLocalScript(ctx context.Context, path string) error {
	return execScript(ctx, path)
}

func execScript(ctx context.Context, path string) error {
	shell := "bash"
	if runtime.GOOS == "windows" {
		shell = "powershell"
	}
	cmd := procutil.CommandContext(ctx, shell, path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
