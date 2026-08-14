//go:build !windows

package registry

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func lockRegistryUpdateFile(path string) (func() error, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open registry update lock: %w", err)
	}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("lock registry update: %w", err),
			closeRegistryUpdateLockFile(file),
		)
	}

	return func() error {
		unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		if unlockErr != nil {
			unlockErr = fmt.Errorf("unlock registry update: %w", unlockErr)
		}
		return errors.Join(unlockErr, closeRegistryUpdateLockFile(file))
	}, nil
}

func closeRegistryUpdateLockFile(file *os.File) error {
	if err := file.Close(); err != nil {
		return fmt.Errorf("close registry update lock: %w", err)
	}
	return nil
}
