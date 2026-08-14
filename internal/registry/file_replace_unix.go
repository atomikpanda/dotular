//go:build !windows

package registry

import "os"

func replaceFile(tempPath string, path string) error {
	return os.Rename(tempPath, path)
}
