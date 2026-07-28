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
	// Repin permits an existing pin to be moved to the bytes just fetched. Only
	// UpdatePins sets it: everywhere else a ref whose content no longer matches
	// its pin is refused, which is what makes an apply reproducible.
	Repin bool
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

	// Pin and cache together, under one condition. A ref with no entry yet is a
	// first pin, not a re-pin, so it needs no authorisation. The cache is only
	// ever read alongside a pin (see the guard above), so caching bytes we are
	// not authorised to pin would at best be dead weight and at worst leave the
	// two disagreeing — and --check, which authorises neither, must touch
	// nothing at all.
	if !inLock || opts.Repin {
		lock.Registry[rawRef] = LockEntry{
			SHA256:    sum,
			FetchedAt: time.Now().UTC(),
			URL:       ref.FetchURL,
		}
		if err := writeCacheFile(cachePath, data); err != nil {
			// Non-fatal: we have the data in memory.
			u.Warn(fmt.Sprintf("could not cache registry module: %v", err))
		}
	}

	mod, err := parseModule(data)
	return mod, ref.Trust, err
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

	opts := FetchOptions{NoCache: true, Repin: !checkOnly}
	drifted, written := 0, 0

	// persist writes the pins accumulated so far, and runs on the way out of the
	// failure path as well as the success path. Fetch has already written the
	// cache file for every ref it pinned, so abandoning those pins would leave
	// the cache holding new bytes that the lockfile still pins to the old
	// checksum — the next apply would hash the cache, disagree with the pin, and
	// report dotular's own partial update as a tampered cache. Partial progress
	// is already this tool's semantic (ApplyAll leaves earlier modules applied);
	// an incoherent disk is not.
	persist := func() error {
		if checkOnly || written == 0 {
			return nil
		}
		if err := SaveLock(lockPath, lock); err != nil {
			return fmt.Errorf("save lockfile: %w", err)
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
			written++
			u.Info(fmt.Sprintf("%-24s new pin %s", ref, shortSum(newPin)))
		case newPin != pinned:
			written++
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
	u.Success(fmt.Sprintf("updated %d pin(s) of %d registry module(s) checked", written, len(refs)))
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
