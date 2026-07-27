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
	"strings"
	"testing"

	"github.com/atomikpanda/dotular/internal/ui"
)

const testModuleYAML = "name: test-mod\nitems:\n  - package: neovim\n    via: brew\n"

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

func fetchForTest(t *testing.T, ref string, lock *LockFile, noCache bool) (*RemoteModule, TrustLevel, error) {
	t.Helper()
	return Fetch(context.Background(), ref, lock, noCache, ui.New(&bytes.Buffer{}, &bytes.Buffer{}))
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
			// Current --no-cache behaviour, deliberately documented rather than
			// changed: verification is skipped and the entry is re-pinned. See
			// issue #11 for whether that is the policy we want.
			name:     "--no-cache skips verification and re-pins",
			handler:  ok,
			lockSum:  "0000000000000000000000000000000000000000000000000000000000000000",
			noCache:  true,
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
