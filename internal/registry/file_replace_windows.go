//go:build windows

package registry

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

var moveFileEx = windows.MoveFileEx

const windowsLongPathThreshold = 248

func replaceFile(tempPath string, path string) error {
	flags := uint32(windows.MOVEFILE_WRITE_THROUGH)

	_, err := os.Stat(path)
	switch {
	case err == nil:
		flags |= windows.MOVEFILE_REPLACE_EXISTING
	case os.IsNotExist(err):
	default:
		return fmt.Errorf("inspect destination before MoveFileEx: %w", err)
	}

	tempMovePath, err := normalizeMoveFileExPath(tempPath)
	if err != nil {
		return fmt.Errorf("normalize temporary file path for MoveFileEx: %w", err)
	}
	tempPathUTF16, err := windows.UTF16PtrFromString(tempMovePath)
	if err != nil {
		return fmt.Errorf("encode temporary file path for MoveFileEx: %w", err)
	}

	destinationMovePath, err := normalizeMoveFileExPath(path)
	if err != nil {
		return fmt.Errorf("normalize destination path for MoveFileEx: %w", err)
	}
	pathUTF16, err := windows.UTF16PtrFromString(destinationMovePath)
	if err != nil {
		return fmt.Errorf("encode destination path for MoveFileEx: %w", err)
	}

	if err := moveFileEx(tempPathUTF16, pathUTF16, flags); err != nil {
		return fmt.Errorf("replace file with MoveFileEx: %w", err)
	}

	return nil
}

func normalizeMoveFileExPath(path string) (string, error) {
	if len(path) >= 4 {
		if path[:4] == `\??\` {
			return path, nil
		}
		if os.IsPathSeparator(path[0]) &&
			os.IsPathSeparator(path[1]) &&
			path[2] == '?' &&
			os.IsPathSeparator(path[3]) {
			return path, nil
		}
	}

	pathLength := len(path)
	if !filepath.IsAbs(path) {
		workingDirectory, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get working directory: %w", err)
		}
		pathLength += len(workingDirectory) + 1
	}
	if pathLength < windowsLongPathThreshold {
		return path, nil
	}

	isUNC := false
	isDevice := false
	if len(path) >= 2 &&
		os.IsPathSeparator(path[0]) &&
		os.IsPathSeparator(path[1]) {
		if len(path) >= 4 &&
			path[2] == '.' &&
			os.IsPathSeparator(path[3]) {
			isDevice = true
		} else {
			isUNC = true
		}
	}

	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path: %w", err)
	}
	switch {
	case isUNC:
		return `\\?\UNC\` + absolutePath[2:], nil
	case isDevice:
		return absolutePath, nil
	default:
		return `\\?\` + absolutePath, nil
	}
}
