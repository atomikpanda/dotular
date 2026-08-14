//go:build linux

package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRegistryUpdateLockPathUsesCanonicalConfigIdentityInUserCache(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	repository := t.TempDir()
	configPath := filepath.Join(repository, "dotular.yaml")
	if err := os.WriteFile(configPath, []byte("modules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	aliasPath := filepath.Join(t.TempDir(), "config-alias.yaml")
	if err := os.Symlink(configPath, aliasPath); err != nil {
		t.Fatal(err)
	}

	path, err := registryUpdateLockPath(configPath)
	if err != nil {
		t.Fatalf("registryUpdateLockPath() error = %v", err)
	}
	alias, err := registryUpdateLockPath(aliasPath)
	if err != nil {
		t.Fatalf("registryUpdateLockPath(alias) error = %v", err)
	}

	if alias != path {
		t.Fatalf("alias lock path = %q, want canonical path %q", alias, path)
	}
	wantDir := filepath.Join(cacheRoot, "dotular", "registry", "update-locks")
	if filepath.Dir(path) != wantDir {
		t.Fatalf("lock directory = %q, want %q", filepath.Dir(path), wantDir)
	}
	if filepath.Ext(path) != ".lock" || len(strings.TrimSuffix(filepath.Base(path), ".lock")) != 64 {
		t.Fatalf("lock filename = %q, want SHA-256 key with .lock suffix", filepath.Base(path))
	}
	if filepath.Dir(path) == repository {
		t.Fatalf("lock path %q leaves an artifact in repository %q", path, repository)
	}
}

func TestNormalizeRegistryUpdateIdentityFoldsCaseForCaseInsensitivePlatforms(t *testing.T) {
	path := filepath.Join(string(filepath.Separator), "Users", "Example", "Dotular.yaml")
	for _, goos := range []string{"darwin", "windows"} {
		if got, want := normalizeRegistryUpdateIdentity(path, goos), strings.ToLower(filepath.Clean(path)); got != want {
			t.Fatalf("normalizeRegistryUpdateIdentity(%q, %q) = %q, want %q", path, goos, got, want)
		}
	}
	if got, want := normalizeRegistryUpdateIdentity(path, "linux"), filepath.Clean(path); got != want {
		t.Fatalf("normalizeRegistryUpdateIdentity(%q, linux) = %q, want %q", path, got, want)
	}
}

func TestAcquireRegistryUpdateLockProvidesMutualExclusion(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	configPath := filepath.Join(t.TempDir(), "dotular.yaml")
	if err := os.WriteFile(configPath, []byte("modules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	releaseFirst, err := acquireRegistryUpdateLock(configPath)
	if err != nil {
		t.Fatalf("first acquireRegistryUpdateLock() error = %v", err)
	}
	firstReleased := false
	t.Cleanup(func() {
		if !firstReleased {
			_ = releaseFirst()
		}
	})

	attempted := make(chan struct{})
	acquired := make(chan func() error, 1)
	errs := make(chan error, 1)
	go func() {
		close(attempted)
		release, err := acquireRegistryUpdateLock(configPath)
		if err != nil {
			errs <- err
			return
		}
		acquired <- release
	}()
	<-attempted

	select {
	case release := <-acquired:
		_ = release()
		t.Fatal("second acquisition succeeded while first lock was held")
	case err := <-errs:
		t.Fatalf("second acquireRegistryUpdateLock() error = %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := releaseFirst(); err != nil {
		t.Fatalf("release first registry update lock: %v", err)
	}
	firstReleased = true
	select {
	case release := <-acquired:
		if err := release(); err != nil {
			t.Fatalf("release second registry update lock: %v", err)
		}
	case err := <-errs:
		t.Fatalf("second acquireRegistryUpdateLock() error = %v", err)
	case <-time.After(time.Second):
		t.Fatal("second acquisition remained blocked after first release")
	}

	lockPath, err := registryUpdateLockPath(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("stat retained lock file: %v", err)
	}
}
