package registry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atomikpanda/dotular/internal/config"
)

func TestPinStatusValues(t *testing.T) {
	tests := map[string]PinStatus{
		"missing": PinStatusMissing,
		"match":   PinStatusMatch,
		"drift":   PinStatusDrift,
	}
	for want, got := range tests {
		if string(got) != want {
			t.Fatalf("status = %q, want %q", got, want)
		}
	}
}

func TestStageActiveRefsReturnsCompleteSortedUniqueRecordsWithoutWrites(t *testing.T) {
	dataA := moduleYAML("module-a", "a")
	dataM := moduleYAML("module-m", "m")
	dataZ := moduleYAML("module-z", "z")
	fixture := newUpdateFixture(t, map[string][]byte{
		"/a": dataA,
		"/m": dataM,
		"/z": dataZ,
	})
	refA, refM, refZ := fixture.ref("/a"), fixture.ref("/m"), fixture.ref("/z")
	fixture.configure(refZ, refM, refA, refM, refZ)
	fixture.persistLock(map[string]LockEntry{
		refM: {SHA256: sha256Hex(dataM), URL: ParseRef(refM).FetchURL},
		refZ: {SHA256: strings.Repeat("0", sha256.Size*2), URL: ParseRef(refZ).FetchURL},
	})
	fixture.seedCache(refA, []byte("old cache a"))
	fixture.seedCache(refM, []byte("old cache m"))
	fixture.seedCache(refZ, []byte("old cache z"))
	fixture.snapshotDurableState()

	staged, err := stageActiveRefs(context.Background(), fixture.cfg, fixture.lock)
	if err != nil {
		t.Fatalf("stageActiveRefs() error = %v", err)
	}

	if got, want := refsFromStaged(staged), []string{refA, refM, refZ}; !slices.Equal(got, want) {
		t.Fatalf("staged refs = %q, want %q", got, want)
	}
	for _, ref := range []string{refA, refM, refZ} {
		if got := fixture.requestCount(ref); got != 1 {
			t.Fatalf("requests for %q = %d, want 1", ref, got)
		}
	}
	wantModuleNames := map[string]string{
		refA: "module-a",
		refM: "module-m",
		refZ: "module-z",
	}
	wantPackages := map[string]string{
		refA: "a",
		refM: "m",
		refZ: "z",
	}
	for _, got := range staged {
		if got.ref == "" {
			t.Fatal("staged ref is empty")
		}
		if got.proposedSHA256 != sha256Hex(got.data) {
			t.Fatalf(
				"proposed checksum for %q = %q, want %q",
				got.ref,
				got.proposedSHA256,
				sha256Hex(got.data),
			)
		}
		if got.replacement.SHA256 != got.proposedSHA256 {
			t.Fatalf(
				"replacement checksum for %q = %q, want %q",
				got.ref,
				got.replacement.SHA256,
				got.proposedSHA256,
			)
		}
		if got.replacement.URL != ParseRef(got.ref).FetchURL {
			t.Fatalf("replacement URL for %q = %q, want %q", got.ref, got.replacement.URL, ParseRef(got.ref).FetchURL)
		}
		if got.replacement.FetchedAt.IsZero() {
			t.Fatalf("replacement fetch time for %q is zero", got.ref)
		}
		if got.module.Name != wantModuleNames[got.ref] ||
			len(got.module.Items) != 1 ||
			got.module.Items[0].Package != wantPackages[got.ref] {
			t.Fatalf("parsed module for %q = %#v, want name %q and package %q", got.ref, got.module, wantModuleNames[got.ref], wantPackages[got.ref])
		}
		switch got.status {
		case PinStatusMissing:
			if got.oldPresent || got.oldSHA256 != "" {
				t.Fatalf("missing ref %q has an old checksum", got.ref)
			}
		case PinStatusMatch:
			if !got.oldPresent || got.oldSHA256 != got.proposedSHA256 {
				t.Fatalf("match ref %q has inconsistent checksums", got.ref)
			}
		case PinStatusDrift:
			if !got.oldPresent || got.oldSHA256 == got.proposedSHA256 {
				t.Fatalf("drift ref %q has inconsistent checksums", got.ref)
			}
		default:
			t.Fatalf("ref %q has invalid status %q", got.ref, got.status)
		}
	}
	fixture.requireDurableStateUnchanged()
}

func TestStageActiveRefsReturnsMissingMatchAndDrift(t *testing.T) {
	dataA := moduleYAML("module-a", "a")
	dataM := moduleYAML("module-m", "m")
	dataZ := moduleYAML("module-z", "z")
	fixture := newUpdateFixture(t, map[string][]byte{
		"/a": dataA,
		"/m": dataM,
		"/z": dataZ,
	})
	refA, refM, refZ := fixture.ref("/a"), fixture.ref("/m"), fixture.ref("/z")
	oldZ := strings.Repeat("f", sha256.Size*2)
	fixture.configure(refZ, refA, refM)
	fixture.persistLock(map[string]LockEntry{
		refM: {SHA256: sha256Hex(dataM)},
		refZ: {SHA256: oldZ},
	})
	fixture.snapshotDurableState()

	staged, err := stageActiveRefs(context.Background(), fixture.cfg, fixture.lock)
	if err != nil {
		t.Fatalf("stageActiveRefs() error = %v", err)
	}
	got := changesFromStaged(staged)
	want := []PinChange{
		{
			Ref:       refA,
			OldSHA256: "",
			NewSHA256: sha256Hex(dataA),
			Status:    PinStatusMissing,
		},
		{
			Ref:       refM,
			OldSHA256: sha256Hex(dataM),
			NewSHA256: sha256Hex(dataM),
			Status:    PinStatusMatch,
		},
		{
			Ref:       refZ,
			OldSHA256: oldZ,
			NewSHA256: sha256Hex(dataZ),
			Status:    PinStatusDrift,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changesFromStaged() = %#v, want %#v", got, want)
	}
	fixture.requireDurableStateUnchanged()
}

func TestStageActiveRefsLaterFailureReturnsNoPartialRecordsWithoutWrites(t *testing.T) {
	dataA := moduleYAML("module-a", "a")
	fixture := newUpdateFixture(t, map[string][]byte{
		"/a": dataA,
		"/z": []byte("name: [unterminated\n"),
	})
	refA, refZ := fixture.ref("/a"), fixture.ref("/z")
	fixture.configure(refZ, refA)
	fixture.persistLock(map[string]LockEntry{
		refA: {SHA256: sha256Hex(dataA)},
	})
	fixture.seedCache(refA, []byte("old cache a"))
	fixture.seedCache(refZ, []byte("old cache z"))
	fixture.snapshotDurableState()

	got, err := stageActiveRefs(context.Background(), fixture.cfg, fixture.lock)
	fixture.requireWrappedStageError(err, refZ, "parse registry module")
	if got != nil {
		t.Fatalf("stageActiveRefs() = %#v, want nil records after a later failure", got)
	}
	if requests := fixture.requestCount(refA); requests != 1 {
		t.Fatalf("requests for first ref %q = %d, want 1", refA, requests)
	}
	if requests := fixture.requestCount(refZ); requests != 1 {
		t.Fatalf("requests for failing ref %q = %d, want 1", refZ, requests)
	}
	fixture.requireDurableStateUnchanged()
}

func TestStageActiveRefsDownloadFailureHasNoWrites(t *testing.T) {
	fixture := newUpdateFixture(t, map[string][]byte{"/a": moduleYAML("module-a", "a")})
	ref := fixture.ref("/a")
	fixture.prepareFailure(ref)
	cause := errors.New("fetch unavailable")
	fixture.swapTransport(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, cause
	}))

	got, err := stageActiveRefs(context.Background(), fixture.cfg, fixture.lock)
	fixture.requireStageError(err, cause, ref)
	if got != nil {
		t.Fatalf("stageActiveRefs() = %#v, want nil records", got)
	}
	fixture.requireDurableStateUnchanged()
}

func TestStageActiveRefsBodyReadFailureHasNoWrites(t *testing.T) {
	fixture := newUpdateFixture(t, map[string][]byte{"/a": moduleYAML("module-a", "a")})
	ref := fixture.ref("/a")
	fixture.prepareFailure(ref)
	cause := errors.New("body read failed")
	fixture.swapTransport(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(io.MultiReader(
				strings.NewReader("name: partial\n"),
				updateErrReader{cause},
			)),
			Header: make(http.Header),
		}, nil
	}))

	got, err := stageActiveRefs(context.Background(), fixture.cfg, fixture.lock)
	fixture.requireStageError(err, cause, ref)
	if got != nil {
		t.Fatalf("stageActiveRefs() = %#v, want nil records", got)
	}
	fixture.requireDurableStateUnchanged()
}

func TestStageActiveRefsMalformedYAMLHasNoWrites(t *testing.T) {
	fixture := newUpdateFixture(t, map[string][]byte{"/a": []byte("name: [unterminated\n")})
	ref := fixture.ref("/a")
	fixture.prepareFailure(ref)

	got, err := stageActiveRefs(context.Background(), fixture.cfg, fixture.lock)
	fixture.requireWrappedStageError(err, ref, "parse registry module")
	if got != nil {
		t.Fatalf("stageActiveRefs() = %#v, want nil records", got)
	}
	fixture.requireDurableStateUnchanged()
}

func TestStageActiveRefsYAMLTypeFailureHasNoWrites(t *testing.T) {
	data := []byte("name: invalid\nitems:\n  - file: source\n    destination: [not, a, platform, map]\n")
	fixture := newUpdateFixture(t, map[string][]byte{"/a": data})
	ref := fixture.ref("/a")
	fixture.prepareFailure(ref)

	got, err := stageActiveRefs(context.Background(), fixture.cfg, fixture.lock)
	fixture.requireWrappedStageError(err, ref, "must be a string or a macos/windows/linux mapping")
	if !strings.Contains(err.Error(), "parse registry module") {
		t.Fatalf("stageActiveRefs() error = %q, want stable parse context", err)
	}
	if got != nil {
		t.Fatalf("stageActiveRefs() = %#v, want nil records", got)
	}
	fixture.requireDurableStateUnchanged()
}

func TestReplacementLockPreservesInactiveEntriesAndDoesNotAliasOriginal(t *testing.T) {
	activeA := "example.com/a"
	activeZ := "example.com/z"
	inactive := "example.com/inactive"
	original := &LockFile{Registry: map[string]LockEntry{
		activeA: {
			SHA256:    strings.Repeat("a", sha256.Size*2),
			FetchedAt: time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC),
			URL:       "https://old.example/a",
		},
		activeZ: {
			SHA256:    strings.Repeat("z", sha256.Size*2),
			FetchedAt: time.Date(2025, 2, 3, 4, 5, 6, 0, time.UTC),
			URL:       "https://old.example/z",
		},
		inactive: {
			SHA256:    strings.Repeat("i", sha256.Size*2),
			FetchedAt: time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC),
			URL:       "https://old.example/inactive",
		},
	}}
	before := cloneTestLock(original)
	replacementA := LockEntry{
		SHA256:    strings.Repeat("b", sha256.Size*2),
		FetchedAt: time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC),
		URL:       "https://new.example/a",
	}
	replacementZ := LockEntry{
		SHA256:    strings.Repeat("c", sha256.Size*2),
		FetchedAt: time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC),
		URL:       "https://new.example/z",
	}
	staged := []stagedRef{
		{ref: activeA, replacement: replacementA},
		{ref: activeZ, replacement: replacementZ},
	}

	got := replacementLock(original, staged)

	if got == original {
		t.Fatal("replacementLock() returned the caller-owned lock")
	}
	if entry := got.Registry[activeA]; entry != replacementA {
		t.Fatalf("active entry %q = %#v, want %#v", activeA, entry, replacementA)
	}
	if entry := got.Registry[activeZ]; entry != replacementZ {
		t.Fatalf("active entry %q = %#v, want %#v", activeZ, entry, replacementZ)
	}
	if entry := got.Registry[inactive]; entry != before.Registry[inactive] {
		t.Fatalf("inactive entry %q = %#v, want %#v", inactive, entry, before.Registry[inactive])
	}
	if !reflect.DeepEqual(original, before) {
		t.Fatalf("original lock changed\nbefore: %#v\nafter:  %#v", before, original)
	}

	got.Registry[inactive] = LockEntry{SHA256: "mutated"}
	delete(got.Registry, activeA)
	if !reflect.DeepEqual(original, before) {
		t.Fatalf("mutating replacement changed original\nbefore: %#v\nafter:  %#v", before, original)
	}
}

type updateFixture struct {
	t          *testing.T
	server     *httptest.Server
	responses  map[string][]byte
	requestsMu sync.Mutex
	requests   map[string]int
	configPath string
	lockPath   string
	cfg        config.Config
	lock       *LockFile
	cachePaths map[string]string
	lockBytes  []byte
	cacheBytes map[string][]byte
	lockCopy   *LockFile
}

func newUpdateFixture(t *testing.T, responses map[string][]byte) *updateFixture {
	t.Helper()
	f := &updateFixture{
		t:          t,
		responses:  responses,
		requests:   make(map[string]int),
		cachePaths: make(map[string]string),
		cacheBytes: make(map[string][]byte),
	}
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requestsMu.Lock()
		f.requests[r.URL.Path]++
		f.requestsMu.Unlock()
		data, ok := f.responses[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(f.server.Close)
	swapClient(t, f.server.Client())
	home := t.TempDir()
	t.Setenv("HOME", home)
	f.configPath = filepath.Join(home, "dotular.yaml")
	f.lockPath = LockPath(f.configPath)
	return f
}

func (f *updateFixture) ref(path string) string {
	return strings.TrimPrefix(f.server.URL, "https://") + path
}

func (f *updateFixture) configure(refs ...string) {
	f.t.Helper()
	modules := make([]config.Module, len(refs))
	for i, ref := range refs {
		modules[i] = config.Module{From: ref}
	}
	if err := config.Save(f.configPath, config.Config{Modules: modules}); err != nil {
		f.t.Fatal(err)
	}
	cfg, err := config.Load(f.configPath)
	if err != nil {
		f.t.Fatal(err)
	}
	f.cfg = cfg
}

func (f *updateFixture) persistLock(entries map[string]LockEntry) {
	f.t.Helper()
	lock := &LockFile{Registry: make(map[string]LockEntry, len(entries))}
	for ref, entry := range entries {
		lock.Registry[ref] = entry
	}
	if err := SaveLock(f.lockPath, lock); err != nil {
		f.t.Fatal(err)
	}
	loaded, err := LoadLock(f.lockPath)
	if err != nil {
		f.t.Fatal(err)
	}
	f.lock = loaded
}

func (f *updateFixture) seedCache(ref string, data []byte) {
	f.t.Helper()
	path := moduleCachePath(ref)
	if err := writeCacheFile(path, data); err != nil {
		f.t.Fatal(err)
	}
	f.cachePaths[ref] = path
}

func (f *updateFixture) prepareFailure(ref string) {
	f.t.Helper()
	f.configure(ref)
	f.persistLock(nil)
	f.seedCache(ref, []byte("old cache"))
	f.snapshotDurableState()
}

func (f *updateFixture) snapshotDurableState() {
	f.t.Helper()
	var err error
	f.lockBytes, err = os.ReadFile(f.lockPath)
	if err != nil {
		f.t.Fatal(err)
	}
	for ref, path := range f.cachePaths {
		f.cacheBytes[ref], err = os.ReadFile(path)
		if err != nil {
			f.t.Fatal(err)
		}
	}
	f.lockCopy = cloneTestLock(f.lock)
}

func (f *updateFixture) requireDurableStateUnchanged() {
	f.t.Helper()
	gotLock, err := os.ReadFile(f.lockPath)
	if err != nil {
		f.t.Fatal(err)
	}
	if !bytes.Equal(gotLock, f.lockBytes) {
		f.t.Fatalf("lockfile bytes changed\nbefore: %q\nafter:  %q", f.lockBytes, gotLock)
	}
	for ref, path := range f.cachePaths {
		got, err := os.ReadFile(path)
		if err != nil {
			f.t.Fatalf("read cache for %q: %v", ref, err)
		}
		if !bytes.Equal(got, f.cacheBytes[ref]) {
			f.t.Fatalf("cache bytes for %q changed\nbefore: %q\nafter:  %q", ref, f.cacheBytes[ref], got)
		}
	}
	if !reflect.DeepEqual(f.lock, f.lockCopy) {
		f.t.Fatalf("caller-owned lock changed\nbefore: %#v\nafter:  %#v", f.lockCopy, f.lock)
	}
}

func (f *updateFixture) requestCount(ref string) int {
	f.t.Helper()
	path := "/" + strings.TrimPrefix(ParseRef(ref).Path, "/")
	f.requestsMu.Lock()
	defer f.requestsMu.Unlock()
	return f.requests[path]
}

func (f *updateFixture) swapTransport(transport http.RoundTripper) {
	f.t.Helper()
	swapClient(f.t, &http.Client{Transport: transport})
}

func (f *updateFixture) requireStageError(err, cause error, ref string) {
	f.t.Helper()
	if err == nil {
		f.t.Fatal("stageActiveRefs() error = nil, want failure")
	}
	if !strings.Contains(err.Error(), ref) {
		f.t.Fatalf("stageActiveRefs() error = %q, want failing ref %q", err, ref)
	}
	if !errors.Is(err, cause) {
		f.t.Fatalf("stageActiveRefs() error = %v, want it to wrap %v", err, cause)
	}
}

func (f *updateFixture) requireWrappedStageError(err error, ref, causeText string) {
	f.t.Helper()
	if err == nil {
		f.t.Fatal("stageActiveRefs() error = nil, want failure")
	}
	if !strings.Contains(err.Error(), ref) {
		f.t.Fatalf("stageActiveRefs() error = %q, want failing ref %q", err, ref)
	}
	if !strings.Contains(err.Error(), causeText) {
		f.t.Fatalf("stageActiveRefs() error = %q, want cause %q", err, causeText)
	}
	if errors.Unwrap(err) == nil {
		f.t.Fatalf("stageActiveRefs() error = %v, want a wrapped concrete cause", err)
	}
}

func moduleYAML(name, packageName string) []byte {
	return []byte(fmt.Sprintf("name: %s\nitems:\n  - package: %s\n    via: brew\n", name, packageName))
}

func sha256Hex(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func refsFromStaged(staged []stagedRef) []string {
	refs := make([]string, len(staged))
	for i := range staged {
		refs[i] = staged[i].ref
	}
	return refs
}

func cloneTestLock(lock *LockFile) *LockFile {
	clone := &LockFile{Registry: make(map[string]LockEntry, len(lock.Registry))}
	for ref, entry := range lock.Registry {
		clone.Registry[ref] = entry
	}
	return clone
}

type updateErrReader struct {
	err error
}

func (r updateErrReader) Read([]byte) (int, error) {
	return 0, r.err
}
