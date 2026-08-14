package registry

import (
	"context"
	"fmt"
	"slices"

	"github.com/atomikpanda/dotular/internal/config"
)

// PinStatus classifies an active registry ref against its pre-command pin.
type PinStatus string

const (
	PinStatusMissing PinStatus = "missing"
	PinStatusMatch   PinStatus = "match"
	PinStatusDrift   PinStatus = "drift"
)

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
	status         PinStatus
	data           []byte
	replacement    LockEntry
}

func stageActiveRefs(ctx context.Context, cfg config.Config, lock *LockFile) ([]stagedRef, error) {
	refs := make([]string, 0, len(cfg.Modules))
	for _, module := range cfg.Modules {
		if module.From != "" {
			refs = append(refs, module.From)
		}
	}
	slices.Sort(refs)
	refs = slices.Compact(refs)

	staged := make([]stagedRef, 0, len(refs))
	for _, ref := range refs {
		entry, err := stageOneRef(ctx, ref, lock)
		if err != nil {
			return nil, fmt.Errorf("stage registry ref %q: %w", ref, err)
		}
		staged = append(staged, entry)
	}
	return staged, nil
}

func stageOneRef(ctx context.Context, ref string, lock *LockFile) (stagedRef, error) {
	data, module, replacement, _, err := fetchNoWrite(ctx, ref, nil)
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
