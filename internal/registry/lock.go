package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// LockFile records the SHA-256 checksums of every fetched registry module.
// It lives alongside dotular.yaml and should be committed to the repo.
type LockFile struct {
	Registry map[string]LockEntry `yaml:"registry,omitempty"`
}

// LockEntry records a single cached module's checksum and fetch time.
type LockEntry struct {
	SHA256    string    `yaml:"sha256"`
	FetchedAt time.Time `yaml:"fetched_at"`
	URL       string    `yaml:"url"`
}

// LockPath returns the lockfile path derived from the config file path.
func LockPath(configPath string) string {
	dir := filepath.Dir(configPath)
	return filepath.Join(dir, "dotular.lock.yaml")
}

// AcquireWriterLock serializes a lockfile load-modify-save transaction across
// processes. lockTarget must name an existing regular file, normally the
// dotular config; using it avoids non-portable directory locks and creates no
// coordination artifact.
func AcquireWriterLock(path, lockTarget string) (func() error, error) {
	canonical, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve lockfile path: %w", err)
	}
	if dir, err := filepath.EvalSymlinks(filepath.Dir(canonical)); err == nil {
		canonical = filepath.Join(dir, filepath.Base(canonical))
	}
	target, err := filepath.Abs(lockTarget)
	if err != nil {
		return nil, fmt.Errorf("resolve writer lock target: %w", err)
	}
	if evaluated, err := filepath.EvalSymlinks(target); err == nil {
		target = evaluated
	}
	release, err := acquirePlatformWriterLock(canonical, target)
	if err != nil {
		return nil, fmt.Errorf("acquire registry writer lock: %w", err)
	}
	return release, nil
}

// LoadLock reads the lockfile, returning an empty LockFile if not found.
func LoadLock(path string) (*LockFile, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &LockFile{Registry: make(map[string]LockEntry)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read lockfile: %w", err)
	}
	var lf LockFile
	if err := yaml.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("parse lockfile: %w", err)
	}
	if lf.Registry == nil {
		lf.Registry = make(map[string]LockEntry)
	}
	return &lf, nil
}

// SaveLock writes the lockfile atomically.
func SaveLock(path string, lf *LockFile) error {
	data, err := yaml.Marshal(lf)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write lockfile: %w", err)
	}
	return os.Rename(tmp, path)
}
