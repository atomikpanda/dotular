//go:build !windows

package registry

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceFileUsesAtomicRename(t *testing.T) {
	tests := []struct {
		name            string
		seedDestination bool
	}{
		{
			name:            "creates absent destination",
			seedDestination: false,
		},
		{
			name:            "replaces existing destination",
			seedDestination: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "dotular.lock.yaml")
			tempPath := path + ".tmp"
			writeTestFile(t, tempPath, []byte("new"))

			if test.seedDestination {
				writeTestFile(t, path, []byte("old"))
			}

			if err := replaceFile(tempPath, path); err != nil {
				t.Fatalf("replaceFile() error = %v", err)
			}

			assertTestFileData(t, path, []byte("new"))
			if _, err := os.Stat(tempPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("os.Stat(%q) error = %v, want os.ErrNotExist", tempPath, err)
			}
			assertDirectoryNames(t, dir, []string{"dotular.lock.yaml"})
		})
	}
}
