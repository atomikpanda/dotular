//go:build windows

package registry

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/sys/windows"
)

func windowsMutexObjectName(lockPath string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(lockPath)))
	return fmt.Sprintf(`Global\dotular-registry-%x`, sum)
}

func acquirePlatformWriterLock(lockPath string) (func() error, error) {
	// The Global namespace coordinates Dotular processes in every Windows
	// session. The default security descriptor limits access to the creating
	// user's token, which is the user whose config and lockfile are protected.
	name, err := windows.UTF16PtrFromString(windowsMutexObjectName(lockPath))
	if err != nil {
		return nil, err
	}

	// Windows mutex ownership belongs to an OS thread, not a goroutine. Pin the
	// goroutine before waiting and keep it pinned until ReleaseMutex.
	runtime.LockOSThread()
	handle, err := windows.CreateMutexEx(
		nil,
		name,
		0,
		windows.SYNCHRONIZE|windows.MUTEX_MODIFY_STATE,
	)
	if err != nil && err != windows.ERROR_ALREADY_EXISTS {
		runtime.UnlockOSThread()
		return nil, err
	}
	result, err := windows.WaitForSingleObject(handle, windows.INFINITE)
	if err != nil {
		windows.CloseHandle(handle)
		runtime.UnlockOSThread()
		return nil, err
	}
	if result != windows.WAIT_OBJECT_0 && result != windows.WAIT_ABANDONED {
		windows.CloseHandle(handle)
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("unexpected mutex wait result %d", result)
	}
	return func() error {
		defer runtime.UnlockOSThread()
		return errors.Join(windows.ReleaseMutex(handle), windows.CloseHandle(handle))
	}, nil
}
