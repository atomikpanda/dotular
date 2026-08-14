//go:build windows

package registry

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestReplaceCacheFileWindowsMoveFileExFlags(t *testing.T) {
	originalMoveCacheFileEx := moveCacheFileEx
	t.Cleanup(func() {
		moveCacheFileEx = originalMoveCacheFileEx
	})

	tests := []struct {
		name            string
		seedDestination bool
		wantFlags       uint32
	}{
		{
			name:            "create if absent uses write through only",
			seedDestination: false,
			wantFlags:       windows.MOVEFILE_WRITE_THROUGH,
		},
		{
			name:            "replace if present uses replace existing and write through",
			seedDestination: true,
			wantFlags: windows.MOVEFILE_REPLACE_EXISTING |
				windows.MOVEFILE_WRITE_THROUGH,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "cache.json")
			tempPath := filepath.Join(dir, ".cache.json.tmp-platform")

			if test.seedDestination {
				if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
					t.Fatalf("os.WriteFile(%q) error = %v", path, err)
				}
			}

			var gotFlags uint32
			moveCacheFileEx = func(
				existingFileName *uint16,
				newFileName *uint16,
				flags uint32,
			) error {
				gotTempPath := windows.UTF16PtrToString(existingFileName)
				gotPath := windows.UTF16PtrToString(newFileName)
				if gotTempPath != tempPath {
					t.Fatalf("MoveFileEx source = %q, want %q", gotTempPath, tempPath)
				}
				if gotPath != path {
					t.Fatalf("MoveFileEx destination = %q, want %q", gotPath, path)
				}
				gotFlags = flags
				return nil
			}

			if err := replaceCacheFile(tempPath, path); err != nil {
				t.Fatalf("replaceCacheFile() error = %v", err)
			}
			if gotFlags != test.wantFlags {
				t.Fatalf("MoveFileEx flags = %#x, want exactly %#x", gotFlags, test.wantFlags)
			}
			if gotFlags&windows.MOVEFILE_COPY_ALLOWED != 0 {
				t.Fatalf("MoveFileEx flags = %#x, MOVEFILE_COPY_ALLOWED must not be set", gotFlags)
			}
		})
	}
}
