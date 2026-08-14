package registry

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type cacheTempFile interface {
	Write([]byte) (int, error)
	Close() error
	Chmod(os.FileMode) error
	Name() string
}

type cacheWriteOps struct {
	createTemp func(string, string) (cacheTempFile, error)
	replace    func(string, string) error
}

func writeCacheFileWithOps(path string, data []byte, ops cacheWriteOps) error {
	tempPattern := "." + filepath.Base(path) + ".tmp-*"
	tempFile, err := ops.createTemp(filepath.Dir(path), tempPattern)
	if err != nil {
		return fmt.Errorf("create cache temporary file: %w", err)
	}

	tempPath := tempFile.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()

	closeAfterFailure := func(primary error) error {
		if closeErr := tempFile.Close(); closeErr != nil {
			return errors.Join(primary, fmt.Errorf("close cache temporary file: %w", closeErr))
		}
		return primary
	}

	if err := tempFile.Chmod(0o644); err != nil {
		return closeAfterFailure(fmt.Errorf("set cache temporary file mode: %w", err))
	}

	written, writeErr := tempFile.Write(data)
	if writeErr != nil {
		return closeAfterFailure(fmt.Errorf("write cache temporary file: %w", writeErr))
	}
	if written != len(data) {
		return closeAfterFailure(fmt.Errorf(
			"write cache temporary file: wrote %d of %d bytes: %w",
			written,
			len(data),
			io.ErrShortWrite,
		))
	}

	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close cache temporary file: %w", err)
	}

	if err := ops.replace(tempPath, path); err != nil {
		return fmt.Errorf("commit cache temporary file: %w", err)
	}

	return nil
}
