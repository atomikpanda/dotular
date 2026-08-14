//go:build !windows

package registry

import "os"

func replaceCacheFile(tempPath string, path string) error {
	return os.Rename(tempPath, path)
}
