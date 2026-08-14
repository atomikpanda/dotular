package registry

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func acquireRegistryUpdateLock(configPath string) (func() error, error) {
	path, err := registryUpdateLockPath(configPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create registry update lock directory: %w", err)
	}
	return lockRegistryUpdateFile(path)
}

func registryUpdateLockPath(configPath string) (string, error) {
	absolutePath, err := filepath.Abs(configPath)
	if err != nil {
		return "", fmt.Errorf("resolve registry config path: %w", err)
	}
	canonicalPath, err := filepath.EvalSymlinks(absolutePath)
	if err != nil {
		return "", fmt.Errorf("canonicalize registry config path: %w", err)
	}
	identity := normalizeRegistryUpdateIdentity(canonicalPath, runtime.GOOS)
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(identity)))
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate user cache directory: %w", err)
	}
	return filepath.Join(cacheDir, "dotular", "registry", "update-locks", key+".lock"), nil
}

func normalizeRegistryUpdateIdentity(path, goos string) string {
	identity := filepath.Clean(path)
	if goos == "windows" || goos == "darwin" {
		identity = strings.ToLower(identity)
	}
	return identity
}
