package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const registryMutationLockFilename = "registry-mutation.lock"

// WithRegistryMutationLock serializes every mutation of the global registry
// cache and its corresponding durable lockfiles.
func WithRegistryMutationLock(callback func() error) error {
	return withRegistryMutationLock(acquireRegistryUpdateLock, callback)
}

func withRegistryMutationLock(
	acquire func() (func() error, error),
	callback func() error,
) (err error) {
	release, err := acquire()
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, release())
	}()
	return callback()
}

func acquireRegistryUpdateLock() (func() error, error) {
	path, err := registryUpdateLockPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create registry update lock directory: %w", err)
	}
	return lockRegistryUpdateFile(path)
}

func registryUpdateLockPath() (string, error) {
	registryDir, err := registryCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate user cache directory: %w", err)
	}
	return filepath.Join(filepath.Dir(registryDir), registryMutationLockFilename), nil
}
