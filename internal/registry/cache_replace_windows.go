//go:build windows

package registry

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

var moveCacheFileEx = windows.MoveFileEx

func replaceCacheFile(tempPath string, path string) error {
	flags := uint32(windows.MOVEFILE_WRITE_THROUGH)

	_, err := os.Stat(path)
	switch {
	case err == nil:
		flags |= windows.MOVEFILE_REPLACE_EXISTING
	case os.IsNotExist(err):
	default:
		return fmt.Errorf("inspect cache destination before MoveFileEx: %w", err)
	}

	tempPathUTF16, err := windows.UTF16PtrFromString(tempPath)
	if err != nil {
		return fmt.Errorf("encode cache temporary path for MoveFileEx: %w", err)
	}

	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("encode cache destination path for MoveFileEx: %w", err)
	}

	if err := moveCacheFileEx(tempPathUTF16, pathUTF16, flags); err != nil {
		return fmt.Errorf("MoveFileEx cache commit: %w", err)
	}

	return nil
}
