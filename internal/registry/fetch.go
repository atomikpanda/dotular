package registry

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/atomikpanda/dotular/internal/config"
)

// Fetch retrieves a remote module by its reference string, using the cache
// when available. When noCache is true the network is always consulted.
//
// If the module is already in the lockfile, the cached copy's checksum is
// verified against the recorded value; a mismatch is a fatal error.
func Fetch(ctx context.Context, rawRef string, lock *LockFile, noCache bool) (*RemoteModule, TrustLevel, error) {
	ref := ParseRef(rawRef)

	cachePath := moduleCachePath(rawRef)
	entry, inLock := lock.Registry[rawRef]

	if !noCache && inLock {
		// Validate cache file exists and checksum matches.
		if data, err := os.ReadFile(cachePath); err == nil {
			sum := fmt.Sprintf("%x", sha256.Sum256(data))
			if sum != entry.SHA256 {
				return nil, ref.Trust, fmt.Errorf(
					"registry: checksum mismatch for %s (expected %s, got %s) — run with --no-cache to re-fetch",
					rawRef, entry.SHA256, sum,
				)
			}
			return parseModule(data)
		}
		// Cache file missing despite lockfile entry — re-fetch below.
	}

	// Fetch from network.
	data, err := download(ctx, ref.FetchURL)
	if err != nil {
		return nil, ref.Trust, fmt.Errorf("fetch %s: %w", rawRef, err)
	}

	// Verify against existing lockfile entry (if any).
	sum := fmt.Sprintf("%x", sha256.Sum256(data))
	if inLock && entry.SHA256 != sum {
		return nil, ref.Trust, fmt.Errorf(
			"registry: checksum mismatch for %s after re-fetch (lockfile: %s, got: %s)",
			rawRef, entry.SHA256, sum,
		)
	}

	// Update lockfile + write cache.
	lock.Registry[rawRef] = LockEntry{
		SHA256:    sum,
		FetchedAt: time.Now().UTC(),
		URL:       ref.FetchURL,
	}
	if err := writeCacheFile(cachePath, data); err != nil {
		// Non-fatal: we have the data in memory.
		fmt.Fprintf(os.Stderr, "  warning: could not cache registry module: %v\n", err)
	}

	mod, _, err := parseModule(data)
	return mod, ref.Trust, err
}

func download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "dotular/1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	var buf []byte
	tmp := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if readErr != nil {
			break
		}
	}
	return buf, nil
}

func parseModule(data []byte) (*RemoteModule, TrustLevel, error) {
	var mod RemoteModule
	if err := yaml.Unmarshal(data, &mod); err != nil {
		return nil, Private, fmt.Errorf("parse registry module: %w", err)
	}
	return &mod, Private, nil
}

func moduleCachePath(rawRef string) string {
	safe := strings.NewReplacer(
		"/", "_", "@", "_", ":", "_", ".", "_",
	).Replace(rawRef)
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "dotular", "registry", safe+".yaml")
}

func writeCacheFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ClearCache removes the local registry cache directory.
func ClearCache() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(home, ".cache", "dotular", "registry"))
}

// CachedRefs returns the references currently in the cache directory.
func CachedRefs(lock *LockFile) []string {
	refs := make([]string, 0, len(lock.Registry))
	for ref := range lock.Registry {
		refs = append(refs, ref)
	}
	return refs
}

// UnusedCacheEntries returns lock entries whose ref is not in the given set.
func UnusedCacheEntries(lock *LockFile, activeRefs map[string]bool) []string {
	var unused []string
	for ref := range lock.Registry {
		if !activeRefs[ref] {
			unused = append(unused, ref)
		}
	}
	return unused
}

// collectActiveRefs walks a config and returns the set of registry refs used.
func CollectActiveRefs(cfg config.Config) map[string]bool {
	refs := make(map[string]bool)
	for _, mod := range cfg.Modules {
		if mod.From != "" {
			refs[mod.From] = true
		}
	}
	return refs
}
