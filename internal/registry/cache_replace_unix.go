//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package registry

import "os"

func replaceCacheFile(from, to string) error {
	return os.Rename(from, to)
}
