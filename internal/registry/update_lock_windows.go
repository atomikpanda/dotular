//go:build windows

package registry

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

func acquirePlatformWriterLock(lockPath string) (func() error, error) {
	sum := sha256.Sum256([]byte(strings.ToLower(lockPath)))
	name, err := windows.UTF16PtrFromString(fmt.Sprintf("dotular-registry-%x", sum))
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil && err != windows.ERROR_ALREADY_EXISTS {
		return nil, err
	}
	result, err := windows.WaitForSingleObject(handle, windows.INFINITE)
	if err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	if result != windows.WAIT_OBJECT_0 && result != windows.WAIT_ABANDONED {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("unexpected mutex wait result %d", result)
	}
	return func() error {
		return errors.Join(windows.ReleaseMutex(handle), windows.CloseHandle(handle))
	}, nil
}
