//go:build windows

package registry

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func lockRegistryUpdateFile(path string) (func() error, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open registry update lock: %w", err)
	}
	handle := windows.Handle(file.Fd())
	overlapped := new(windows.Overlapped)
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlapped); err != nil {
		return nil, errors.Join(
			fmt.Errorf("lock registry update: %w", err),
			closeRegistryUpdateLockFile(file),
		)
	}

	return func() error {
		unlockErr := windows.UnlockFileEx(handle, 0, 1, 0, overlapped)
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
