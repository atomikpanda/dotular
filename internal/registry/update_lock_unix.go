//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package registry

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func acquirePlatformWriterLock(_ string, lockTarget string) (func() error, error) {
	file, err := os.Open(lockTarget)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		file.Close()
		return nil, err
	}
	return func() error {
		return errors.Join(unix.Flock(int(file.Fd()), unix.LOCK_UN), file.Close())
	}, nil
}
