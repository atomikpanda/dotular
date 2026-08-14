//go:build windows

package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

var moveCacheFileEx = windows.MoveFileEx

const windowsLongPathThreshold = 248

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

	tempMovePath, err := normalizeMoveFileExPath(tempPath)
	if err != nil {
		return fmt.Errorf("normalize cache temporary path for MoveFileEx: %w", err)
	}
	tempPathUTF16, err := windows.UTF16PtrFromString(tempMovePath)
	if err != nil {
		return fmt.Errorf("encode cache temporary path for MoveFileEx: %w", err)
	}

	destinationMovePath, err := normalizeMoveFileExPath(path)
	if err != nil {
		return fmt.Errorf("normalize cache destination path for MoveFileEx: %w", err)
	}
	pathUTF16, err := windows.UTF16PtrFromString(destinationMovePath)
	if err != nil {
		return fmt.Errorf("encode cache destination path for MoveFileEx: %w", err)
	}

	if err := moveCacheFileEx(tempPathUTF16, pathUTF16, flags); err != nil {
		return fmt.Errorf("MoveFileEx cache commit: %w", err)
	}

	return nil
}

func normalizeMoveFileExPath(path string) (string, error) {
	if strings.HasPrefix(path, `\\?\`) || strings.HasPrefix(path, `\??\`) {
		return path, nil
	}
	if filepath.IsAbs(path) && len(path) < windowsLongPathThreshold {
		return path, nil
	}

	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if len(absolutePath) < windowsLongPathThreshold {
		return path, nil
	}
	if strings.HasPrefix(absolutePath, `\\.\`) {
		return absolutePath, nil
	}
	if strings.HasPrefix(absolutePath, `\\`) {
		return `\\?\UNC\` + absolutePath[2:], nil
	}
	return `\\?\` + absolutePath, nil
}
