package registry

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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

// FetchOptions controls how Fetch treats the two independent things a fetch
// touches: the disk cache and the lockfile pin.
type FetchOptions struct {
	// NoCache bypasses the on-disk cache and always consults the network. It
	// has no bearing on integrity: a pinned ref is verified either way.
	NoCache bool
	// NoWrite suppresses Fetch's disk-cache write. UpdatePins uses it for
	// --check while retaining the fetched checksum in its private LockFile.
	NoWrite bool
	// Repin permits an existing pin to be moved to the bytes just fetched. Only
	// UpdatePins sets it: everywhere else a ref whose content no longer matches
	// its pin is refused, which is what makes an apply reproducible.
	Repin bool
	// deferredCacheWrites lets UpdatePins publish downloaded bytes only after
	// their pins are durable. It is nil for every ordinary fetch.
	deferredCacheWrites map[string][]byte
}

// ChecksumMismatch reports content that disagrees with the lockfile pin. The
// pin records what was approved, so it wins: the content is refused rather than
// recorded.
type ChecksumMismatch struct {
	Ref       string
	Pinned    string
	Got       string
	FromCache bool
}

func (e *ChecksumMismatch) Error() string {
	if e.FromCache {
		return fmt.Sprintf(
			"registry: checksum mismatch for %s (pinned %s, cached copy %s) — the local cache is stale or has been tampered with; run 'dotular registry clear' to discard it",
			e.Ref, e.Pinned, e.Got,
		)
	}
	return fmt.Sprintf(
		"registry: checksum mismatch for %s (pinned %s, upstream now serves %s) — upstream changed under you; run 'dotular registry update' to review the change and move the pin",
		e.Ref, e.Pinned, e.Got,
	)
}

// Fetch retrieves a remote module by its reference string, using the cache
// when available.
//
// If the ref is already in the lockfile, the content's checksum is verified
// against the pin on both the cache and the network path; a mismatch is a fatal
// ChecksumMismatch. The pin itself is written only for a ref that has none, or
// when opts.Repin explicitly authorises moving it.
func Fetch(ctx context.Context, rawRef string, lock *LockFile, opts FetchOptions, u *ui.UI) (*RemoteModule, TrustLevel, error) {
	ref := ParseRef(rawRef)
	// Checked before the cache is consulted: an unhonourable version is a bad
	// reference, not a stale download, so a warm cache must not mask it.
	if err := ref.checkVersionSupported(); err != nil {
		return nil, ref.Trust, err
	}

	cachePath := moduleCachePath(rawRef)
	entry, inLock := lock.Registry[rawRef]

	// The cache is only read when a pin exists to check it against; --no-cache
	// bypasses the cache, never the verification.
	if !opts.NoCache && inLock {
		// Validate cache file exists and checksum matches.
		if data, err := os.ReadFile(cachePath); err == nil {
			sum := fmt.Sprintf("%x", sha256.Sum256(data))
			if sum != entry.SHA256 {
				return nil, ref.Trust, &ChecksumMismatch{
					Ref: rawRef, Pinned: entry.SHA256, Got: sum, FromCache: true,
				}
			}
			mod, err := parseModule(data)
			return mod, ref.Trust, err
		}
		// Cache file missing despite lockfile entry — re-fetch below.
	}

	// Fetch from network.
	data, err := download(ctx, ref.FetchURL)
	if err != nil {
		return nil, ref.Trust, fmt.Errorf("fetch %s: %w", rawRef, err)
	}

	// A pinned ref is verified however it was reached. Re-pinning is the only
	// way past a mismatch, and it has to be asked for.
	sum := fmt.Sprintf("%x", sha256.Sum256(data))
	if inLock && entry.SHA256 != sum && !opts.Repin {
		return nil, ref.Trust, &ChecksumMismatch{
			Ref: rawRef, Pinned: entry.SHA256, Got: sum,
		}
	}

	// Validate downloaded bytes before recording or caching them. In update
	// mode an earlier ref may already need persisting if this one fails, so a
	// malformed module must never enter that accumulated lock state.
	mod, err := parseModule(data)
	if err != nil {
		return nil, ref.Trust, err
	}

	// A ref with no entry yet is a first pin, not a re-pin, so it needs no
	// authorisation. --check retains the proposed pin only in memory for
	// reporting; UpdatePins separately skips SaveLock.
	if !inLock || opts.Repin {
		lock.Registry[rawRef] = LockEntry{
			SHA256:    sum,
			FetchedAt: time.Now().UTC(),
			URL:       ref.FetchURL,
		}
	}

	// Every accepted network response is useful cache content, including a
	// pinned response fetched because its cache entry was missing. UpdatePins
	// stages these bytes until after the matching pins are durable; otherwise a
	// failed write can leave old cache bytes next to a newly persisted pin.
	if !opts.NoWrite {
		if opts.deferredCacheWrites != nil {
			opts.deferredCacheWrites[cachePath] = data
		} else if err := writeCacheFile(cachePath, data); err != nil {
			// Non-fatal: we have the data in memory.
			u.Warn(fmt.Sprintf("could not cache registry module: %v", err))
		}
	}

	return mod, ref.Trust, nil
}

// UpdatePins re-fetches every registry ref used by cfg, bypassing the cache,
// and is the only path allowed to move a pin. It reports every ref's old and
// new checksum before writing: because a bare ref expands to the mutable @main,
// upstream changing is routine, so "never silent" has to mean reported rather
// than refused — a flag in the routine path would only become muscle memory.
//
// With checkOnly it writes nothing and returns an error when any ref drifted,
// which is the CI mode: report and signal through the exit status.
func UpdatePins(ctx context.Context, cfg config.Config, lockPath string, checkOnly bool, u *ui.UI) error {
	lock, err := LoadLock(lockPath)
	if err != nil {
		return fmt.Errorf("load lockfile: %w", err)
	}

	active := CollectActiveRefs(cfg)
	refs := make([]string, 0, len(active))
	for ref := range active {
		refs = append(refs, ref)
	}
	// Map order would make the report jump around between otherwise identical runs.
	sort.Strings(refs)

	deferredCacheWrites := make(map[string][]byte)
	opts := FetchOptions{
		NoCache:            true,
		NoWrite:            checkOnly,
		Repin:              !checkOnly,
		deferredCacheWrites: deferredCacheWrites,
	}
	drifted := 0
	var repinned []string

	// persist publishes a changed pin and its cache in a crash-safe order:
	// remove the old cache, save the new pin, then install the new cache. A
	// process interrupted at either boundary leaves a missing cache, which the
	// normal fetch path safely restores from the network and verifies. Unchanged
	// responses can be cached first because their bytes already match the
	// persisted pin.
	persist := func() error {
		if checkOnly {
			return nil
		}

		repinnedPaths := make(map[string]struct{}, len(repinned))
		for _, ref := range repinned {
			repinnedPaths[moduleCachePath(ref)] = struct{}{}
		}
		for _, ref := range refs {
			path := moduleCachePath(ref)
			data, ok := deferredCacheWrites[path]
			if _, changed := repinnedPaths[path]; !ok || changed {
				continue
			}
			if err := writeCacheFile(path, data); err != nil {
				u.Warn(fmt.Sprintf("could not cache registry module: %v", err))
			}
		}

		if len(repinned) == 0 {
			return nil
		}
		for _, ref := range repinned {
			if err := os.Remove(moduleCachePath(ref)); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("discard old cache for %s: %w", ref, err)
			}
		}
		if err := SaveLock(lockPath, lock); err != nil {
			return fmt.Errorf("save lockfile: %w", err)
		}
		for _, ref := range repinned {
			path := moduleCachePath(ref)
			if err := writeCacheFile(path, deferredCacheWrites[path]); err != nil {
				// The durable pin and missing cache are coherent; the next fetch
				// restores and verifies the same bytes.
				u.Warn(fmt.Sprintf("could not cache registry module: %v", err))
			}
		}
		return nil
	}

	for _, ref := range refs {
		pinned := lock.Registry[ref].SHA256

		_, _, err := Fetch(ctx, ref, lock, opts, u)
		var mismatch *ChecksumMismatch
		if errors.As(err, &mismatch) {
			// Only reachable under checkOnly: an update authorises the re-pin.
			drifted++
			u.Warn(fmt.Sprintf("%-24s %s -> %s  DRIFT", ref, shortSum(mismatch.Pinned), shortSum(mismatch.Got)))
			continue
		}
		if err != nil {
			// The fetch failure is the more useful error to return, so a failure
			// to persist is only reported.
			if perr := persist(); perr != nil {
				u.Warn(perr.Error())
			}
			return err
		}

		switch newPin := lock.Registry[ref].SHA256; {
		case pinned == "":
			repinned = append(repinned, ref)
			u.Info(fmt.Sprintf("%-24s new pin %s", ref, shortSum(newPin)))
		case newPin != pinned:
			repinned = append(repinned, ref)
			u.Info(fmt.Sprintf("%-24s %s -> %s", ref, shortSum(pinned), shortSum(newPin)))
		default:
			u.Info(fmt.Sprintf("%-24s unchanged", ref))
		}
	}

	if checkOnly {
		if drifted > 0 {
			return fmt.Errorf(
				"registry: %d module(s) drifted from their pins; nothing was written — run 'dotular registry update' to review and accept",
				drifted,
			)
		}
		u.Success(fmt.Sprintf("%d registry module(s) match their pins", len(refs)))
		return nil
	}

	if err := persist(); err != nil {
		return err
	}
	u.Success(fmt.Sprintf("updated %d pin(s) of %d registry module(s) checked", len(repinned), len(refs)))
	return nil
}

// shortSum abbreviates a checksum for reports; the lockfile keeps the full one.
func shortSum(sum string) string {
	const abbrev = 8
	if len(sum) <= abbrev {
		return sum
	}
	return sum[:abbrev] + ".."
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

func moduleCachePath(rawRef string) string {
	safe := strings.NewReplacer(
		"/", "_", "@", "_", ":", "_", ".", "_",
	).Replace(rawRef)
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "dotular", "registry", safe+".yaml")
}

// writeCacheFile writes a cache entry atomically, mirroring SaveLock. A cache
// file is only ever meaningful next to the pin it hashes to, so a write that
// could not complete must leave no trace: a torn file — ENOSPC, a kill mid-write
// — would hash to neither the old pin nor the new one, and Fetch would report
// dotular's own interrupted write as a tampered cache. The temp file has to live
// in the target's directory, because a rename across filesystems is not atomic.
func writeCacheFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
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
