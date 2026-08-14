package registry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/atomikpanda/dotular/internal/config"
	"github.com/atomikpanda/dotular/internal/httputil"
	"github.com/atomikpanda/dotular/internal/ui"
)

// PinStatus classifies an active registry ref against its pre-command pin.
type PinStatus string

const (
	PinStatusMissing PinStatus = "missing"
	PinStatusMatch   PinStatus = "match"
	PinStatusDrift   PinStatus = "drift"
)

const maxAggregateStagedBytes = 4 * httputil.MaxBodySize

// PinChange describes the pre-command and proposed checksums for one active ref.
type PinChange struct {
	Ref       string
	OldSHA256 string
	NewSHA256 string
	Status    PinStatus
}

type stagedRef struct {
	ref            string
	oldSHA256      string
	oldPresent     bool
	proposedSHA256 string
	module         RemoteModule
	trust          TrustLevel
	status         PinStatus
	data           []byte
	replacement    LockEntry
}

func stageActiveRefs(
	ctx context.Context,
	cfg config.Config,
	lock *LockFile,
	maxStagedBytes int,
) ([]stagedRef, error) {
	refs := make([]string, 0, len(cfg.Modules))
	for _, module := range cfg.Modules {
		if module.From != "" {
			refs = append(refs, module.From)
		}
	}
	slices.Sort(refs)
	refs = slices.Compact(refs)

	staged := make([]stagedRef, 0, len(refs))
	stagedBytes := 0
	for _, ref := range refs {
		entry, err := stageOneRef(ctx, ref, lock)
		if err != nil {
			return nil, fmt.Errorf("stage registry ref %q: %w", ref, err)
		}
		if stagedBytes > maxStagedBytes || len(entry.data) > maxStagedBytes-stagedBytes {
			return nil, fmt.Errorf(
				"stage registry ref %q: aggregate staged response data exceeds the %d byte limit",
				ref,
				maxStagedBytes,
			)
		}
		stagedBytes += len(entry.data)
		staged = append(staged, entry)
	}

	stagedModules := make(map[string]*RemoteModule, len(staged))
	for i := range staged {
		stagedModules[staged[i].ref] = &staged[i].module
	}
	for _, module := range cfg.Modules {
		if !module.IsRegistry() {
			continue
		}

		remote := stagedModules[module.From]
		params := resolveParams(remote.Params, module.With)
		if _, err := renderItems(remote.Items, params); err != nil {
			name := module.Name
			if name == "" {
				name = remote.Name
			}
			return nil, fmt.Errorf("validate registry ref %q for module %q: %w", module.From, name, err)
		}
	}
	return staged, nil
}

func stageOneRef(ctx context.Context, ref string, lock *LockFile) (stagedRef, error) {
	data, module, replacement, trust, err := fetchNoWrite(ctx, ref, nil)
	if err != nil {
		return stagedRef{}, err
	}

	old, oldPresent := lock.Registry[ref]
	status := PinStatusMissing
	if oldPresent {
		status = PinStatusDrift
		if old.SHA256 == replacement.SHA256 {
			status = PinStatusMatch
		}
	}

	return stagedRef{
		ref:            ref,
		oldSHA256:      old.SHA256,
		oldPresent:     oldPresent,
		proposedSHA256: replacement.SHA256,
		module:         *module,
		trust:          trust,
		status:         status,
		data:           data,
		replacement:    replacement,
	}, nil
}

func changesFromStaged(staged []stagedRef) []PinChange {
	changes := make([]PinChange, len(staged))
	for i, ref := range staged {
		changes[i] = PinChange{
			Ref:       ref.ref,
			OldSHA256: ref.oldSHA256,
			NewSHA256: ref.proposedSHA256,
			Status:    ref.status,
		}
	}
	return changes
}

func replacementLock(lock *LockFile, staged []stagedRef) *LockFile {
	replacement := &LockFile{Registry: make(map[string]LockEntry, len(lock.Registry)+len(staged))}
	for ref, entry := range lock.Registry {
		replacement.Registry[ref] = entry
	}
	for _, ref := range staged {
		replacement.Registry[ref.ref] = ref.replacement
	}
	return replacement
}

type updateOps struct {
	goos           string
	maxStagedBytes int
	acquire        func() (func() error, error)
	loadLock       func() (LockFile, error)
	saveLock       func(LockFile) error
	cachePath      func(string) string
	readFile       func(string) ([]byte, error)
	publish        func(string, []byte) error
	warn           func(string)
}

func moduleCacheCollisionKey(goos, path string) string {
	key := filepath.Clean(path)
	if goos == "windows" || goos == "darwin" {
		return strings.ToLower(key)
	}
	return key
}

func rejectModuleCachePathCollisions(
	activeRefs []string,
	lockedRefs []string,
	pathFor func(string) string,
	goos string,
) error {
	active := make(map[string]struct{}, len(activeRefs))
	for _, ref := range activeRefs {
		active[ref] = struct{}{}
	}

	refs := append(append([]string(nil), activeRefs...), lockedRefs...)
	slices.Sort(refs)
	refs = slices.Compact(refs)
	for i, left := range refs {
		for _, right := range refs[i+1:] {
			_, leftActive := active[left]
			_, rightActive := active[right]
			if !leftActive && !rightActive {
				continue
			}

			leftPath := pathFor(left)
			rightPath := pathFor(right)
			collisionKey := moduleCacheCollisionKey(goos, leftPath)
			if collisionKey == moduleCacheCollisionKey(goos, rightPath) {
				return fmt.Errorf(
					"module cache path collision: refs %q and %q both map to %q",
					left,
					right,
					collisionKey,
				)
			}
		}
	}

	return nil
}

func prepareActiveTarget(
	path string,
	want []byte,
	readFile func(string) ([]byte, error),
) bool {
	got, err := readFile(path)
	return err == nil && bytes.Equal(got, want)
}

// UpdatePins serializes the lock and cache transition for the complete active
// registry set.
func UpdatePins(
	ctx context.Context,
	cfg config.Config,
	configPath string,
	u *ui.UI,
) ([]PinChange, error) {
	lockPath := LockPath(configPath)
	return updatePinsWithOps(ctx, cfg, updateOps{
		goos:           runtime.GOOS,
		maxStagedBytes: maxAggregateStagedBytes,
		acquire: func() (func() error, error) {
			return acquireRegistryUpdateLock(configPath)
		},
		loadLock: func() (LockFile, error) {
			lock, err := LoadLock(lockPath)
			if err != nil {
				return LockFile{}, err
			}
			return *lock, nil
		},
		saveLock: func(lock LockFile) error {
			return SaveLock(lockPath, &lock)
		},
		cachePath: moduleCachePath,
		readFile:  os.ReadFile,
		publish:   writeCacheFile,
		warn:      u.Warn,
	})
}

func updatePinsWithOps(
	ctx context.Context,
	cfg config.Config,
	ops updateOps,
) (changes []PinChange, err error) {
	if len(CollectActiveRefs(cfg)) == 0 {
		return []PinChange{}, nil
	}

	release, err := ops.acquire()
	if err != nil {
		return nil, err
	}
	defer func() {
		if releaseErr := release(); releaseErr != nil {
			err = errors.Join(err, releaseErr)
		}
	}()

	loaded, err := ops.loadLock()
	if err != nil {
		return nil, err
	}
	lock := &loaded

	staged, err := stageActiveRefs(ctx, cfg, lock, ops.maxStagedBytes)
	if err != nil {
		return nil, err
	}

	changes = changesFromStaged(staged)

	for _, ref := range staged {
		if ref.trust == External {
			ops.warn(fmt.Sprintf("[external] %s", ref.ref))
		}
	}
	nextLock := replacementLock(lock, staged)

	if err := rejectModuleCachePathCollisions(
		activeRefsFromStaged(staged),
		lockRefs(lock),
		ops.cachePath,
		ops.goos,
	); err != nil {
		return changes, err
	}

	retained := make([]bool, len(staged))
	for i, ref := range staged {
		retained[i] = prepareActiveTarget(
			ops.cachePath(ref.ref),
			ref.data,
			ops.readFile,
		)
	}

	if err := ops.saveLock(*nextLock); err != nil {
		return changes, err
	}

	for i, ref := range staged {
		if retained[i] {
			continue
		}
		if err := ops.publish(ops.cachePath(ref.ref), ref.data); err != nil {
			return changes, err
		}
	}

	return changes, nil
}

func activeRefsFromStaged(staged []stagedRef) []string {
	refs := make([]string, len(staged))
	for i := range staged {
		refs[i] = staged[i].ref
	}
	return refs
}

func lockRefs(lock *LockFile) []string {
	refs := make([]string, 0, len(lock.Registry))
	for ref := range lock.Registry {
		refs = append(refs, ref)
	}
	return refs
}
