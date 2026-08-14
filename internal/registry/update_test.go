package registry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atomikpanda/dotular/internal/config"
	"github.com/atomikpanda/dotular/internal/ui"
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

	staged, err := stageActiveRefs(context.Background(), fixture.cfg, fixture.lock, maxAggregateStagedBytes)
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

func TestStageActiveRefsAllowsExactAggregateByteLimit(t *testing.T) {
	dataA := moduleYAML("module-a", "a")
	dataZ := moduleYAML("module-z", "z")
	fixture := newUpdateFixture(t, map[string][]byte{
		"/a": dataA,
		"/z": dataZ,
	})
	refA, refZ := fixture.ref("/a"), fixture.ref("/z")
	fixture.configure(refZ, refA)
	fixture.persistLock(nil)
	fixture.snapshotDurableState()

	staged, err := stageActiveRefs(
		context.Background(),
		fixture.cfg,
		fixture.lock,
		len(dataA)+len(dataZ),
	)
	if err != nil {
		t.Fatalf("stageActiveRefs() error = %v", err)
	}
	if got, want := refsFromStaged(staged), []string{refA, refZ}; !slices.Equal(got, want) {
		t.Fatalf("staged refs = %q, want %q", got, want)
	}
	for _, ref := range []string{refA, refZ} {
		if got := fixture.requestCount(ref); got != 1 {
			t.Fatalf("requests for %q = %d, want 1", ref, got)
		}
	}
	fixture.requireDurableStateUnchanged()
}

func TestUpdatePinsAggregateByteLimitReturnsNoRowsOrMutation(t *testing.T) {
	dataA := moduleYAML("module-a", "a")
	dataZ := moduleYAML("module-z", "z")
	fixture := newUpdateFixture(t, map[string][]byte{
		"/a": dataA,
		"/z": dataZ,
	})
	refA, refZ := fixture.ref("/a"), fixture.ref("/z")
	fixture.configure(refZ, refA, refZ)
	fixture.persistLock(nil)
	fixture.snapshotDurableState()
	recorder := newUpdateRecorder(fixture.lock)
	ops := recorder.ops()
	ops.maxStagedBytes = len(dataA) + len(dataZ) - 1

	changes, err := updatePinsWithOps(context.Background(), fixture.cfg, ops)

	if err == nil {
		t.Fatal("updatePinsWithOps() error = nil, want aggregate staged-data limit error")
	}
	if !strings.Contains(err.Error(), fmt.Sprintf(
		"stage registry ref %q: aggregate staged response data exceeds the %d byte limit",
		refZ,
		ops.maxStagedBytes,
	)) {
		t.Fatalf("updatePinsWithOps() error = %q, want staged ref and aggregate byte-limit context", err)
	}
	if changes != nil {
		t.Fatalf("changes = %#v, want nil", changes)
	}
	for _, ref := range []string{refA, refZ} {
		if got := fixture.requestCount(ref); got != 1 {
			t.Fatalf("requests for %q = %d, want 1", ref, got)
		}
	}
	if len(recorder.warnings) != 0 {
		t.Fatalf("warnings = %q, want none", recorder.warnings)
	}
	recorder.requireNoMutationOps(t)
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

	staged, err := stageActiveRefs(context.Background(), fixture.cfg, fixture.lock, maxAggregateStagedBytes)
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

	got, err := stageActiveRefs(context.Background(), fixture.cfg, fixture.lock, maxAggregateStagedBytes)
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

	got, err := stageActiveRefs(context.Background(), fixture.cfg, fixture.lock, maxAggregateStagedBytes)
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

	got, err := stageActiveRefs(context.Background(), fixture.cfg, fixture.lock, maxAggregateStagedBytes)
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

	got, err := stageActiveRefs(context.Background(), fixture.cfg, fixture.lock, maxAggregateStagedBytes)
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

	got, err := stageActiveRefs(context.Background(), fixture.cfg, fixture.lock, maxAggregateStagedBytes)
	fixture.requireWrappedStageError(err, ref, "must be a string or a macos/windows/linux mapping")
	if !strings.Contains(err.Error(), "parse registry module") {
		t.Fatalf("stageActiveRefs() error = %q, want stable parse context", err)
	}
	if got != nil {
		t.Fatalf("stageActiveRefs() = %#v, want nil records", got)
	}
	fixture.requireDurableStateUnchanged()
}

func TestStageActiveRefsValidatesEverySharedRefUsageAfterUniqueFetchesWithoutWrites(t *testing.T) {
	sharedData := []byte(
		"name: shared\n" +
			"params:\n" +
			"  package:\n" +
			"    default: safe\n" +
			"items:\n" +
			"  - package: '{{ .package }}'\n" +
			"    via: brew\n",
	)
	otherData := moduleYAML("other", "other")
	fixture := newUpdateFixture(t, map[string][]byte{
		"/shared": sharedData,
		"/other":  otherData,
	})
	sharedRef, otherRef := fixture.ref("/shared"), fixture.ref("/other")
	fixture.configure(sharedRef, otherRef, sharedRef)
	fixture.cfg.Modules[0].Name = "valid-shared-usage"
	fixture.cfg.Modules[0].With = map[string]any{"package": "valid"}
	fixture.cfg.Modules[2].Name = "invalid-shared-usage"
	fixture.cfg.Modules[2].With = map[string]any{"package": "invalid'quote"}
	fixture.persistLock(map[string]LockEntry{
		sharedRef: {SHA256: strings.Repeat("a", sha256.Size*2)},
		otherRef:  {SHA256: strings.Repeat("b", sha256.Size*2)},
	})
	fixture.seedCache(sharedRef, []byte("old shared cache"))
	fixture.seedCache(otherRef, []byte("old other cache"))
	fixture.snapshotDurableState()

	got, err := stageActiveRefs(context.Background(), fixture.cfg, fixture.lock, maxAggregateStagedBytes)

	fixture.requireWrappedStageError(err, sharedRef, `module "invalid-shared-usage"`)
	if !strings.Contains(err.Error(), "unmarshal rendered item") {
		t.Fatalf("stageActiveRefs() error = %q, want rendered item context", err)
	}
	if got != nil {
		t.Fatalf("stageActiveRefs() = %#v, want nil records after validation failure", got)
	}
	for _, ref := range []string{sharedRef, otherRef} {
		if requests := fixture.requestCount(ref); requests != 1 {
			t.Fatalf("requests for %q = %d, want 1", ref, requests)
		}
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

func TestModuleCacheCollisionKeyUsesTargetOSCaseSensitivity(t *testing.T) {
	tests := []struct {
		name      string
		goos      string
		left      string
		right     string
		wantEqual bool
	}{
		{
			name:      "windows case alias",
			goos:      "windows",
			left:      "/cache/Parent/../Module",
			right:     "/cache/module",
			wantEqual: true,
		},
		{
			name:      "darwin case alias",
			goos:      "darwin",
			left:      "/cache/Parent/../Module",
			right:     "/cache/module",
			wantEqual: true,
		},
		{
			name:      "linux case distinct",
			goos:      "linux",
			left:      "/cache/Parent/../Module",
			right:     "/cache/module",
			wantEqual: false,
		},
		{
			name:      "linux cleaned alias",
			goos:      "linux",
			left:      "/cache/Parent/../module",
			right:     "/cache/module",
			wantEqual: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			left := moduleCacheCollisionKey(tt.goos, tt.left)
			right := moduleCacheCollisionKey(tt.goos, tt.right)
			if got := left == right; got != tt.wantEqual {
				t.Fatalf(
					"moduleCacheCollisionKey(%q, %q) == moduleCacheCollisionKey(%q, %q) = %t, want %t",
					tt.goos,
					tt.left,
					tt.goos,
					tt.right,
					got,
					tt.wantEqual,
				)
			}
		})
	}
}

func TestUpdatePinsHandlesCaseOnlyActiveCachePathsByTargetOS(t *testing.T) {
	tests := []struct {
		goos          string
		wantCollision bool
	}{
		{goos: "windows", wantCollision: true},
		{goos: "darwin", wantCollision: true},
		{goos: "linux", wantCollision: false},
	}

	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			dataUpper := moduleYAML("module-upper", "upper")
			dataLower := moduleYAML("module-lower", "lower")
			fixture := newUpdateFixture(t, map[string][]byte{
				"/Module": dataUpper,
				"/module": dataLower,
			})
			refUpper, refLower := fixture.ref("/Module"), fixture.ref("/module")
			fixture.configure(refLower, refUpper)
			fixture.persistLock(nil)
			recorder := newUpdateRecorder(fixture.lock)
			recorder.goos = tt.goos
			recorder.paths[refUpper] = "/cache/Module"
			recorder.paths[refLower] = "/cache/module"

			changes, err := updatePinsWithOps(context.Background(), fixture.cfg, recorder.ops())

			wantChanges := []PinChange{
				{Ref: refUpper, NewSHA256: sha256Hex(dataUpper), Status: PinStatusMissing},
				{Ref: refLower, NewSHA256: sha256Hex(dataLower), Status: PinStatusMissing},
			}
			if !reflect.DeepEqual(changes, wantChanges) {
				t.Fatalf("changes = %#v, want %#v", changes, wantChanges)
			}
			if tt.wantCollision {
				wantErr := fmt.Sprintf(
					"module cache path collision: refs %q and %q both map to %q",
					refUpper,
					refLower,
					"/cache/module",
				)
				if err == nil || err.Error() != wantErr {
					t.Fatalf("error = %q, want %q", err, wantErr)
				}
				recorder.requireNoMutationOps(t)
				return
			}

			if err != nil {
				t.Fatalf("updatePinsWithOps() error = %v", err)
			}
			if len(recorder.saveAttempts) != 1 {
				t.Fatalf("save attempts = %d, want 1", len(recorder.saveAttempts))
			}
			if got, want := recorder.publications, []string{"/cache/Module", "/cache/module"}; !slices.Equal(got, want) {
				t.Fatalf("publications = %q, want %q", got, want)
			}
		})
	}
}

func TestUpdatePinsStagesEveryRefBeforeActiveActiveCollision(t *testing.T) {
	fixture := newUpdateFixture(t, map[string][]byte{
		"/a": moduleYAML("module-a", "a"),
		"/z": moduleYAML("module-z", "z"),
	})
	refA, refZ := fixture.ref("/a"), fixture.ref("/z")
	fixture.configure(refZ, refA)
	fixture.persistLock(nil)
	recorder := newUpdateRecorder(fixture.lock)
	recorder.paths[refA] = "/cache/collision"
	recorder.paths[refZ] = "/cache/collision"

	changes, err := updatePinsWithOps(context.Background(), fixture.cfg, recorder.ops())

	requireChangeRefs(t, changes, refA, refZ)
	if err == nil {
		t.Fatal("updatePinsWithOps() error = nil, want collision")
	}
	for _, ref := range []string{refA, refZ} {
		if got := fixture.requestCount(ref); got != 1 {
			t.Fatalf("requests for %q = %d, want 1", ref, got)
		}
	}
	recorder.requireNoMutationOps(t)
}

func TestUpdatePinsRejectsActiveInactiveCollisionAfterStaging(t *testing.T) {
	data := moduleYAML("active", "active")
	fixture := newUpdateFixture(t, map[string][]byte{"/active": data})
	activeRef := fixture.ref("/active")
	inactiveRef := "inactive.example/module"
	fixture.configure(activeRef)
	fixture.persistLock(map[string]LockEntry{
		inactiveRef: {SHA256: strings.Repeat("i", sha256.Size*2)},
	})
	recorder := newUpdateRecorder(fixture.lock)
	collisionPath := "/cache/collision"
	recorder.paths[activeRef] = collisionPath
	recorder.paths[inactiveRef] = collisionPath

	changes, err := updatePinsWithOps(context.Background(), fixture.cfg, recorder.ops())

	requireChangeRefs(t, changes, activeRef)
	if got := fixture.requestCount(activeRef); got != 1 {
		t.Fatalf("requests for %q = %d, want 1", activeRef, got)
	}
	want := fmt.Sprintf(
		"module cache path collision: refs %q and %q both map to %q",
		activeRef,
		inactiveRef,
		collisionPath,
	)
	if err == nil || err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
	recorder.requireNoMutationOps(t)
}

func TestUpdatePinsAllowsInactiveInactiveCollision(t *testing.T) {
	data := moduleYAML("active", "active")
	fixture := newUpdateFixture(t, map[string][]byte{"/active": data})
	activeRef := fixture.ref("/active")
	inactiveA, inactiveZ := "inactive.example/a", "inactive.example/z"
	inactiveEntries := map[string]LockEntry{
		inactiveA: {SHA256: strings.Repeat("a", sha256.Size*2)},
		inactiveZ: {SHA256: strings.Repeat("z", sha256.Size*2)},
	}
	fixture.configure(activeRef)
	fixture.persistLock(inactiveEntries)
	recorder := newUpdateRecorder(fixture.lock)
	recorder.paths[activeRef] = "/cache/active"
	recorder.paths[inactiveA] = "/cache/inactive-shared"
	recorder.paths[inactiveZ] = "/cache/inactive-shared"
	recorder.files["/cache/inactive-shared"] = []byte("inactive")

	changes, err := updatePinsWithOps(context.Background(), fixture.cfg, recorder.ops())

	if err != nil {
		t.Fatalf("updatePinsWithOps() error = %v", err)
	}
	requireChangeRefs(t, changes, activeRef)
	for ref, want := range inactiveEntries {
		if got := recorder.durable.Registry[ref]; got != want {
			t.Fatalf("inactive entry %q = %#v, want %#v", ref, got, want)
		}
	}
	recorder.requirePathUntouched(t, "/cache/inactive-shared")
}

func TestUpdatePinsCompletesAllCollisionChecksBeforeCachePreparation(t *testing.T) {
	fixture := newUpdateFixture(t, map[string][]byte{
		"/a": moduleYAML("module-a", "a"),
		"/m": moduleYAML("module-m", "m"),
		"/z": moduleYAML("module-z", "z"),
	})
	refA, refM, refZ := fixture.ref("/a"), fixture.ref("/m"), fixture.ref("/z")
	fixture.configure(refZ, refM, refA)
	fixture.persistLock(nil)
	recorder := newUpdateRecorder(fixture.lock)
	recorder.paths[refA] = "/cache/a"
	recorder.paths[refM] = "/cache/collision"
	recorder.paths[refZ] = "/cache/collision"

	changes, err := updatePinsWithOps(context.Background(), fixture.cfg, recorder.ops())

	requireChangeRefs(t, changes, refA, refM, refZ)
	if err == nil {
		t.Fatal("updatePinsWithOps() error = nil, want collision")
	}
	for _, ref := range []string{refA, refM, refZ} {
		if got := fixture.requestCount(ref); got != 1 {
			t.Fatalf("requests for %q = %d, want 1", ref, got)
		}
	}
	recorder.requireNoMutationOps(t)
}

func TestUpdatePinsReturnsSortedMissingMatchAndDrift(t *testing.T) {
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
	fixture.configure(refZ, refM, refA, refZ, refM)
	fixture.persistLock(map[string]LockEntry{
		refM: {SHA256: sha256Hex(dataM)},
		refZ: {SHA256: oldZ},
	})
	before := cloneTestLock(fixture.lock)
	recorder := newUpdateRecorder(fixture.lock)

	changes, err := updatePinsWithOps(context.Background(), fixture.cfg, recorder.ops())

	if err != nil {
		t.Fatalf("updatePinsWithOps() error = %v", err)
	}
	want := []PinChange{
		{Ref: refA, NewSHA256: sha256Hex(dataA), Status: PinStatusMissing},
		{Ref: refM, OldSHA256: sha256Hex(dataM), NewSHA256: sha256Hex(dataM), Status: PinStatusMatch},
		{Ref: refZ, OldSHA256: oldZ, NewSHA256: sha256Hex(dataZ), Status: PinStatusDrift},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %#v, want %#v", changes, want)
	}
	for _, ref := range []string{refA, refM, refZ} {
		if got := fixture.requestCount(ref); got != 1 {
			t.Fatalf("requests for %q = %d, want 1", ref, got)
		}
		if got := recorder.durable.Registry[ref].SHA256; got != changeForRef(t, changes, ref).NewSHA256 {
			t.Fatalf("saved checksum for %q = %q", ref, got)
		}
	}
	if len(recorder.saveAttempts) != 1 {
		t.Fatalf("save attempts = %d, want 1", len(recorder.saveAttempts))
	}
	if !reflect.DeepEqual(fixture.lock, before) {
		t.Fatalf("caller-owned lock changed\nbefore: %#v\nafter:  %#v", before, fixture.lock)
	}
}

func TestUpdatePinsPreservesInactiveLockEntriesAndCachePaths(t *testing.T) {
	data := moduleYAML("active", "active")
	fixture := newUpdateFixture(t, map[string][]byte{"/active": data})
	activeRef := fixture.ref("/active")
	inactiveA, inactiveZ := "inactive.example/a", "inactive.example/z"
	inactiveEntries := map[string]LockEntry{
		inactiveA: {SHA256: strings.Repeat("a", sha256.Size*2), URL: "https://inactive/a"},
		inactiveZ: {SHA256: strings.Repeat("z", sha256.Size*2), URL: "https://inactive/z"},
	}
	fixture.configure(activeRef)
	fixture.persistLock(inactiveEntries)
	recorder := newUpdateRecorder(fixture.lock)
	recorder.paths[activeRef] = "/cache/active"
	recorder.paths[inactiveA] = "/cache/inactive-shared"
	recorder.paths[inactiveZ] = "/cache/inactive-shared"
	recorder.files["/cache/inactive-shared"] = []byte("preserve me")

	_, err := updatePinsWithOps(context.Background(), fixture.cfg, recorder.ops())

	if err != nil {
		t.Fatalf("updatePinsWithOps() error = %v", err)
	}
	for ref, want := range inactiveEntries {
		if got := recorder.durable.Registry[ref]; got != want {
			t.Fatalf("inactive entry %q = %#v, want %#v", ref, got, want)
		}
	}
	recorder.requirePathUntouched(t, "/cache/inactive-shared")
}

func TestUpdatePinsRetainsExactReadableActiveCache(t *testing.T) {
	fixture, recorder, ref, data := newSingleUpdateScenario(t, true)
	recorder.files[recorder.path(ref)] = append([]byte(nil), data...)

	_, err := updatePinsWithOps(context.Background(), fixture.cfg, recorder.ops())

	if err != nil {
		t.Fatalf("updatePinsWithOps() error = %v", err)
	}
	if len(recorder.publications) != 0 {
		t.Fatalf("publications = %q, want none", recorder.publications)
	}
}

func TestUpdatePinsTreatsMissingActiveCacheAsAbsent(t *testing.T) {
	fixture, recorder, ref, data := newSingleUpdateScenario(t, false)

	_, err := updatePinsWithOps(context.Background(), fixture.cfg, recorder.ops())

	if err != nil {
		t.Fatalf("updatePinsWithOps() error = %v", err)
	}
	if len(recorder.publications) != 1 {
		t.Fatalf("publications = %q, want one", recorder.publications)
	}
	requirePublishedBytes(t, recorder, ref, data)
}

func TestUpdatePinsReplacesMismatchingActiveCacheOnlyAfterSave(t *testing.T) {
	fixture, recorder, ref, data := newSingleUpdateScenario(t, true)
	path := recorder.path(ref)
	old := []byte("stale")
	recorder.files[path] = append([]byte(nil), old...)
	oldPresentAtSave := false
	recorder.beforeSave = func() {
		oldPresentAtSave = bytes.Equal(recorder.files[path], old)
	}

	_, err := updatePinsWithOps(context.Background(), fixture.cfg, recorder.ops())

	if err != nil {
		t.Fatalf("updatePinsWithOps() error = %v", err)
	}
	if !oldPresentAtSave {
		t.Fatalf("cache %q changed before SaveLock: events=%q", path, recorder.events)
	}
	if indexOfEvent(recorder.events, "save") > indexOfEvent(recorder.events, "publish:"+path) {
		t.Fatalf("events = %q, want save before publication", recorder.events)
	}
	requirePublishedBytes(t, recorder, ref, data)
}

func TestUpdatePinsRetainsMatchingOrphanForMissingPin(t *testing.T) {
	fixture, recorder, ref, data := newSingleUpdateScenario(t, false)
	recorder.files[recorder.path(ref)] = append([]byte(nil), data...)

	_, err := updatePinsWithOps(context.Background(), fixture.cfg, recorder.ops())

	if err != nil {
		t.Fatalf("updatePinsWithOps() error = %v", err)
	}
	if len(recorder.publications) != 0 {
		t.Fatalf("publications = %q, want none", recorder.publications)
	}
}

func TestUpdatePinsReplacesMismatchingOrphanOnlyAfterSave(t *testing.T) {
	fixture, recorder, ref, data := newSingleUpdateScenario(t, false)
	path := recorder.path(ref)
	old := []byte("orphan")
	recorder.files[path] = append([]byte(nil), old...)
	oldPresentAtSave := false
	recorder.beforeSave = func() {
		oldPresentAtSave = bytes.Equal(recorder.files[path], old)
	}

	_, err := updatePinsWithOps(context.Background(), fixture.cfg, recorder.ops())

	if err != nil {
		t.Fatalf("updatePinsWithOps() error = %v", err)
	}
	if !oldPresentAtSave {
		t.Fatalf("orphan cache %q changed before SaveLock: events=%q", path, recorder.events)
	}
	requirePublishedBytes(t, recorder, ref, data)
}

func TestUpdatePinsReadErrorPreservesBytesUntilPostSavePublication(t *testing.T) {
	fixture, recorder, ref, data := newSingleUpdateScenario(t, true)
	path := recorder.path(ref)
	errRead := errors.New("read failed")
	old := []byte("unverifiable")
	recorder.readErrors[path] = errRead
	recorder.files[path] = append([]byte(nil), old...)
	oldPresentAtSave := false
	recorder.beforeSave = func() {
		oldPresentAtSave = bytes.Equal(recorder.files[path], old)
	}

	_, err := updatePinsWithOps(context.Background(), fixture.cfg, recorder.ops())

	if err != nil {
		t.Fatalf("updatePinsWithOps() error = %v", err)
	}
	if !oldPresentAtSave {
		t.Fatalf("unreadable cache %q changed before SaveLock: events=%q", path, recorder.events)
	}
	if len(recorder.saveAttempts) != 1 {
		t.Fatalf("save attempts = %d, want 1", len(recorder.saveAttempts))
	}
	if indexOfEvent(recorder.events, "save") > indexOfEvent(recorder.events, "publish:"+path) {
		t.Fatalf("events = %q, want save before publication", recorder.events)
	}
	if got := recorder.files[path]; !bytes.Equal(got, data) {
		t.Fatalf("final target = %q, want %q", got, data)
	}
}

func TestUpdatePinsStagesWholeCommandBeforeFirstPreparation(t *testing.T) {
	fixture := newUpdateFixture(t, map[string][]byte{
		"/a": moduleYAML("module-a", "a"),
		"/m": moduleYAML("module-m", "m"),
		"/z": moduleYAML("module-z", "z"),
	})
	refA, refM, refZ := fixture.ref("/a"), fixture.ref("/m"), fixture.ref("/z")
	fixture.configure(refZ, refM, refA)
	fixture.persistLock(nil)
	recorder := newUpdateRecorder(fixture.lock)
	firstPreparationSawAllStaged := false
	recorder.beforeFirstRead = func() {
		firstPreparationSawAllStaged = fixture.requestCount(refA) == 1 &&
			fixture.requestCount(refM) == 1 &&
			fixture.requestCount(refZ) == 1
	}

	_, err := updatePinsWithOps(context.Background(), fixture.cfg, recorder.ops())

	if err != nil {
		t.Fatalf("updatePinsWithOps() error = %v", err)
	}
	if !firstPreparationSawAllStaged {
		t.Fatal("cache preparation began before every ref was fetched and parsed")
	}
}

func TestUpdatePinsSavesCompleteReplacementExactlyOnce(t *testing.T) {
	dataA := moduleYAML("module-a", "a")
	dataM := moduleYAML("module-m", "m")
	dataZ := moduleYAML("module-z", "z")
	fixture := newUpdateFixture(t, map[string][]byte{
		"/a": dataA,
		"/m": dataM,
		"/z": dataZ,
	})
	refA, refM, refZ := fixture.ref("/a"), fixture.ref("/m"), fixture.ref("/z")
	inactive := "inactive.example/module"
	fixture.configure(refZ, refM, refA)
	fixture.persistLock(map[string]LockEntry{
		refM:     {SHA256: sha256Hex(dataM)},
		refZ:     {SHA256: strings.Repeat("z", sha256.Size*2)},
		inactive: {SHA256: strings.Repeat("i", sha256.Size*2)},
	})
	before := cloneTestLock(fixture.lock)
	recorder := newUpdateRecorder(fixture.lock)
	recorder.files[recorder.path(refM)] = append([]byte(nil), dataM...)

	changes, err := updatePinsWithOps(context.Background(), fixture.cfg, recorder.ops())

	if err != nil {
		t.Fatalf("updatePinsWithOps() error = %v", err)
	}
	if len(recorder.saveAttempts) != 1 {
		t.Fatalf("save attempts = %d, want 1", len(recorder.saveAttempts))
	}
	saved := recorder.saveAttempts[0]
	for _, change := range changes {
		if got := saved.Registry[change.Ref].SHA256; got != change.NewSHA256 {
			t.Fatalf("saved checksum for %q = %q, want %q", change.Ref, got, change.NewSHA256)
		}
	}
	if got := saved.Registry[inactive]; got != before.Registry[inactive] {
		t.Fatalf("inactive entry = %#v, want %#v", got, before.Registry[inactive])
	}
	saveIndex := indexOfEvent(recorder.events, "save")
	for _, event := range recorder.events[:saveIndex] {
		if strings.HasPrefix(event, "publish:") {
			t.Fatalf("publication preceded save: %q", recorder.events)
		}
	}
	for _, path := range recorder.publications {
		if indexOfEvent(recorder.events, "publish:"+path) < saveIndex {
			t.Fatalf("events = %q, want save before every publication", recorder.events)
		}
	}
	saved.Registry[inactive] = LockEntry{SHA256: "mutated"}
	if !reflect.DeepEqual(fixture.lock, before) {
		t.Fatalf("mutating captured lock changed pre-command lock\nbefore: %#v\nafter:  %#v", before, fixture.lock)
	}
}

func TestUpdatePinsSaveLockFailureReturnsAllRowsAndPreservesEveryCacheByte(t *testing.T) {
	fixture := newUpdateFixture(t, map[string][]byte{
		"/a": moduleYAML("module-a", "a"),
		"/z": moduleYAML("module-z", "z"),
	})
	refA, refZ := fixture.ref("/a"), fixture.ref("/z")
	fixture.configure(refZ, refA)
	fixture.persistLock(map[string]LockEntry{
		refA: {SHA256: strings.Repeat("a", sha256.Size*2), URL: "https://old.example/a"},
		refZ: {SHA256: strings.Repeat("z", sha256.Size*2), URL: "https://old.example/z"},
	})
	recorder := newUpdateRecorder(fixture.lock)
	pathA, pathZ := recorder.path(refA), recorder.path(refZ)
	oldA, oldZ := []byte("stale-a"), []byte("unreadable-z")
	recorder.files[pathA] = append([]byte(nil), oldA...)
	recorder.files[pathZ] = append([]byte(nil), oldZ...)
	recorder.readErrors[pathZ] = errors.New("read failed")
	errSave := errors.New("save failed")
	recorder.saveError = errSave
	before := cloneTestLock(&recorder.durable)

	changes, err := updatePinsWithOps(context.Background(), fixture.cfg, recorder.ops())

	if !errors.Is(err, errSave) {
		t.Fatalf("error = %v, want it to wrap %v", err, errSave)
	}
	requireChangeRefs(t, changes, refA, refZ)
	if len(recorder.saveAttempts) != 1 {
		t.Fatalf("save attempts = %d, want 1", len(recorder.saveAttempts))
	}
	if len(recorder.publications) != 0 {
		t.Fatalf("publications = %q, want none", recorder.publications)
	}
	if !reflect.DeepEqual(&recorder.durable, before) {
		t.Fatalf("durable lock changed\nbefore: %#v\nafter:  %#v", before, recorder.durable)
	}
	if got := recorder.files[pathA]; !bytes.Equal(got, oldA) {
		t.Fatalf("cache %q = %q, want unchanged %q", pathA, got, oldA)
	}
	if got := recorder.files[pathZ]; !bytes.Equal(got, oldZ) {
		t.Fatalf("cache %q = %q, want unchanged %q", pathZ, got, oldZ)
	}
}

func TestUpdatePinsPublicationFailureReturnsAllRowsAndLeavesAtomicOldOrNewBytes(t *testing.T) {
	fixture := newUpdateFixture(t, map[string][]byte{
		"/a": moduleYAML("module-a", "a"),
		"/z": moduleYAML("module-z", "z"),
	})
	refA, refZ := fixture.ref("/a"), fixture.ref("/z")
	fixture.configure(refZ, refA)
	fixture.persistLock(nil)
	recorder := newUpdateRecorder(fixture.lock)
	pathZ := recorder.path(refZ)
	oldZ := []byte("stale-z")
	recorder.files[pathZ] = append([]byte(nil), oldZ...)
	errPublish := errors.New("publish failed")
	recorder.publishErrors[pathZ] = errPublish

	changes, err := updatePinsWithOps(context.Background(), fixture.cfg, recorder.ops())

	if !errors.Is(err, errPublish) {
		t.Fatalf("error = %v, want it to wrap %v", err, errPublish)
	}
	requireChangeRefs(t, changes, refA, refZ)
	if len(recorder.saveAttempts) != 1 {
		t.Fatalf("save attempts = %d, want 1", len(recorder.saveAttempts))
	}
	for _, change := range changes {
		if got := recorder.durable.Registry[change.Ref].SHA256; got != change.NewSHA256 {
			t.Fatalf("durable checksum for %q = %q, want %q", change.Ref, got, change.NewSHA256)
		}
	}
	if got := recorder.files[recorder.path(refA)]; !bytes.Equal(got, responseForRef(t, fixture, refA)) {
		t.Fatalf("published target %q = %q, want exact staged bytes", refA, got)
	}
	if got := recorder.files[pathZ]; !bytes.Equal(got, oldZ) {
		t.Fatalf("failed atomic replacement target %q = %q, want unchanged %q", pathZ, got, oldZ)
	}
}

func TestUpdatePinsSuccessfulOrderingIsDeterministic(t *testing.T) {
	dataA := moduleYAML("module-a", "a")
	dataM := moduleYAML("module-m", "m")
	dataZ := moduleYAML("module-z", "z")
	fixture := newUpdateFixture(t, map[string][]byte{
		"/a": dataA,
		"/m": dataM,
		"/z": dataZ,
	})
	refA, refM, refZ := fixture.ref("/a"), fixture.ref("/m"), fixture.ref("/z")
	fixture.configure(refZ, refA, refM, refZ)
	fixture.persistLock(nil)
	recorder := newUpdateRecorder(fixture.lock)
	recorder.files[recorder.path(refM)] = append([]byte(nil), dataM...)

	changes, err := updatePinsWithOps(context.Background(), fixture.cfg, recorder.ops())

	if err != nil {
		t.Fatalf("updatePinsWithOps() error = %v", err)
	}
	requireChangeRefs(t, changes, refA, refM, refZ)
	if got, want := fixture.requestOrder(), []string{"/a", "/m", "/z"}; !slices.Equal(got, want) {
		t.Fatalf("fetch order = %q, want %q", got, want)
	}
	wantReads := []string{recorder.path(refA), recorder.path(refM), recorder.path(refZ)}
	if !slices.Equal(recorder.reads, wantReads) {
		t.Fatalf("preparation order = %q, want %q", recorder.reads, wantReads)
	}
	wantPublications := []string{recorder.path(refA), recorder.path(refZ)}
	if !slices.Equal(recorder.publications, wantPublications) {
		t.Fatalf("publication order = %q, want %q", recorder.publications, wantPublications)
	}
}

func TestStageActiveRefsCarriesTrustLevel(t *testing.T) {
	data := moduleYAML("module", "package")
	fixture := newUpdateFixture(t, nil)
	officialRef := "github.com/atomikpanda/dotular/modules/official@main"
	githubRef := "github.com/example/project@main"
	externalRef := fixture.ref("/external")
	fixture.configure(externalRef, githubRef, officialRef)
	fixture.persistLock(nil)
	fixture.swapTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(data)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	}))

	staged, err := stageActiveRefs(context.Background(), fixture.cfg, fixture.lock, maxAggregateStagedBytes)

	if err != nil {
		t.Fatalf("stageActiveRefs() error = %v", err)
	}
	wantTrust := map[string]TrustLevel{
		officialRef: Official,
		githubRef:   GitHub,
		externalRef: External,
	}
	for _, got := range staged {
		if got.trust != wantTrust[got.ref] {
			t.Fatalf("trust for %q = %s, want %s", got.ref, got.trust, wantTrust[got.ref])
		}
	}
}

func TestUpdatePinsWarnsOncePerUniqueExternalRefInLexicalOrderAfterStaging(t *testing.T) {
	fixture := newUpdateFixture(t, map[string][]byte{
		"/a": moduleYAML("module-a", "a"),
		"/z": moduleYAML("module-z", "z"),
	})
	refA, refZ := fixture.ref("/a"), fixture.ref("/z")
	fixture.configure(refZ, refA, refZ, refA)
	fixture.persistLock(nil)
	recorder := newUpdateRecorder(fixture.lock)
	warningsBeforePreparation := false
	recorder.beforeFirstRead = func() {
		warningsBeforePreparation = slices.Equal(recorder.warnings, []string{
			fmt.Sprintf("[external] %s", refA),
			fmt.Sprintf("[external] %s", refZ),
		}) &&
			fixture.requestCount(refA) == 1 &&
			fixture.requestCount(refZ) == 1
	}

	_, err := updatePinsWithOps(context.Background(), fixture.cfg, recorder.ops())

	if err != nil {
		t.Fatalf("updatePinsWithOps() error = %v", err)
	}
	want := []string{
		fmt.Sprintf("[external] %s", refA),
		fmt.Sprintf("[external] %s", refZ),
	}
	if !slices.Equal(recorder.warnings, want) {
		t.Fatalf("warnings = %q, want %q", recorder.warnings, want)
	}
	if !warningsBeforePreparation {
		t.Fatalf("warnings were not emitted after complete staging and before preparation: events=%q", recorder.events)
	}
}

func TestUpdatePinsDoesNotWarnForOfficialOrGitHubRefs(t *testing.T) {
	data := moduleYAML("module", "package")
	fixture := newUpdateFixture(t, nil)
	officialRef := "github.com/atomikpanda/dotular/modules/official@main"
	githubRef := "github.com/example/project@main"
	fixture.configure(githubRef, officialRef)
	fixture.persistLock(nil)
	fixture.swapTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(data)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	}))
	recorder := newUpdateRecorder(fixture.lock)

	_, err := updatePinsWithOps(context.Background(), fixture.cfg, recorder.ops())

	if err != nil {
		t.Fatalf("updatePinsWithOps() error = %v", err)
	}
	if len(recorder.warnings) != 0 {
		t.Fatalf("warnings = %q, want none", recorder.warnings)
	}
}

func TestUpdatePinsStagingFailureEmitsNoPartialExternalWarnings(t *testing.T) {
	fixture := newUpdateFixture(t, map[string][]byte{
		"/a": moduleYAML("module-a", "a"),
		"/z": []byte("name: [unterminated\n"),
	})
	refA, refZ := fixture.ref("/a"), fixture.ref("/z")
	fixture.configure(refZ, refA)
	fixture.persistLock(nil)
	recorder := newUpdateRecorder(fixture.lock)

	changes, err := updatePinsWithOps(context.Background(), fixture.cfg, recorder.ops())

	if err == nil {
		t.Fatal("updatePinsWithOps() error = nil, want staging failure")
	}
	if changes != nil {
		t.Fatalf("changes = %#v, want nil", changes)
	}
	if len(recorder.warnings) != 0 {
		t.Fatalf("warnings = %q, want none for incomplete staging", recorder.warnings)
	}
	recorder.requireNoMutationOps(t)
}

func TestUpdatePinsProductionWarningUsesUI(t *testing.T) {
	data := moduleYAML("external", "external")
	fixture := newUpdateFixture(t, map[string][]byte{"/external": data})
	ref := fixture.ref("/external")
	fixture.configure(ref)
	fixture.persistLock(nil)
	var warningOutput bytes.Buffer
	u := ui.New(io.Discard, &warningOutput)

	_, err := UpdatePins(context.Background(), fixture.cfg, fixture.configPath, u)

	if err != nil {
		t.Fatalf("UpdatePins() error = %v", err)
	}
	if !strings.Contains(warningOutput.String(), "[external] "+ref) {
		t.Fatalf("warning output = %q, want external warning for %q", warningOutput.String(), ref)
	}
}

func TestUpdatePinsWithOpsNoActiveRefsIsNoOp(t *testing.T) {
	inactiveRef := "inactive.example/module"
	inactiveEntry := LockEntry{
		SHA256:    strings.Repeat("i", sha256.Size*2),
		FetchedAt: time.Date(2025, 6, 7, 8, 9, 10, 0, time.UTC),
		URL:       "https://inactive.example/module",
	}
	lock := &LockFile{Registry: map[string]LockEntry{inactiveRef: inactiveEntry}}
	before := cloneTestLock(lock)
	recorder := newUpdateRecorder(lock)
	inactivePath := recorder.path(inactiveRef)
	recorder.files[inactivePath] = []byte("inactive cache")

	changes, err := updatePinsWithOps(context.Background(), config.Config{}, recorder.ops())

	if err != nil {
		t.Fatalf("updatePinsWithOps() error = %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("changes = %#v, want empty", changes)
	}
	recorder.requireNoMutationOps(t)
	if len(recorder.warnings) != 0 {
		t.Fatalf("warnings = %q, want none", recorder.warnings)
	}
	if !reflect.DeepEqual(lock, before) {
		t.Fatalf("loaded lock changed\nbefore: %#v\nafter:  %#v", before, lock)
	}
	if got := recorder.files[inactivePath]; !bytes.Equal(got, []byte("inactive cache")) {
		t.Fatalf("inactive cache = %q, want unchanged", got)
	}
}

func TestUpdatePinsWithOpsAcquisitionFailureSkipsLoad(t *testing.T) {
	fixture, recorder, _, _ := newSingleUpdateScenario(t, false)
	errAcquire := errors.New("acquire failed")
	ops := recorder.ops()
	ops.acquire = func() (func() error, error) {
		return nil, errAcquire
	}
	ops.loadLock = func() (LockFile, error) {
		t.Fatal("loadLock called after acquisition failure")
		return LockFile{}, nil
	}

	changes, err := updatePinsWithOps(context.Background(), fixture.cfg, ops)

	if !errors.Is(err, errAcquire) {
		t.Fatalf("updatePinsWithOps() error = %v, want %v", err, errAcquire)
	}
	if changes != nil {
		t.Fatalf("changes = %#v, want nil", changes)
	}
}

func TestUpdatePinsWithOpsReleaseFailureReturnsCompleteChanges(t *testing.T) {
	fixture, recorder, ref, _ := newSingleUpdateScenario(t, false)
	errRelease := errors.New("release failed")
	ops := recorder.ops()
	ops.acquire = func() (func() error, error) {
		return func() error {
			recorder.events = append(recorder.events, "release")
			return errRelease
		}, nil
	}

	changes, err := updatePinsWithOps(context.Background(), fixture.cfg, ops)

	if !errors.Is(err, errRelease) {
		t.Fatalf("updatePinsWithOps() error = %v, want %v", err, errRelease)
	}
	requireChangeRefs(t, changes, ref)
	if got := recorder.events[len(recorder.events)-1]; got != "release" {
		t.Fatalf("last event = %q, want release; events=%q", got, recorder.events)
	}
}

func TestUpdatePinsWithOpsJoinsPublicationAndReleaseFailures(t *testing.T) {
	fixture, recorder, ref, _ := newSingleUpdateScenario(t, false)
	errPublish := errors.New("publish failed")
	errRelease := errors.New("release failed")
	recorder.publishErrors[recorder.path(ref)] = errPublish
	ops := recorder.ops()
	ops.acquire = func() (func() error, error) {
		return func() error {
			recorder.events = append(recorder.events, "release")
			return errRelease
		}, nil
	}

	changes, err := updatePinsWithOps(context.Background(), fixture.cfg, ops)

	if !errors.Is(err, errPublish) || !errors.Is(err, errRelease) {
		t.Fatalf("updatePinsWithOps() error = %v, want joined %v and %v", err, errPublish, errRelease)
	}
	requireChangeRefs(t, changes, ref)
	wantTail := []string{"publish:" + recorder.path(ref), "release"}
	if got := recorder.events[len(recorder.events)-2:]; !slices.Equal(got, wantTail) {
		t.Fatalf("event tail = %q, want %q; events=%q", got, wantTail, recorder.events)
	}
}

func TestUpdatePinsWithOpsSerializesLoadThroughPublication(t *testing.T) {
	fixture, first, _, _ := newSingleUpdateScenario(t, false)
	second := newUpdateRecorder(fixture.lock)
	firstPublishing := make(chan struct{})
	allowFirstPublication := make(chan struct{})
	firstReleased := make(chan struct{})
	secondAcquireStarted := make(chan struct{})
	secondLoaded := make(chan struct{})

	firstOps := first.ops()
	firstOps.acquire = func() (func() error, error) {
		return func() error {
			close(firstReleased)
			return nil
		}, nil
	}
	firstPublish := firstOps.publish
	firstOps.publish = func(path string, data []byte) error {
		close(firstPublishing)
		<-allowFirstPublication
		return firstPublish(path, data)
	}

	secondOps := second.ops()
	secondOps.acquire = func() (func() error, error) {
		close(secondAcquireStarted)
		<-firstReleased
		return func() error { return nil }, nil
	}
	secondLoad := secondOps.loadLock
	secondOps.loadLock = func() (LockFile, error) {
		close(secondLoaded)
		return secondLoad()
	}

	firstDone := make(chan error, 1)
	go func() {
		_, err := updatePinsWithOps(context.Background(), fixture.cfg, firstOps)
		firstDone <- err
	}()
	<-firstPublishing

	secondDone := make(chan error, 1)
	go func() {
		_, err := updatePinsWithOps(context.Background(), fixture.cfg, secondOps)
		secondDone <- err
	}()
	<-secondAcquireStarted

	select {
	case <-firstReleased:
		t.Fatal("first update released while publication was blocked")
	default:
	}
	select {
	case <-secondLoaded:
		t.Fatal("second update loaded before first publication and release")
	default:
	}

	close(allowFirstPublication)
	if err := <-firstDone; err != nil {
		t.Fatalf("first update error = %v", err)
	}
	select {
	case <-secondLoaded:
	case <-time.After(time.Second):
		t.Fatal("second update did not load after first publication and release")
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second update error = %v", err)
	}
}

func TestUpdatePinsNoActiveRefsDoesNotCreateMissingLock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, "dotular.yaml")
	lockPath := LockPath(configPath)
	u := ui.New(io.Discard, io.Discard)

	changes, err := UpdatePins(context.Background(), config.Config{}, configPath, u)

	if err != nil {
		t.Fatalf("UpdatePins() error = %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("changes = %#v, want empty", changes)
	}
	if _, err := os.Stat(lockPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stat lockfile error = %v, want %v", err, fs.ErrNotExist)
	}
}

func TestUpdatePinsNoActiveRefsPreservesExistingInactiveLockAndCache(t *testing.T) {
	fixture := newUpdateFixture(t, nil)
	inactiveRef := "inactive.example/module"
	inactiveEntry := LockEntry{
		SHA256:    strings.Repeat("i", sha256.Size*2),
		FetchedAt: time.Date(2025, 6, 7, 8, 9, 10, 0, time.UTC),
		URL:       "https://inactive.example/module",
	}
	fixture.configure()
	fixture.persistLock(map[string]LockEntry{inactiveRef: inactiveEntry})
	fixture.seedCache(inactiveRef, []byte("inactive cache"))
	fixture.snapshotDurableState()
	u := ui.New(io.Discard, io.Discard)

	changes, err := UpdatePins(context.Background(), fixture.cfg, fixture.configPath, u)

	if err != nil {
		t.Fatalf("UpdatePins() error = %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("changes = %#v, want empty", changes)
	}
	fixture.requireDurableStateUnchanged()
}

func TestUpdatePinsWithOpsEmptyAndLocalOnlyConfigsSkipEveryOperation(t *testing.T) {
	tests := map[string]config.Config{
		"empty": {},
		"local only": {
			Modules: []config.Module{{Name: "local"}},
		},
	}
	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			errLoad := errors.New("load must not run")
			loadCalls := 0
			ops := updateOps{
				loadLock: func() (LockFile, error) {
					loadCalls++
					return LockFile{}, errLoad
				},
				saveLock: func(LockFile) error {
					t.Fatal("saveLock called for no-active config")
					return nil
				},
				cachePath: func(string) string {
					t.Fatal("cachePath called for no-active config")
					return ""
				},
				readFile: func(string) ([]byte, error) {
					t.Fatal("readFile called for no-active config")
					return nil, nil
				},
				publish: func(string, []byte) error {
					t.Fatal("publish called for no-active config")
					return nil
				},
				warn: func(string) {
					t.Fatal("warn called for no-active config")
				},
				acquire: func() (func() error, error) {
					t.Fatal("acquire called for no-active config")
					return nil, nil
				},
			}

			changes, err := updatePinsWithOps(context.Background(), cfg, ops)

			if err != nil {
				t.Fatalf("updatePinsWithOps() error = %v, want nil without loading lock", err)
			}
			if len(changes) != 0 {
				t.Fatalf("changes = %#v, want empty", changes)
			}
			if loadCalls != 0 {
				t.Fatalf("loadLock calls = %d, want 0", loadCalls)
			}
		})
	}
}

func TestUpdatePinsEmptyAndLocalOnlyConfigsIgnoreMalformedLock(t *testing.T) {
	tests := map[string]config.Config{
		"empty": {},
		"local only": {
			Modules: []config.Module{{Name: "local"}},
		},
	}
	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			configPath := filepath.Join(home, "dotular.yaml")
			lockPath := LockPath(configPath)
			malformed := []byte("registry: [unterminated\n")
			if err := os.WriteFile(lockPath, malformed, 0o644); err != nil {
				t.Fatal(err)
			}
			var warningOutput bytes.Buffer
			u := ui.New(io.Discard, &warningOutput)

			changes, err := UpdatePins(context.Background(), cfg, configPath, u)

			if err != nil {
				t.Fatalf("UpdatePins() error = %v, want nil without loading malformed lock", err)
			}
			if len(changes) != 0 {
				t.Fatalf("changes = %#v, want empty", changes)
			}
			got, err := os.ReadFile(lockPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, malformed) {
				t.Fatalf("lockfile = %q, want unchanged %q", got, malformed)
			}
			if warningOutput.Len() != 0 {
				t.Fatalf("warning output = %q, want empty", warningOutput.String())
			}
		})
	}
}

type updateRecorder struct {
	source          *LockFile
	durable         LockFile
	goos            string
	paths           map[string]string
	files           map[string][]byte
	readErrors      map[string]error
	publishErrors   map[string]error
	saveError       error
	reads           []string
	publications    []string
	warnings        []string
	saveAttempts    []*LockFile
	events          []string
	beforeFirstRead func()
	beforeSave      func()
}

func newUpdateRecorder(lock *LockFile) *updateRecorder {
	return &updateRecorder{
		source:        lock,
		durable:       *cloneTestLock(lock),
		goos:          runtime.GOOS,
		paths:         make(map[string]string),
		files:         make(map[string][]byte),
		readErrors:    make(map[string]error),
		publishErrors: make(map[string]error),
	}
}

func (r *updateRecorder) path(ref string) string {
	if path, ok := r.paths[ref]; ok {
		return path
	}
	return "/cache/" + strings.NewReplacer("/", "_", "@", "_", ":", "_", ".", "_").Replace(ref)
}

func (r *updateRecorder) ops() updateOps {
	return updateOps{
		goos:           r.goos,
		maxStagedBytes: maxAggregateStagedBytes,
		acquire: func() (func() error, error) {
			return func() error { return nil }, nil
		},
		loadLock: func() (LockFile, error) {
			return *r.source, nil
		},
		saveLock: func(lock LockFile) error {
			if r.beforeSave != nil {
				r.beforeSave()
			}
			captured := lock
			r.saveAttempts = append(r.saveAttempts, &captured)
			r.events = append(r.events, "save")
			if r.saveError != nil {
				return r.saveError
			}
			r.durable = *cloneTestLock(&lock)
			return nil
		},
		cachePath: r.path,
		readFile: func(path string) ([]byte, error) {
			if len(r.reads) == 0 && r.beforeFirstRead != nil {
				r.beforeFirstRead()
			}
			r.reads = append(r.reads, path)
			r.events = append(r.events, "read:"+path)
			if err := r.readErrors[path]; err != nil {
				return nil, err
			}
			data, ok := r.files[path]
			if !ok {
				return nil, fs.ErrNotExist
			}
			return append([]byte(nil), data...), nil
		},
		publish: func(path string, data []byte) error {
			r.publications = append(r.publications, path)
			r.events = append(r.events, "publish:"+path)
			if err := r.publishErrors[path]; err != nil {
				return err
			}
			r.files[path] = append([]byte(nil), data...)
			return nil
		},
		warn: func(message string) {
			r.warnings = append(r.warnings, message)
			r.events = append(r.events, "warn:"+message)
		},
	}
}

func (r *updateRecorder) requireNoMutationOps(t *testing.T) {
	t.Helper()
	if len(r.reads) != 0 || len(r.saveAttempts) != 0 || len(r.publications) != 0 {
		t.Fatalf(
			"reads = %q, saves = %d, publications = %q, want none",
			r.reads,
			len(r.saveAttempts),
			r.publications,
		)
	}
}

func (r *updateRecorder) requirePathUntouched(t *testing.T, path string) {
	t.Helper()
	for _, paths := range [][]string{r.reads, r.publications} {
		if slices.Contains(paths, path) {
			t.Fatalf("inactive path %q was accessed: reads=%q publications=%q", path, r.reads, r.publications)
		}
	}
}

func newSingleUpdateScenario(t *testing.T, pinned bool) (*updateFixture, *updateRecorder, string, []byte) {
	t.Helper()
	data := moduleYAML("module", "package")
	fixture := newUpdateFixture(t, map[string][]byte{"/module": data})
	ref := fixture.ref("/module")
	fixture.configure(ref)
	entries := map[string]LockEntry(nil)
	if pinned {
		entries = map[string]LockEntry{ref: {SHA256: sha256Hex(data)}}
	}
	fixture.persistLock(entries)
	return fixture, newUpdateRecorder(fixture.lock), ref, data
}

func requireChangeRefs(t *testing.T, changes []PinChange, refs ...string) {
	t.Helper()
	got := make([]string, len(changes))
	for i := range changes {
		got[i] = changes[i].Ref
	}
	if !slices.Equal(got, refs) {
		t.Fatalf("change refs = %q, want %q", got, refs)
	}
}

func changeForRef(t *testing.T, changes []PinChange, ref string) PinChange {
	t.Helper()
	for _, change := range changes {
		if change.Ref == ref {
			return change
		}
	}
	t.Fatalf("missing change for %q", ref)
	return PinChange{}
}

func requirePublishedBytes(t *testing.T, recorder *updateRecorder, ref string, want []byte) {
	t.Helper()
	path := recorder.path(ref)
	if !slices.Equal(recorder.publications, []string{path}) {
		t.Fatalf("publications = %q, want %q", recorder.publications, []string{path})
	}
	if got := recorder.files[path]; !bytes.Equal(got, want) {
		t.Fatalf("published bytes = %q, want %q", got, want)
	}
}

func responseForRef(t *testing.T, fixture *updateFixture, ref string) []byte {
	t.Helper()
	path := "/" + strings.TrimPrefix(ParseRef(ref).Path, "/")
	data, ok := fixture.responses[path]
	if !ok {
		t.Fatalf("missing response for %q", ref)
	}
	return data
}

func indexOfEvent(events []string, want string) int {
	for i, event := range events {
		if event == want {
			return i
		}
	}
	return -1
}

type updateFixture struct {
	t               *testing.T
	server          *httptest.Server
	responses       map[string][]byte
	requestsMu      sync.Mutex
	requests        map[string]int
	requestsInOrder []string
	configPath      string
	lockPath        string
	cfg             config.Config
	lock            *LockFile
	cachePaths      map[string]string
	lockBytes       []byte
	cacheBytes      map[string][]byte
	lockCopy        *LockFile
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
		f.requestsInOrder = append(f.requestsInOrder, r.URL.Path)
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
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
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

func (f *updateFixture) requestOrder() []string {
	f.t.Helper()
	f.requestsMu.Lock()
	defer f.requestsMu.Unlock()
	return append([]string(nil), f.requestsInOrder...)
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

func TestWithRegistryMutationLockExecutesCallback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	configPath := filepath.Join(t.TempDir(), "dotular.yaml")
	if err := os.WriteFile(configPath, []byte("modules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	called := false
	if err := WithRegistryMutationLock(configPath, func() error {
		called = true
		return nil
	}); err != nil {
		t.Fatalf("WithRegistryMutationLock() error = %v", err)
	}
	if !called {
		t.Fatal("WithRegistryMutationLock() did not execute callback")
	}
}

func TestWithRegistryMutationLockJoinsCallbackAndReleaseErrors(t *testing.T) {
	callbackErr := errors.New("callback failed")
	releaseErr := errors.New("release failed")
	var events []string

	err := withRegistryMutationLock(
		"dotular.yaml",
		func(configPath string) (func() error, error) {
			events = append(events, "acquire "+configPath)
			return func() error {
				events = append(events, "release")
				return releaseErr
			}, nil
		},
		func() error {
			events = append(events, "callback")
			return callbackErr
		},
	)

	if !errors.Is(err, callbackErr) || !errors.Is(err, releaseErr) {
		t.Fatalf("withRegistryMutationLock() error = %v, want callback and release errors", err)
	}
	wantEvents := []string{"acquire dotular.yaml", "callback", "release"}
	if !slices.Equal(events, wantEvents) {
		t.Fatalf("events = %q, want %q", events, wantEvents)
	}
}
