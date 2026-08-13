//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package registry

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func acquirePlatformWriterLock(lockPath string) (func() error, error) {
	dir, err := os.Open(filepath.Dir(lockPath))
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(dir.Fd()), unix.LOCK_EX); err != nil {
		dir.Close()
		return nil, err
	}
	return func() error {
		return errors.Join(unix.Flock(int(dir.Fd()), unix.LOCK_UN), dir.Close())
	}, nil
}
