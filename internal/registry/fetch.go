package registry

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/atomikpanda/dotular/internal/config"
	"github.com/atomikpanda/dotular/internal/httputil"
	"github.com/atomikpanda/dotular/internal/ui"
)

// httpClient performs every registry download. It is a variable so that tests
// can point the fetch path at an httptest server: Ref.FetchURL is derived from
// the ref, so swapping the client is the only seam that does not require faking
// a hostname.
var httpClient = httputil.Client

type FetchOptions struct {
	NoCache bool
}

// Fetch retrieves a remote module by its reference string, using the cache
// when available. When opts.NoCache is true the network is always consulted.
//
// If the module is already in the lockfile, the cached copy's checksum is
// verified against the recorded value; a mismatch is a fatal error.
func Fetch(ctx context.Context, rawRef string, lock *LockFile, opts FetchOptions, u *ui.UI) (*RemoteModule, TrustLevel, error) {
	ref := ParseRef(rawRef)
	// Checked before the cache is consulted: an unhonourable version is a bad
	// reference, not a stale download, so a warm cache must not mask it.
	if err := ref.checkVersionSupported(); err != nil {
		return nil, ref.Trust, err
	}

	if err := rejectModuleCachePathCollisions(
		[]string{rawRef},
		CachedRefs(lock),
		moduleCachePath,
		runtime.GOOS,
	); err != nil {
		return nil, ref.Trust, err
	}

	cachePath := moduleCachePath(rawRef)
	entry, inLock := lock.Registry[rawRef]

	var (
		data        []byte
		mod         *RemoteModule
		replacement LockEntry
		err         error
	)
	fromCache := false
	if !opts.NoCache && inLock {
		if cachedData, readErr := os.ReadFile(cachePath); readErr == nil {
			data = cachedData
			fromCache = true
		}
	}

	if fromCache {
		replacement = LockEntry{
			SHA256:    fmt.Sprintf("%x", sha256.Sum256(data)),
			FetchedAt: time.Now().UTC(),
			URL:       ref.FetchURL,
		}
		if err := verifyPinnedChecksum(rawRef, &entry, replacement.SHA256); err != nil {
			return nil, ref.Trust, err
		}
		mod, err = parseModule(data)
		if err != nil {
			return nil, ref.Trust, err
		}
	} else {
		var expected *LockEntry
		if inLock {
			expected = &entry
		}
		var trust TrustLevel
		data, mod, replacement, trust, err = fetchNoWrite(ctx, rawRef, expected)
		if err != nil {
			return nil, trust, err
		}
	}

	if !inLock {
		lock.Registry[rawRef] = replacement
	}

	if !fromCache {
		if err := writeCacheFile(cachePath, data); err != nil {
			// Non-fatal: we have the data in memory.
			u.Warn(fmt.Sprintf("could not cache registry module: %v", err))
		}
	}

	return mod, ref.Trust, nil
}

// fetchNoWrite downloads, checksums, and parses a registry module without
// consulting or mutating the lockfile or cache. An optional expected entry
// preserves Fetch's immutable-pin check before parsing untrusted bytes.
func fetchNoWrite(ctx context.Context, rawRef string, expected *LockEntry) ([]byte, *RemoteModule, LockEntry, TrustLevel, error) {
	ref := ParseRef(rawRef)
	if err := ref.checkVersionSupported(); err != nil {
		return nil, nil, LockEntry{}, ref.Trust, err
	}

	data, err := download(ctx, ref.FetchURL)
	if err != nil {
		return nil, nil, LockEntry{}, ref.Trust, fmt.Errorf("fetch %s: %w", rawRef, err)
	}

	replacement := LockEntry{
		SHA256:    fmt.Sprintf("%x", sha256.Sum256(data)),
		FetchedAt: time.Now().UTC(),
		URL:       ref.FetchURL,
	}
	if err := verifyPinnedChecksum(rawRef, expected, replacement.SHA256); err != nil {
		return nil, nil, LockEntry{}, ref.Trust, err
	}
	mod, err := parseModule(data)
	if err != nil {
		return nil, nil, LockEntry{}, ref.Trust, err
	}

	return data, mod, replacement, ref.Trust, nil
}

func verifyPinnedChecksum(rawRef string, expected *LockEntry, got string) error {
	if expected == nil || expected.SHA256 == got {
		return nil
	}
	return fmt.Errorf(
		"registry: checksum mismatch for %s (expected %s, got %s)",
		rawRef, expected.SHA256, got,
	)
}

func download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "dotular/1")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	// A read error must abort: a truncated module is still valid YAML with fewer
	// items, so returning the partial body would get corrupt content SHA-256'd
	// into the lockfile as the authoritative pin.
	return httputil.ReadBody(resp.Body)
}

// parseModule decodes a module definition. Trust is a property of the reference,
// not of the bytes, so it is Fetch's to report on both the cache and network
// paths — deciding it here is what made every cache hit look external.
func parseModule(data []byte) (*RemoteModule, error) {
	var mod RemoteModule
	if err := yaml.Unmarshal(data, &mod); err != nil {
		return nil, fmt.Errorf("parse registry module: %w", err)
	}
	return &mod, nil
}

func registryCacheDir() (string, error) {
	home, err := os.UserHomeDir()
	return filepath.Join(home, ".cache", "dotular", "registry"), err
}

func moduleCachePath(rawRef string) string {
	safe := strings.Map(func(r rune) rune {
		if r < ' ' || strings.ContainsRune(`<>:"/\|?*@.`, r) {
			return '_'
		}
		return r
	}, rawRef)

	lower := strings.ToLower(safe)
	reserved := lower == "con" || lower == "prn" || lower == "aux" || lower == "nul"
	if strings.HasPrefix(lower, "com") || strings.HasPrefix(lower, "lpt") {
		suffix := lower[3:]
		if (len(suffix) == 1 && suffix[0] >= '1' && suffix[0] <= '9') ||
			suffix == "¹" || suffix == "²" || suffix == "³" {
			reserved = true
		}
	}
	if reserved {
		safe += "_"
	}

	cacheDir, _ := registryCacheDir()
	return filepath.Join(cacheDir, safe+".yaml")
}

// ClearCache removes the local registry cache directory while holding the
// process-independent registry mutation lock.
func ClearCache() error {
	cacheDir, err := registryCacheDir()
	if err != nil {
		return err
	}
	return WithRegistryMutationLock(func() error {
		return os.RemoveAll(cacheDir)
	})
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
