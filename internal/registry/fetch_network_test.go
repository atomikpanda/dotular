package registry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/atomikpanda/dotular/internal/ui"
)

func TestFetchOptionContract(t *testing.T) {
	t.Parallel()

	opts := FetchOptions{
		NoCache: true,
		Repin:   true,
	}
	if !opts.NoCache {
		t.Fatal("FetchOptions.NoCache = false; want true")
	}
	if !opts.Repin {
		t.Fatal("FetchOptions.Repin = false; want true")
	}
}

const (
	testModuleYAML        = "name: test-mod\nitems:\n  - package: neovim\n    via: brew\n"
	replacementModuleYAML = "name: replacement-mod\nitems:\n  - package: helix\n    via: brew\n"
	invalidModuleYAML     = "name: [unterminated\n"
)

func testModuleChecksum(data string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(data)))
}

func testModuleSum() string {
	return testModuleChecksum(testModuleYAML)
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

func loadTestLock(t *testing.T, path string) *LockFile {
	t.Helper()
	lock, err := LoadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	return lock
}

func newTestLock(t *testing.T) *LockFile {
	t.Helper()
	return loadTestLock(t, filepath.Join(t.TempDir(), "dotular.lock.yaml"))
}

func persistedTestLock(t *testing.T, ref string, entry *LockEntry) (string, *LockFile) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dotular.lock.yaml")
	lock := &LockFile{Registry: make(map[string]LockEntry)}
	if entry != nil {
		lock.Registry[ref] = *entry
	}
	if err := SaveLock(path, lock); err != nil {
		t.Fatal(err)
	}
	return path, loadTestLock(t, path)
}

func fetchWithOptionsForTest(t *testing.T, ref string, lock *LockFile, opts FetchOptions) (*RemoteModule, TrustLevel, error) {
	t.Helper()
	return Fetch(context.Background(), ref, lock, opts, ui.New(&bytes.Buffer{}, &bytes.Buffer{}))
}

func fetchForTest(t *testing.T, ref string, lock *LockFile, noCache bool) (*RemoteModule, TrustLevel, error) {
	t.Helper()
	return fetchWithOptionsForTest(t, ref, lock, FetchOptions{NoCache: noCache})
}

func requireLockEntryUnchanged(t *testing.T, before, after LockEntry) {
	t.Helper()

	if before != after {
		t.Fatalf("lock entry changed\nbefore: %#v\nafter:  %#v", before, after)
	}
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
		noCache  bool
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
			name:    "--no-cache does not bypass verification",
			handler: ok,
			lockSum: "0000000000000000000000000000000000000000000000000000000000000000",
			noCache: true,
			wantErr: "checksum mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref := serveTestModule(t, tt.handler)
			lock := newTestLock(t)
			if tt.lockSum != "" {
				lock.Registry[ref] = LockEntry{SHA256: tt.lockSum}
			}

			mod, _, err := fetchForTest(t, ref, lock, tt.noCache)

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
	_, _, err := fetchForTest(t, ref, lock, false)
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

	mod, _, err := fetchForTest(t, ref, lock, false)
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

			_, trust, err := fetchForTest(t, tt.ref, lock, false)
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

	_, _, err := fetchForTest(t, ref, lock, false)
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

	mod, _, err := fetchForTest(t, ref, newTestLock(t), false)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if mod.Name != "test-mod" {
		t.Errorf("module name = %q, want the network copy %q", mod.Name, "test-mod")
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

	mod, _, err := fetchForTest(t, ref, lock, false)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if mod.Name != "test-mod" {
		t.Errorf("module name = %q, want %q", mod.Name, "test-mod")
	}
}

func TestFetchPinnedCacheMatchPreservesLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var requests atomic.Int32
	ref := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, replacementModuleYAML)
	})
	original := LockEntry{
		SHA256: testModuleChecksum(testModuleYAML),
		URL:    ParseRef(ref).FetchURL,
	}
	lockPath, lock := persistedTestLock(t, ref, &original)
	if err := writeCacheFile(moduleCachePath(ref), []byte(testModuleYAML)); err != nil {
		t.Fatal(err)
	}
	before := lock.Registry[ref]

	mod, _, err := fetchWithOptionsForTest(t, ref, lock, FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if mod.Name != "test-mod" {
		t.Fatalf("module name = %q, want %q", mod.Name, "test-mod")
	}
	requireLockEntryUnchanged(t, before, lock.Registry[ref])
	requireLockEntryUnchanged(t, before, loadTestLock(t, lockPath).Registry[ref])
	if got := requests.Load(); got != 0 {
		t.Fatalf("network requests = %d, want 0", got)
	}
}

func TestFetchRejectsPinnedCacheMismatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var requests atomic.Int32
	ref := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, testModuleYAML)
	})
	original := LockEntry{
		SHA256: testModuleChecksum(testModuleYAML),
		URL:    ParseRef(ref).FetchURL,
	}
	lockPath, lock := persistedTestLock(t, ref, &original)
	if err := writeCacheFile(moduleCachePath(ref), []byte(replacementModuleYAML)); err != nil {
		t.Fatal(err)
	}
	before := lock.Registry[ref]

	_, _, err := fetchWithOptionsForTest(t, ref, lock, FetchOptions{})
	if err == nil {
		t.Fatal("Fetch() = nil error, want a checksum mismatch")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Fetch() error = %q, want it to contain %q", err, "checksum mismatch")
	}
	requireLockEntryUnchanged(t, before, lock.Registry[ref])
	requireLockEntryUnchanged(t, before, loadTestLock(t, lockPath).Registry[ref])
	if got := requests.Load(); got != 0 {
		t.Fatalf("network requests = %d, want 0", got)
	}
}

func TestFetchPinnedNetworkMatchPreservesLock(t *testing.T) {
	tests := []struct {
		name      string
		opts      FetchOptions
		seedCache bool
	}{
		{name: "cache miss"},
		{name: "no cache", opts: FetchOptions{NoCache: true}, seedCache: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			var requests atomic.Int32
			ref := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				fmt.Fprint(w, testModuleYAML)
			})
			original := LockEntry{
				SHA256: testModuleChecksum(testModuleYAML),
				URL:    ParseRef(ref).FetchURL,
			}
			lockPath, lock := persistedTestLock(t, ref, &original)
			if tt.seedCache {
				if err := writeCacheFile(moduleCachePath(ref), []byte(replacementModuleYAML)); err != nil {
					t.Fatal(err)
				}
			}
			before := lock.Registry[ref]

			mod, _, err := fetchWithOptionsForTest(t, ref, lock, tt.opts)
			if err != nil {
				t.Fatalf("Fetch() error = %v", err)
			}
			if mod.Name != "test-mod" {
				t.Fatalf("module name = %q, want %q", mod.Name, "test-mod")
			}
			requireLockEntryUnchanged(t, before, lock.Registry[ref])
			requireLockEntryUnchanged(t, before, loadTestLock(t, lockPath).Registry[ref])
			if got := requests.Load(); got != 1 {
				t.Fatalf("network requests = %d, want 1", got)
			}
		})
	}
}

func TestFetchNoCacheRejectsPinnedNetworkMismatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var requests atomic.Int32
	ref := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, replacementModuleYAML)
	})
	original := LockEntry{
		SHA256: testModuleChecksum(testModuleYAML),
		URL:    ParseRef(ref).FetchURL,
	}
	lockPath, lock := persistedTestLock(t, ref, &original)
	if err := writeCacheFile(moduleCachePath(ref), []byte(testModuleYAML)); err != nil {
		t.Fatal(err)
	}
	before := lock.Registry[ref]

	_, _, err := fetchWithOptionsForTest(t, ref, lock, FetchOptions{NoCache: true})
	if err == nil {
		t.Fatal("Fetch() = nil error, want a checksum mismatch")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Fetch() error = %q, want it to contain %q", err, "checksum mismatch")
	}
	requireLockEntryUnchanged(t, before, lock.Registry[ref])
	requireLockEntryUnchanged(t, before, loadTestLock(t, lockPath).Registry[ref])
	if got := requests.Load(); got != 1 {
		t.Fatalf("network requests = %d, want 1", got)
	}
}

func TestFetchCreatesInitialPinInMemoryOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, testModuleYAML)
	})
	lockPath, lock := persistedTestLock(t, ref, nil)

	mod, _, err := fetchWithOptionsForTest(t, ref, lock, FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if mod.Name != "test-mod" {
		t.Fatalf("module name = %q, want %q", mod.Name, "test-mod")
	}
	if got, want := lock.Registry[ref].SHA256, testModuleChecksum(testModuleYAML); got != want {
		t.Fatalf("in-memory checksum = %q, want %q", got, want)
	}
	if _, ok := loadTestLock(t, lockPath).Registry[ref]; ok {
		t.Fatal("Fetch() persisted an initial lock entry")
	}
}

func TestFetchRejectsPinnedMismatchBeforeYAMLParsing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var requests atomic.Int32
	ref := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, invalidModuleYAML)
	})
	original := LockEntry{
		SHA256: testModuleChecksum(testModuleYAML),
		URL:    ParseRef(ref).FetchURL,
	}
	lockPath, lock := persistedTestLock(t, ref, &original)
	before := lock.Registry[ref]

	_, _, err := fetchWithOptionsForTest(t, ref, lock, FetchOptions{})
	if err == nil {
		t.Fatal("Fetch() = nil error, want a checksum mismatch")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Fetch() error = %q, want checksum rejection before YAML parsing", err)
	}
	requireLockEntryUnchanged(t, before, lock.Registry[ref])
	requireLockEntryUnchanged(t, before, loadTestLock(t, lockPath).Registry[ref])
	if got := requests.Load(); got != 1 {
		t.Fatalf("network requests = %d, want 1", got)
	}
}

func TestFetchRepinInvalidYAMLLeavesPinUnchanged(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var requests atomic.Int32
	ref := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, invalidModuleYAML)
	})
	original := LockEntry{
		SHA256: testModuleChecksum(testModuleYAML),
		URL:    ParseRef(ref).FetchURL,
	}
	lockPath, lock := persistedTestLock(t, ref, &original)
	if err := writeCacheFile(moduleCachePath(ref), []byte(testModuleYAML)); err != nil {
		t.Fatal(err)
	}
	before := lock.Registry[ref]

	_, _, err := fetchWithOptionsForTest(t, ref, lock, FetchOptions{NoCache: true, Repin: true})
	if err == nil {
		t.Fatal("Fetch() = nil error, want a YAML parse error")
	}
	if !strings.Contains(err.Error(), "parse registry module") {
		t.Fatalf("Fetch() error = %q, want a YAML parse error", err)
	}
	requireLockEntryUnchanged(t, before, lock.Registry[ref])
	requireLockEntryUnchanged(t, before, loadTestLock(t, lockPath).Registry[ref])
	if got := requests.Load(); got != 1 {
		t.Fatalf("network requests = %d, want 1", got)
	}
	cached, readErr := os.ReadFile(moduleCachePath(ref))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(cached) != testModuleYAML {
		t.Fatalf("cache changed to invalid bytes: %q", cached)
	}
}

func TestFetchRepinMovesExistingPin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var requests atomic.Int32
	ref := serveTestModule(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, replacementModuleYAML)
	})
	original := LockEntry{
		SHA256: testModuleChecksum(testModuleYAML),
		URL:    ParseRef(ref).FetchURL,
	}
	lockPath, lock := persistedTestLock(t, ref, &original)
	before := lock.Registry[ref]

	mod, _, err := fetchWithOptionsForTest(t, ref, lock, FetchOptions{NoCache: true, Repin: true})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if mod.Name != "replacement-mod" {
		t.Fatalf("module name = %q, want %q", mod.Name, "replacement-mod")
	}
	if got, want := lock.Registry[ref].SHA256, testModuleChecksum(replacementModuleYAML); got != want {
		t.Fatalf("in-memory checksum = %q, want %q", got, want)
	}
	requireLockEntryUnchanged(t, before, loadTestLock(t, lockPath).Registry[ref])
	if got := requests.Load(); got != 1 {
		t.Fatalf("network requests = %d, want 1", got)
	}
}
