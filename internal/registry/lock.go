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
