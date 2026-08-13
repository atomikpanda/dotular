package registry

import (
	"crypto/sha256"
	"errors"
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

// acquireUpdateLock serializes writers for one lockfile across processes. The
// lock lives in the user cache rather than beside the committed lockfile, where
// a persistent coordination file would pollute the repository.
func acquireUpdateLock(path string) (func() error, error) {
	canonical, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve lockfile path: %w", err)
	}
	if dir, err := filepath.EvalSymlinks(filepath.Dir(canonical)); err == nil {
		canonical = filepath.Join(dir, filepath.Base(canonical))
	}

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("resolve cache directory: %w", err)
	}
	lockDir := filepath.Join(cacheDir, "dotular", "locks")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return nil, fmt.Errorf("create update lock directory: %w", err)
	}
	sum := sha256.Sum256([]byte(canonical))
	file, err := os.OpenFile(filepath.Join(lockDir, fmt.Sprintf("%x.lock", sum)), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open update lock: %w", err)
	}
	if err := lockUpdateFile(file); err != nil {
		file.Close()
		return nil, fmt.Errorf("acquire update lock: %w", err)
	}
	return func() error {
		return errors.Join(unlockUpdateFile(file), file.Close())
	}, nil
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
