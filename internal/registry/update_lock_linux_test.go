//go:build linux

package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRegistryUpdateLockPathUsesCanonicalLockfileIdentityOutsideRegistryCache(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	repository := t.TempDir()
	configA := filepath.Join(repository, "a.yaml")
	configB := filepath.Join(repository, "b.yaml")
	for _, path := range []string{configA, configB} {
		if err := os.WriteFile(path, []byte("modules: []\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	pathA, err := registryUpdateLockPath(configA)
	if err != nil {
		t.Fatalf("registryUpdateLockPath(a) error = %v", err)
	}
	pathB, err := registryUpdateLockPath(configB)
	if err != nil {
		t.Fatalf("registryUpdateLockPath(b) error = %v", err)
	}
	if pathB != pathA {
		t.Fatalf("same lockfile destination produced different paths: a=%q b=%q", pathA, pathB)
	}

	parentAlias := filepath.Join(t.TempDir(), "repository-alias")
	if err := os.Symlink(repository, parentAlias); err != nil {
		t.Fatal(err)
	}
	aliasPath, err := registryUpdateLockPath(filepath.Join(parentAlias, filepath.Base(configA)))
	if err != nil {
		t.Fatalf("registryUpdateLockPath(parent alias) error = %v", err)
	}
	if aliasPath != pathA {
		t.Fatalf("parent-alias lock path = %q, want canonical path %q", aliasPath, pathA)
	}

	configAliasDir := t.TempDir()
	configAlias := filepath.Join(configAliasDir, "config-alias.yaml")
	if err := os.Symlink(configA, configAlias); err != nil {
		t.Fatal(err)
	}
	configAliasPath, err := registryUpdateLockPath(configAlias)
	if err != nil {
		t.Fatalf("registryUpdateLockPath(config alias) error = %v", err)
	}
	if configAliasPath == pathA {
		t.Fatalf("config-file alias in distinct lockfile directory reused %q", pathA)
	}

	wantDir := filepath.Join(cacheRoot, "dotular", "update-locks")
	if filepath.Dir(pathA) != wantDir {
		t.Fatalf("lock directory = %q, want %q", filepath.Dir(pathA), wantDir)
	}
	registryCacheDir := filepath.Join(cacheRoot, "dotular", "registry")
	relativeToRegistry, err := filepath.Rel(registryCacheDir, pathA)
	if err != nil {
		t.Fatalf("resolve lock path relative to registry cache: %v", err)
	}
	if relativeToRegistry != ".." && !strings.HasPrefix(relativeToRegistry, ".."+string(filepath.Separator)) {
		t.Fatalf("lock path %q is inside deletable registry cache tree %q", pathA, registryCacheDir)
	}
	if filepath.Ext(pathA) != ".lock" || len(strings.TrimSuffix(filepath.Base(pathA), ".lock")) != 64 {
		t.Fatalf("lock filename = %q, want SHA-256 key with .lock suffix", filepath.Base(pathA))
	}
	if filepath.Dir(pathA) == repository {
		t.Fatalf("lock path %q leaves an artifact in repository %q", pathA, repository)
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
	configDir := t.TempDir()
	configA := filepath.Join(configDir, "a.yaml")
	configB := filepath.Join(configDir, "b.yaml")
	for _, path := range []string{configA, configB} {
		if err := os.WriteFile(path, []byte("modules: []\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	releaseFirst, err := acquireRegistryUpdateLock(configA)
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
		release, err := acquireRegistryUpdateLock(configB)
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

	lockPath, err := registryUpdateLockPath(configA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("stat retained lock file: %v", err)
	}
}
