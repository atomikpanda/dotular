package procutil

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestCommandContextKillsBackgroundChildrenOnCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix process-group regression")
	}

	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	marker := filepath.Join(dir, "marker")
	ctx, cancel := context.WithCancel(context.Background())
	cmd := CommandContext(ctx, "sh", "-c", `touch "$READY"; (sleep 0.5; touch "$MARKER") & wait`)
	cmd.Env = append(os.Environ(), "READY="+ready, "MARKER="+marker)
	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("command did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("canceled command returned nil")
	}

	time.Sleep(750 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("background child recreated state after cancellation: %v", err)
	}
}
