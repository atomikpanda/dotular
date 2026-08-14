//go:build linux

package registry

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRegistryUpdateLockPathIsGlobalAndOutsideRegistryCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := registryUpdateLockPath()
	if err != nil {
		t.Fatalf("registryUpdateLockPath() error = %v", err)
	}
	want := filepath.Join(home, ".cache", "dotular", "registry-mutation.lock")
	if path != want {
		t.Fatalf("registry update lock path = %q, want %q", path, want)
	}
}

func TestClearCacheWaitsForRegistryMutationLockAndPreservesLockFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cacheDir, err := registryCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(cacheDir, "cached.yaml")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte("cached"), 0o644); err != nil {
		t.Fatal(err)
	}

	release, err := acquireRegistryUpdateLock()
	if err != nil {
		t.Fatalf("acquireRegistryUpdateLock() error = %v", err)
	}
	released := false
	t.Cleanup(func() {
		if !released {
			_ = release()
		}
	})
	lockPath, err := registryUpdateLockPath()
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(lockPath)
	if err != nil {
		t.Fatalf("stat held lock file: %v", err)
	}

	clearStarted := make(chan struct{})
	clearDone := make(chan error, 1)
	go func() {
		close(clearStarted)
		clearDone <- ClearCache()
	}()
	<-clearStarted

	select {
	case err := <-clearDone:
		t.Fatalf("ClearCache returned while mutation lock was held: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("cache changed while mutation lock was held: %v", err)
	}

	if err := release(); err != nil {
		t.Fatalf("release registry update lock: %v", err)
	}
	released = true
	select {
	case err := <-clearDone:
		if err != nil {
			t.Fatalf("ClearCache() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ClearCache remained blocked after mutation lock release")
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("stat cleared cache directory error = %v, want not exist", err)
	}
	after, err := os.Stat(lockPath)
	if err != nil {
		t.Fatalf("stat retained lock file: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatalf("ClearCache replaced registry mutation lock file %q", lockPath)
	}
}
