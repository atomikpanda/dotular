package registry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atomikpanda/dotular/internal/config"
	"github.com/atomikpanda/dotular/internal/ui"
)

const testModuleYAML = "name: test-mod\nitems:\n  - package: neovim\n    via: brew\n"

const registryLockContentionWindow = 250 * time.Millisecond

func testModuleSum() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(testModuleYAML)))
}

// serveTestModule starts a TLS server running handler and points httpClient at
// it, returning a ref that Fetch will resolve to that server. TLS rather than
// plain HTTP because Ref.FetchURL is always https:// for a non-github host, and
// srv.Client() is the only client that trusts the server's generated cert.
func serveTestModule(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	swapClient(t, srv.Client())
	return strings.TrimPrefix(srv.URL, "https://") + "/module.yaml"
}

func swapClient(t *testing.T, c *http.Client) {
	t.Helper()
	prev := httpClient
	httpClient = c
	t.Cleanup(func() { httpClient = prev })
}

func failCacheTempWrites(t *testing.T) func() {
	t.Helper()
	previous := createCacheTemp
	createCacheTemp = func(string, string) (*os.File, error) {
		return nil, errors.New("cache temp creation blocked")
	}
	restore := func() { createCacheTemp = previous }
	t.Cleanup(restore)
	return restore
}

// forbidNetwork makes any HTTP request fail the test, so a "cache hit" claim is
// proven rather than assumed.
func forbidNetwork(t *testing.T) {
	t.Helper()
	swapClient(t, &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("unexpected network request to %s; the cache should have satisfied this fetch", r.URL)
		return nil, fmt.Errorf("network forbidden")
	})})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newTestLock(t *testing.T) *LockFile {
	t.Helper()
	lock, err := LoadLock(filepath.Join(t.TempDir(), "dotular.lock.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return lock
}

func fetchForTest(t *testing.T, ref string, lock *LockFile, opts FetchOptions) (*RemoteModule, TrustLevel, error) {
	t.Helper()
	return Fetch(context.Background(), ref, lock, opts, ui.New(&bytes.Buffer{}, &bytes.Buffer{}))
}

// TestFetchFromNetwork covers the integrity gate on the network path: the
// checksum recorded in the lockfile must be honoured, and a bad response must
// never be pinned.
func TestFetchFromNetwork(t *testing.T) {
	ok := func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, testModuleYAML) }

	tests := []struct {
		name     string
		handler  http.HandlerFunc
		lockSum  string // "" means no existing lockfile entry
		opts     FetchOptions
		wantErr  string
		wantName string
	}{
		{
			name:     "unpinned ref is fetched and pinned",
			handler:  ok,
			wantName: "test-mod",
		},
		{
			name:     "checksum matching the lockfile is accepted",
			handler:  ok,
			lockSum:  testModuleSum(),
			wantName: "test-mod",
		},
		{
			name:    "checksum disagreeing with the lockfile is fatal",
			handler: ok,
			lockSum: "0000000000000000000000000000000000000000000000000000000000000000",
			wantErr: "checksum mismatch",
		},
		{
			name:    "non-200 response is an error",
			handler: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) },
			wantErr: "HTTP 404",
		},
		{
			// --no-cache bypasses the disk cache, not the integrity check: the
			// pin still decides whether the fetched bytes are acceptable.
			name:    "--no-cache still verifies against the pin",
			handler: ok,
			lockSum: "0000000000000000000000000000000000000000000000000000000000000000",
			opts:    FetchOptions{NoCache: true},
			wantErr: "checksum mismatch",
		},
		{
			name:     "an explicit re-pin accepts the new checksum",
			handler:  ok,
			lockSum:  "0000000000000000000000000000000000000000000000000000000000000000",
			opts:     FetchOptions{NoCache: true, Repin: true},
			wantName: "test-mod",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref := serveTestModule(t, tt.handler)
			lock := newTestLock(t)
			if tt.lockSum != "" {
				lock.Registry[ref] = LockEntry{SHA256: tt.lockSum}
			}

			mod, _, err := fetchForTest(t, ref, lock, tt.opts)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Fetch() = nil error, want one containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Fetch() error = %q, want it to contain %q", err, tt.wantErr)
				}
				if got, ok := lock.Registry[ref]; ok && got.SHA256 == testModuleSum() {
					t.Error("a rejected fetch must not be pinned in the lockfile")
				}
				return
			}

			if err != nil {
				t.Fatalf("Fetch() error = %v", err)
			}
			if mod.Name != tt.wantName {
				t.Errorf("module name = %q, want %q", mod.Name, tt.wantName)
			}
			if got := lock.Registry[ref].SHA256; got != testModuleSum() {
				t.Errorf("lockfile checksum = %q, want %q", got, testModuleSum())
			}
		})
	}
}

// A body that ends before its declared Content-Length must be an error. A
// truncated module is still valid YAML — it simply has fewer items — so if the
// read error is swallowed the partial content gets pinned in the lockfile as
// authoritative and every later "verified" fetch agrees with the corruption.
func TestFetchRejectsTruncatedBody(t *testing.T) {
	ref := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(testModuleYAML)))
		fmt.Fprint(w, testModuleYAML[:12])
	})

	lock := newTestLock(t)
	_, _, err := fetchForTest(t, ref, lock, FetchOptions{})
	if err == nil {
		t.Fatal("Fetch() = nil error, want an error for a body shorter than its Content-Length")
	}
	if len(lock.Registry) != 0 {
		t.Errorf("lockfile pinned %d entries; a truncated download must never be pinned", len(lock.Registry))
	}
}

func TestFetchCacheHitSkipsNetwork(t *testing.T) {
	ref := "cache.example/hit/module"
	if err := writeCacheFile(moduleCachePath(ref), []byte(testModuleYAML)); err != nil {
		t.Fatal(err)
	}
	forbidNetwork(t)

	lock := newTestLock(t)
	lock.Registry[ref] = LockEntry{SHA256: testModuleSum()}

	mod, _, err := fetchForTest(t, ref, lock, FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if mod.Name != "test-mod" {
		t.Errorf("module name = %q, want %q", mod.Name, "test-mod")
	}
}

// The cache-hit path must report the ref's real trust level. Returning
// parseModule's hardcoded External made official modules print an [external]
// warning on the common path, which trains users to ignore the only gate there
// is on running remote module definitions.
func TestFetchCacheHitReportsRefTrust(t *testing.T) {
	tests := []struct {
		ref  string
		want TrustLevel
	}{
		{"github.com/atomikpanda/dotular/modules/wezterm@main", Official},
		{"wezterm", Official},
		{"github.com/someone/else/modules/wezterm@main", GitHub},
		{"example.com/modules/wezterm", External},
	}

	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			if err := writeCacheFile(moduleCachePath(tt.ref), []byte(testModuleYAML)); err != nil {
				t.Fatal(err)
			}
			forbidNetwork(t)

			lock := newTestLock(t)
			lock.Registry[tt.ref] = LockEntry{SHA256: testModuleSum()}

			_, trust, err := fetchForTest(t, tt.ref, lock, FetchOptions{})
			if err != nil {
				t.Fatalf("Fetch() error = %v", err)
			}
			if trust != tt.want {
				t.Errorf("trust = %s, want %s", trust, tt.want)
			}
		})
	}
}

func TestFetchCacheHitRejectsTamperedCache(t *testing.T) {
	ref := "cache.example/tampered/module"
	if err := writeCacheFile(moduleCachePath(ref), []byte("name: tampered\n")); err != nil {
		t.Fatal(err)
	}
	forbidNetwork(t)

	lock := newTestLock(t)
	lock.Registry[ref] = LockEntry{SHA256: testModuleSum()}

	_, _, err := fetchForTest(t, ref, lock, FetchOptions{})
	if err == nil {
		t.Fatal("Fetch() = nil error, want a checksum mismatch for a tampered cache file")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("Fetch() error = %q, want a checksum mismatch", err)
	}
}

// Without a lockfile entry the cache is not consulted at all — there is nothing
// to verify it against, so trusting it would be trusting unpinned bytes.
func TestFetchIgnoresCacheWithoutLockEntry(t *testing.T) {
	ref := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, testModuleYAML)
	})
	if err := writeCacheFile(moduleCachePath(ref), []byte("name: stale-cache\n")); err != nil {
		t.Fatal(err)
	}

	mod, _, err := fetchForTest(t, ref, newTestLock(t), FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if mod.Name != "test-mod" {
		t.Errorf("module name = %q, want the network copy %q", mod.Name, "test-mod")
	}
}

// A legacy cache entry is usable only by a ref whose persisted pin matches it.
func TestFetchMigratesOnlyMatchingLegacyCacheEntry(t *testing.T) {
	firstRef, collidingRef := "cache.example/a.b/foo", "cache.example/a/b.foo"
	legacyPath := legacyModuleCachePath(firstRef)
	if got := legacyModuleCachePath(collidingRef); got != legacyPath {
		t.Fatalf("legacy paths do not collide: %q != %q", got, legacyPath)
	}
	if err := writeCacheFile(legacyPath, []byte(oldModuleYAML)); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{firstRef, collidingRef} {
		_ = os.Remove(moduleCachePath(ref))
	}
	t.Cleanup(func() {
		_ = os.Remove(legacyPath)
		_ = os.Remove(moduleCachePath(firstRef))
		_ = os.Remove(moduleCachePath(collidingRef))
	})

	lock := &LockFile{Registry: map[string]LockEntry{
		firstRef:     {SHA256: oldModuleSum(), URL: "https://" + firstRef},
		collidingRef: {SHA256: testModuleSum(), URL: "https://" + collidingRef},
	}}
	swapClient(t, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network unavailable")
	})})

	mod, _, err := fetchForTest(t, firstRef, lock, FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch() matching legacy entry = %v", err)
	}
	if mod.Name != "old-mod" {
		t.Errorf("legacy module name = %q, want %q", mod.Name, "old-mod")
	}
	if _, err := os.Stat(moduleCachePath(firstRef)); err != nil {
		t.Fatalf("hashed cache was not migrated: %v", err)
	}

	_, _, err = fetchForTest(t, collidingRef, lock, FetchOptions{})
	if err == nil {
		t.Fatal("Fetch() colliding legacy entry = nil error, want network fallback")
	}
	var mismatch *ChecksumMismatch
	if errors.As(err, &mismatch) {
		t.Fatalf("colliding legacy entry was treated as this ref's tampered cache: %v", err)
	}
}

// A lockfile entry whose cache file has been evicted must fall back to the
// network and still be verified against the pin.
func TestFetchRefetchesWhenCacheFileMissing(t *testing.T) {
	ref := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, testModuleYAML)
	})
	os.Remove(moduleCachePath(ref))

	lock := newTestLock(t)
	lock.Registry[ref] = LockEntry{SHA256: testModuleSum()}

	mod, _, err := fetchForTest(t, ref, lock, FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if mod.Name != "test-mod" {
		t.Errorf("module name = %q, want %q", mod.Name, "test-mod")
	}
}

func TestFetchCachesAcceptedPinnedNetworkResponse(t *testing.T) {
	ref := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, testModuleYAML)
	})
	cachePath := moduleCachePath(ref)
	if err := os.Remove(cachePath); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(cachePath) })

	pinned := LockEntry{
		SHA256:    testModuleSum(),
		FetchedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		URL:       "https://" + ref,
	}
	lock := newTestLock(t)
	lock.Registry[ref] = pinned

	if _, _, err := fetchForTest(t, ref, lock, FetchOptions{}); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if got := lock.Registry[ref]; got != pinned {
		t.Errorf("accepted fetch moved pin to %+v, want it left at %+v", got, pinned)
	}
	cached, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read populated cache: %v", err)
	}
	if !bytes.Equal(cached, []byte(testModuleYAML)) {
		t.Errorf("cached bytes = %q, want %q", cached, testModuleYAML)
	}
}

// The pin records what was approved, so content that disagrees with it must
// leave the entry exactly as it was — URL and timestamp included, not just the
// checksum.
func TestFetchRefusesDriftAndLeavesPinInPlace(t *testing.T) {
	ref := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, testModuleYAML)
	})
	pinned := LockEntry{
		SHA256:    "0000000000000000000000000000000000000000000000000000000000000000",
		FetchedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		URL:       "https://" + ref,
	}
	lock := newTestLock(t)
	lock.Registry[ref] = pinned

	_, _, err := fetchForTest(t, ref, lock, FetchOptions{NoCache: true})

	var mismatch *ChecksumMismatch
	if !errors.As(err, &mismatch) {
		t.Fatalf("Fetch() error = %v, want a *ChecksumMismatch", err)
	}
	if mismatch.Got != testModuleSum() {
		t.Errorf("mismatch.Got = %q, want the fetched content's sum %q", mismatch.Got, testModuleSum())
	}
	if !strings.Contains(err.Error(), "dotular registry update") {
		t.Errorf("error = %q, want it to name the command that reviews and moves the pin", err)
	}
	if got := lock.Registry[ref]; got != pinned {
		t.Errorf("pin moved to %+v, want it left at %+v", got, pinned)
	}
}

const stalePin = "0000000000000000000000000000000000000000000000000000000000000000"

// driftedSetup serves the test module and writes a lockfile pinning ref to a
// checksum it does not have, i.e. the state after an upstream commit.
func driftedSetup(t *testing.T) (cfg config.Config, ref, lockPath string) {
	t.Helper()
	ref = serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, testModuleYAML)
	})
	cfg = config.Config{Modules: []config.Module{{Name: "drifted", From: ref}}}
	lockPath = filepath.Join(t.TempDir(), "dotular.lock.yaml")
	if err := SaveLock(lockPath, &LockFile{Registry: map[string]LockEntry{
		ref: {
			SHA256:    stalePin,
			FetchedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			URL:       "https://" + ref,
		},
	}}); err != nil {
		t.Fatal(err)
	}
	return cfg, ref, lockPath
}

// Moving the pin is registry update's job — upstream is a mutable branch, so a
// changed checksum is routine — but it has to say what it moved.
func TestUpdatePinsReportsAndWritesDrift(t *testing.T) {
	cfg, ref, lockPath := driftedSetup(t)

	var stdout bytes.Buffer
	u := ui.New(&stdout, &bytes.Buffer{})
	if err := UpdatePins(context.Background(), cfg, lockPath, false, u); err != nil {
		t.Fatalf("UpdatePins() error = %v", err)
	}

	report := stdout.String()
	for _, want := range []string{ref, stalePin, testModuleSum()} {
		if !strings.Contains(report, want) {
			t.Errorf("report = %q, want it to contain %q", report, want)
		}
	}

	lock, err := LoadLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := lock.Registry[ref].SHA256; got != testModuleSum() {
		t.Errorf("pin = %q, want it moved to %q", got, testModuleSum())
	}
}

func TestUpdatePinsReportsFullChecksumsForNewAndUnchangedRefs(t *testing.T) {
	ref := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, testModuleYAML)
	})
	tests := []struct {
		name   string
		pinned bool
	}{
		{name: "new"},
		{name: "unchanged", pinned: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lockPath := filepath.Join(t.TempDir(), "dotular.lock.yaml")
			if tt.pinned {
				if err := SaveLock(lockPath, &LockFile{Registry: map[string]LockEntry{
					ref: {SHA256: testModuleSum(), URL: "https://" + ref},
				}}); err != nil {
					t.Fatal(err)
				}
			}

			cfg := config.Config{Modules: []config.Module{{Name: tt.name, From: ref}}}
			var stdout bytes.Buffer
			if err := UpdatePins(
				context.Background(),
				cfg,
				lockPath,
				false,
				ui.New(&stdout, &bytes.Buffer{}),
			); err != nil {
				t.Fatal(err)
			}
			report := stdout.String()
			if tt.pinned {
				if strings.Count(report, testModuleSum()) < 2 || !strings.Contains(report, "unchanged") {
					t.Errorf("unchanged report = %q, want pinned and fetched full checksums", report)
				}
			} else if !strings.Contains(report, "(none)") ||
				!strings.Contains(report, testModuleSum()) ||
				!strings.Contains(report, "NEW") {
				t.Errorf("new-pin report = %q, want absent old pin and full fetched checksum", report)
			}
		})
	}
}

// --check is the CI mode: report the drift, write nothing at all, fail.
func TestUpdatePinsCheckOnlyReportsWithoutWriting(t *testing.T) {
	cfg, ref, lockPath := driftedSetup(t)
	before, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	u := ui.New(&bytes.Buffer{}, &stderr)
	if err := UpdatePins(context.Background(), cfg, lockPath, true, u); err == nil {
		t.Fatal("UpdatePins(check) = nil error, want a non-zero exit for a drifted ref")
	}
	if !strings.Contains(stderr.String(), ref) {
		t.Errorf("drift report = %q, want it to name %s", stderr.String(), ref)
	}

	after, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("--check wrote to the lockfile:\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestUpdatePinsCheckOnlyPassesWithoutDrift(t *testing.T) {
	cfg, ref, lockPath := driftedSetup(t)
	if err := SaveLock(lockPath, &LockFile{Registry: map[string]LockEntry{
		ref: {SHA256: testModuleSum(), FetchedAt: time.Now().UTC(), URL: "https://" + ref},
	}}); err != nil {
		t.Fatal(err)
	}

	u := ui.New(&bytes.Buffer{}, &bytes.Buffer{})
	if err := UpdatePins(context.Background(), cfg, lockPath, true, u); err != nil {
		t.Fatalf("UpdatePins(check) error = %v, want success when every ref matches its pin", err)
	}
}

func TestUpdatePinsCheckOnlyRejectsUnpinnedRefWithoutWriting(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv("LocalAppData", cacheRoot)
	ref := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, testModuleYAML)
	})
	cfg := config.Config{Modules: []config.Module{{Name: "new", From: ref}}}
	lockPath := filepath.Join(t.TempDir(), "dotular.lock.yaml")
	cachePath := moduleCachePath(ref)
	if err := os.Remove(cachePath); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(cachePath) })

	var stderr bytes.Buffer
	u := ui.New(&bytes.Buffer{}, &stderr)
	err := UpdatePins(context.Background(), cfg, lockPath, true, u)
	if err == nil || !strings.Contains(err.Error(), "drifted") {
		t.Fatalf("UpdatePins(check) error = %v, want unpinned ref to fail as drift", err)
	}
	if !strings.Contains(stderr.String(), "(none)") {
		t.Errorf("UpdatePins(check) report = %q, want absent pin to be explicit", stderr.String())
	}
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Errorf("--check cache stat error = %v, want cache file not to exist", err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("--check lockfile stat error = %v, want lockfile not to exist", err)
	}
	entries, err := os.ReadDir(cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("--check created coordination files under the user cache: %v", entries)
	}
}

// --check must be inert on disk. The cache is the half that was silently
// written: --check never reads it, so nothing else would notice.
func TestUpdatePinsCheckOnlyLeavesCacheUntouched(t *testing.T) {
	base := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, testModuleYAML)
	})
	host := strings.TrimSuffix(base, "/module.yaml")
	driftedRef, matchingRef := host+"/drifted.yaml", host+"/matching.yaml"

	// Sentinel bytes rather than the real module: an overwrite with exactly what
	// the server serves would be invisible to a byte comparison, and the
	// matching ref is the one that was being rewritten. --check does not read
	// the cache, so its contents are free to be nonsense.
	cacheBefore := make(map[string][]byte, 2)
	for _, ref := range []string{driftedRef, matchingRef} {
		sentinel := []byte("name: sentinel " + ref + "\n")
		if err := writeCacheFile(moduleCachePath(ref), sentinel); err != nil {
			t.Fatal(err)
		}
		cacheBefore[ref] = sentinel
	}

	cfg := config.Config{Modules: []config.Module{
		{Name: "drifted", From: driftedRef},
		{Name: "matching", From: matchingRef},
	}}
	lockPath := filepath.Join(t.TempDir(), "dotular.lock.yaml")
	if err := SaveLock(lockPath, &LockFile{Registry: map[string]LockEntry{
		driftedRef:  {SHA256: stalePin, URL: "https://" + driftedRef},
		matchingRef: {SHA256: testModuleSum(), URL: "https://" + matchingRef},
	}}); err != nil {
		t.Fatal(err)
	}
	lockBefore, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}

	u := ui.New(&bytes.Buffer{}, &bytes.Buffer{})
	if err := UpdatePins(context.Background(), cfg, lockPath, true, u); err == nil {
		t.Fatal("UpdatePins(check) = nil error, want a non-zero exit for the drifted ref")
	}

	for ref, want := range cacheBefore {
		got, err := os.ReadFile(moduleCachePath(ref))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("--check rewrote the cache for %s:\nbefore: %s\nafter:  %s", ref, want, got)
		}
	}
	lockAfter, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(lockBefore, lockAfter) {
		t.Errorf("--check rewrote the lockfile:\nbefore: %s\nafter:  %s", lockBefore, lockAfter)
	}
}

func TestUpdatePinsKeepsDistinctCachesForCollidingRefs(t *testing.T) {
	base := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/a.b/foo":
			fmt.Fprint(w, oldModuleYAML)
		case "/a/b.foo":
			fmt.Fprint(w, testModuleYAML)
		default:
			t.Errorf("unexpected registry path %q", r.URL.Path)
		}
	})
	host := strings.TrimSuffix(base, "/module.yaml")
	firstRef, secondRef := host+"/a.b/foo", host+"/a/b.foo"
	cfg := config.Config{Modules: []config.Module{
		{Name: "first", From: firstRef},
		{Name: "second", From: secondRef},
	}}
	lockPath := filepath.Join(t.TempDir(), "dotular.lock.yaml")
	t.Cleanup(func() {
		_ = os.Remove(moduleCachePath(firstRef))
		_ = os.Remove(moduleCachePath(secondRef))
	})

	u := ui.New(&bytes.Buffer{}, &bytes.Buffer{})
	if err := UpdatePins(context.Background(), cfg, lockPath, false, u); err != nil {
		t.Fatal(err)
	}
	lock, err := LoadLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}

	forbidNetwork(t)
	first, _, err := fetchForTest(t, firstRef, lock, FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch(%q) = %v", firstRef, err)
	}
	second, _, err := fetchForTest(t, secondRef, lock, FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch(%q) = %v", secondRef, err)
	}
	if first.Name != "old-mod" || second.Name != "test-mod" {
		t.Errorf("cached modules = %q, %q; want %q, %q", first.Name, second.Name, "old-mod", "test-mod")
	}
}

// A ref failing partway through must not leave the cache ahead of the lockfile:
// the next apply would hash the new cached bytes against the old pin and report
// dotular's own partial update as a tampered cache.
func TestUpdatePinsKeepsCacheAndLockfileInStepOnFailure(t *testing.T) {
	base := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "z-broken.yaml") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, testModuleYAML)
	})
	host := strings.TrimSuffix(base, "/module.yaml")
	// Refs are fetched in sorted order, so the failure lands after a ref that
	// has already been pinned and cached.
	okRef, brokenRef := host+"/a-ok.yaml", host+"/z-broken.yaml"

	cfg := config.Config{Modules: []config.Module{
		{Name: "ok", From: okRef},
		{Name: "broken", From: brokenRef},
	}}
	lockPath := filepath.Join(t.TempDir(), "dotular.lock.yaml")
	if err := SaveLock(lockPath, &LockFile{Registry: map[string]LockEntry{
		okRef: {
			SHA256:    stalePin,
			FetchedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			URL:       "https://" + okRef,
		},
	}}); err != nil {
		t.Fatal(err)
	}

	u := ui.New(&bytes.Buffer{}, &bytes.Buffer{})
	if err := UpdatePins(context.Background(), cfg, lockPath, false, u); err == nil {
		t.Fatal("UpdatePins() = nil error, want the transport failure to propagate")
	}

	cached, err := os.ReadFile(moduleCachePath(okRef))
	if err != nil {
		t.Fatal(err)
	}
	lock, err := LoadLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := lock.Registry[okRef].SHA256; got != fmt.Sprintf("%x", sha256.Sum256(cached)) {
		t.Errorf("persisted pin = %q, want it to agree with the cached bytes", got)
	}

	// The regression guard: an ordinary cached fetch must not now cry tamper.
	forbidNetwork(t)
	if _, _, err := fetchForTest(t, okRef, lock, FetchOptions{}); err != nil {
		t.Errorf("Fetch() after a partial update = %v, want the cache and the pin to agree", err)
	}
}

func TestUpdatePinsDoesNotPersistMalformedModulePin(t *testing.T) {
	const malformedModuleYAML = "name: [\n"
	base := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "z-malformed.yaml") {
			fmt.Fprint(w, malformedModuleYAML)
			return
		}
		fmt.Fprint(w, testModuleYAML)
	})
	host := strings.TrimSuffix(base, "/module.yaml")
	goodRef, malformedRef := host+"/a-good.yaml", host+"/z-malformed.yaml"
	malformedCachePath := moduleCachePath(malformedRef)
	if err := os.Remove(malformedCachePath); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(moduleCachePath(goodRef))
		_ = os.Remove(malformedCachePath)
	})

	cfg := config.Config{Modules: []config.Module{
		{Name: "good", From: goodRef},
		{Name: "malformed", From: malformedRef},
	}}
	lockPath := filepath.Join(t.TempDir(), "dotular.lock.yaml")
	if err := SaveLock(lockPath, &LockFile{Registry: map[string]LockEntry{
		goodRef:      {SHA256: stalePin, URL: "https://" + goodRef},
		malformedRef: {SHA256: stalePin, URL: "https://" + malformedRef},
	}}); err != nil {
		t.Fatal(err)
	}

	u := ui.New(&bytes.Buffer{}, &bytes.Buffer{})
	if err := UpdatePins(context.Background(), cfg, lockPath, false, u); err == nil {
		t.Fatal("UpdatePins() = nil error, want malformed YAML to fail")
	}

	lock, err := LoadLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := lock.Registry[goodRef].SHA256; got != testModuleSum() {
		t.Errorf("prior valid pin = %q, want persisted progress %q", got, testModuleSum())
	}
	if got := lock.Registry[malformedRef].SHA256; got != stalePin {
		t.Errorf("malformed module pin = %q, want original pin %q", got, stalePin)
	}
	if _, err := os.Stat(malformedCachePath); !os.IsNotExist(err) {
		t.Errorf("malformed cache stat error = %v, want cache file not to exist", err)
	}
}

// Updating a pin and its cache is a two-file transaction. If the cache cannot
// be replaced, the new pin must never be persisted beside the old cache bytes;
// a missing cache is safe because Fetch restores it and verifies the pin.
func TestUpdatePinsKeepsPinAndCacheCoherentWhenCacheWriteFails(t *testing.T) {
	ref := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, testModuleYAML)
	})
	cfg := config.Config{Modules: []config.Module{{Name: "changed", From: ref}}}
	lockPath := filepath.Join(t.TempDir(), "dotular.lock.yaml")
	if err := SaveLock(lockPath, &LockFile{Registry: map[string]LockEntry{
		ref: {SHA256: oldModuleSum(), URL: "https://" + ref},
	}}); err != nil {
		t.Fatal(err)
	}

	cachePath := moduleCachePath(ref)
	if err := writeCacheFile(cachePath, []byte(oldModuleYAML)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(cachePath) })
	restoreCacheWrites := failCacheTempWrites(t)

	u := ui.New(&bytes.Buffer{}, &bytes.Buffer{})
	if err := UpdatePins(context.Background(), cfg, lockPath, false, u); err != nil {
		t.Fatalf("UpdatePins() = %v, want the cache failure to remain non-fatal", err)
	}

	lock, err := LoadLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := lock.Registry[ref].SHA256; got != testModuleSum() {
		t.Errorf("persisted pin = %q, want accepted checksum %q", got, testModuleSum())
	}
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Errorf("cache stat error = %v, want old cache removed rather than paired with the new pin", err)
	}

	restoreCacheWrites()
	mod, _, err := fetchForTest(t, ref, lock, FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch() after failed cache write = %v, want a verified network recovery", err)
	}
	if mod.Name != "test-mod" {
		t.Errorf("module name = %q, want %q", mod.Name, "test-mod")
	}
}

func TestUpdatePinsDiscardsStaleCacheWhenUnchangedReplacementFails(t *testing.T) {
	ref := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, testModuleYAML)
	})
	cfg := config.Config{Modules: []config.Module{{Name: "unchanged", From: ref}}}
	lockPath := filepath.Join(t.TempDir(), "dotular.lock.yaml")
	if err := SaveLock(lockPath, &LockFile{Registry: map[string]LockEntry{
		ref: {SHA256: testModuleSum(), URL: "https://" + ref},
	}}); err != nil {
		t.Fatal(err)
	}

	cachePath := moduleCachePath(ref)
	if err := writeCacheFile(cachePath, []byte(oldModuleYAML)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(cachePath) })
	restoreCacheWrites := failCacheTempWrites(t)

	var stderr bytes.Buffer
	u := ui.New(&bytes.Buffer{}, &stderr)
	if err := UpdatePins(context.Background(), cfg, lockPath, false, u); err != nil {
		t.Fatalf("UpdatePins() = %v, want recoverable cache failure to remain non-fatal", err)
	}
	if report := stderr.String(); !strings.Contains(report, "cache temp creation blocked") ||
		!strings.Contains(report, "discarded previous cache entry") {
		t.Errorf("UpdatePins() warning = %q, want original write error and cache invalidation", report)
	}
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Errorf("cache stat error = %v, want stale cache removed", err)
	}

	restoreCacheWrites()
	lock, err := LoadLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	mod, _, err := fetchForTest(t, ref, lock, FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch() after failed unchanged cache write = %v, want verified network recovery", err)
	}
	if mod.Name != "test-mod" {
		t.Errorf("module name = %q, want %q", mod.Name, "test-mod")
	}
}

func TestRegistryTransactionsSerializeWithUpdate(t *testing.T) {
	tests := []struct {
		name string
		run  func(context.Context, config.Config, string, string, *ui.UI) error
	}{
		{
			name: "update",
			run: func(ctx context.Context, cfg config.Config, lockPath, _ string, u *ui.UI) error {
				return UpdatePins(ctx, cfg, lockPath, false, u)
			},
		},
		{
			name: "check",
			run: func(ctx context.Context, cfg config.Config, lockPath, _ string, u *ui.UI) error {
				return UpdatePins(ctx, cfg, lockPath, true, u)
			},
		},
		{
			name: "resolve",
			run: func(ctx context.Context, cfg config.Config, _ string, configPath string, u *ui.UI) error {
				_, err := Resolve(ctx, cfg, configPath, false, u)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			firstRequest := make(chan struct{})
			releaseFirst := make(chan struct{})
			var calls atomic.Int32

			ref := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					close(firstRequest)
					<-releaseFirst
				}
				fmt.Fprint(w, testModuleYAML)
			})
			cfg := config.Config{Modules: []config.Module{{Name: "mutable", From: ref}}}
			configPath := filepath.Join(t.TempDir(), "dotular.yaml")
			lockPath := LockPath(configPath)
			if err := SaveLock(lockPath, &LockFile{Registry: map[string]LockEntry{
				ref: {SHA256: stalePin, URL: "https://" + ref},
			}}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Remove(moduleCachePath(ref)) })

			firstErr := make(chan error, 1)
			go func() {
				firstErr <- UpdatePins(
					context.Background(),
					cfg,
					lockPath,
					false,
					ui.New(&bytes.Buffer{}, &bytes.Buffer{}),
				)
			}()
			<-firstRequest

			secondStarted := make(chan struct{})
			secondErr := make(chan error, 1)
			go func() {
				close(secondStarted)
				secondErr <- tt.run(
					context.Background(),
					cfg,
					lockPath,
					configPath,
					ui.New(&bytes.Buffer{}, &bytes.Buffer{}),
				)
			}()
			<-secondStarted
			select {
			case err := <-secondErr:
				close(releaseFirst)
				t.Fatalf("second %s completed before update released the writer lock: %v", tt.name, err)
			case <-time.After(registryLockContentionWindow):
			}
			close(releaseFirst)

			if err := <-firstErr; err != nil {
				t.Fatalf("first UpdatePins() = %v", err)
			}
			if err := <-secondErr; err != nil {
				t.Fatalf("second %s = %v", tt.name, err)
			}

			lock, err := LoadLock(lockPath)
			if err != nil {
				t.Fatal(err)
			}
			cache, err := os.ReadFile(moduleCachePath(ref))
			if err != nil {
				t.Fatal(err)
			}
			if got, want := lock.Registry[ref].SHA256, fmt.Sprintf("%x", sha256.Sum256(cache)); got != want {
				t.Errorf("persisted pin = %q, cache checksum = %q", got, want)
			}
		})
	}
}

const oldModuleYAML = "name: old-mod\nitems: []\n"

func oldModuleSum() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(oldModuleYAML)))
}

// If the lockfile cannot be saved, the old pins stay on disk, so the cache files
// this run wrote have to go: Fetch treats a missing cache file as a re-fetch and
// verifies it against the pin it kept, whereas new bytes beside an old pin read
// as tampering. A ref reported "unchanged" keeps its cache — it still agrees.
func TestUpdatePinsDiscardsItsCacheWritesWhenTheLockfileCannotBeSaved(t *testing.T) {
	// Upstream serves the new module during the update, then the old one again,
	// which is what a reverted bad deploy looks like — and is what lets the
	// recovery fetch be checked for success rather than merely a different error.
	var reverted atomic.Bool
	base := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "drifted.yaml") && reverted.Load() {
			fmt.Fprint(w, oldModuleYAML)
			return
		}
		fmt.Fprint(w, testModuleYAML)
	})
	host := strings.TrimSuffix(base, "/module.yaml")
	driftedRef, unchangedRef := host+"/drifted.yaml", host+"/unchanged.yaml"

	cfg := config.Config{Modules: []config.Module{
		{Name: "drifted", From: driftedRef},
		{Name: "unchanged", From: unchangedRef},
	}}
	lockPath := filepath.Join(t.TempDir(), "dotular.lock.yaml")
	if err := SaveLock(lockPath, &LockFile{Registry: map[string]LockEntry{
		driftedRef:   {SHA256: oldModuleSum(), URL: "https://" + driftedRef},
		unchangedRef: {SHA256: testModuleSum(), URL: "https://" + unchangedRef},
	}}); err != nil {
		t.Fatal(err)
	}
	// SaveLock writes its temp file first, so a directory in that spot fails the
	// write for any user — unlike a read-only parent, which root would ignore.
	if err := os.Mkdir(lockPath+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}

	u := ui.New(&bytes.Buffer{}, &bytes.Buffer{})
	err := UpdatePins(context.Background(), cfg, lockPath, false, u)
	if err == nil || !strings.Contains(err.Error(), "save lockfile") {
		t.Fatalf("UpdatePins() error = %v, want the lockfile save failure", err)
	}

	if _, statErr := os.Stat(moduleCachePath(driftedRef)); !os.IsNotExist(statErr) {
		t.Errorf("cache file for the re-pinned ref survived (stat = %v); it disagrees with the pin that was kept", statErr)
	}
	// The unchanged ref's cache was written against a pin that still stands.
	kept, err := os.ReadFile(moduleCachePath(unchangedRef))
	if err != nil {
		t.Fatalf("cache file for the unchanged ref was discarded: %v", err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(kept)); got != testModuleSum() {
		t.Errorf("kept cache hashes to %q, want its pin %q", got, testModuleSum())
	}

	lock, err := LoadLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := lock.Registry[driftedRef].SHA256; got != oldModuleSum() {
		t.Errorf("persisted pin = %q, want the old pin %q to have been kept", got, oldModuleSum())
	}

	// The real guard: recovery works. With the cache gone the next ordinary fetch
	// goes to the network and verifies against the pin that was kept, instead of
	// reporting dotular's own failed update as a tampered cache.
	reverted.Store(true)
	mod, _, err := fetchForTest(t, driftedRef, lock, FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch() after a failed save = %v, want a clean re-fetch against the kept pin", err)
	}
	if mod.Name != "old-mod" {
		t.Errorf("module name = %q, want the re-fetched %q", mod.Name, "old-mod")
	}
}

// A cache write that cannot start must leave the previous entry alone: the old
// bytes still hash to the pin, whereas a partial replacement hashes to neither.
func TestWriteCacheFileKeepsThePreviousEntryWhenTheWriteFails(t *testing.T) {
	path := moduleCachePath("cache.example/atomic/kept")
	if err := writeCacheFile(path, []byte(oldModuleYAML)); err != nil {
		t.Fatal(err)
	}
	failCacheTempWrites(t)

	if err := writeCacheFile(path, []byte(testModuleYAML)); err == nil {
		t.Fatal("writeCacheFile() = nil error, want temp creation failure")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("previous cache entry is gone: %v", err)
	}
	if string(got) != oldModuleYAML {
		t.Errorf("cache = %q, want the previous entry %q left intact", got, oldModuleYAML)
	}
}

func TestWriteCacheFileReplacesExistingEntry(t *testing.T) {
	path := moduleCachePath("cache.example/atomic/replace")
	t.Cleanup(func() { _ = os.Remove(path) })
	if err := writeCacheFile(path, []byte(oldModuleYAML)); err != nil {
		t.Fatal(err)
	}
	if err := writeCacheFile(path, []byte(testModuleYAML)); err != nil {
		t.Fatalf("replace existing cache: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != testModuleYAML {
		t.Errorf("cache = %q, want replacement %q", got, testModuleYAML)
	}
}

func TestWriteCacheFileLeavesNoTempFileBehind(t *testing.T) {
	path := moduleCachePath("cache.example/atomic/clean")
	if err := writeCacheFile(path, []byte(testModuleYAML)); err != nil {
		t.Fatal(err)
	}

	tempFiles, err := filepath.Glob(path + ".tmp-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(tempFiles) != 0 {
		t.Errorf("temp files left in the cache directory: %v", tempFiles)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != testModuleYAML {
		t.Errorf("cache = %q, want %q", got, testModuleYAML)
	}
}

func TestWriteCacheFileConcurrentWritersUseUniqueTemps(t *testing.T) {
	path := moduleCachePath("cache.example/atomic/concurrent")
	previous := createCacheTemp
	created := make(chan string, 2)
	release := make(chan struct{})
	createCacheTemp = func(dir, pattern string) (*os.File, error) {
		file, err := os.CreateTemp(dir, pattern)
		if err == nil {
			created <- file.Name()
			<-release
		}
		return file, err
	}
	t.Cleanup(func() {
		createCacheTemp = previous
		_ = os.Remove(path)
	})

	first := []byte("name: first\nitems: []\n")
	second := []byte("name: second\nitems: []\n")
	errs := make(chan error, 2)
	go func() { errs <- writeCacheFile(path, first) }()
	go func() { errs <- writeCacheFile(path, second) }()

	firstTemp, secondTemp := <-created, <-created
	if firstTemp == secondTemp {
		close(release)
		t.Fatalf("concurrent writers shared temp path %q", firstTemp)
	}
	close(release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("writeCacheFile() = %v", err)
		}
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, first) && !bytes.Equal(got, second) {
		t.Errorf("cache contains mixed bytes: %q", got)
	}
	tempFiles, err := filepath.Glob(path + ".tmp-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(tempFiles) != 0 {
		t.Errorf("temp files left after concurrent writes: %v", tempFiles)
	}
}
