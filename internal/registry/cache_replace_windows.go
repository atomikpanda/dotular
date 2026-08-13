//go:build windows

package registry

import "golang.org/x/sys/windows"

func replaceCacheFile(from, to string) error {
	return windows.Rename(from, to)
}
