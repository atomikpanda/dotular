//go:build windows

package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestReplaceFileWindowsMoveFileExFlags(t *testing.T) {
	originalMoveFileEx := moveFileEx
	t.Cleanup(func() {
		moveFileEx = originalMoveFileEx
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
			path := filepath.Join(dir, "dotular.lock.yaml")
			tempPath := path + ".tmp"

			if test.seedDestination {
				if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
					t.Fatalf("os.WriteFile(%q) error = %v", path, err)
				}
			}

			var gotFlags uint32
			moveFileEx = func(
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

			if err := replaceFile(tempPath, path); err != nil {
				t.Fatalf("replaceFile() error = %v", err)
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

func TestReplaceFileWindowsExtendsLongLocalPaths(t *testing.T) {
	originalMoveFileEx := moveFileEx
	t.Cleanup(func() {
		moveFileEx = originalMoveFileEx
	})

	longDir := filepath.Join(t.TempDir(), strings.Repeat(`nested\`, 40))
	tempPath := filepath.Join(longDir, ".cache.json.tmp-platform")
	path := filepath.Join(longDir, "cache.json")

	if !filepath.IsAbs(tempPath) || !filepath.IsAbs(path) {
		t.Fatalf("test paths must be absolute: temp = %q, destination = %q", tempPath, path)
	}

	moveCalled := false
	moveFileEx = func(
		existingFileName *uint16,
		newFileName *uint16,
		_ uint32,
	) error {
		moveCalled = true
		gotTempPath := windows.UTF16PtrToString(existingFileName)
		gotPath := windows.UTF16PtrToString(newFileName)
		wantTempPath := `\\?\` + tempPath
		wantPath := `\\?\` + path
		if gotTempPath != wantTempPath {
			t.Fatalf("MoveFileEx long source = %q, want %q", gotTempPath, wantTempPath)
		}
		if gotPath != wantPath {
			t.Fatalf("MoveFileEx long destination = %q, want %q", gotPath, wantPath)
		}
		return nil
	}

	if err := replaceFile(tempPath, path); err != nil {
		t.Fatalf("replaceFile() error = %v", err)
	}
	if !moveCalled {
		t.Fatal("MoveFileEx was not called")
	}
}

func TestNormalizeMoveFileExPathLongForms(t *testing.T) {
	longTail := strings.Repeat(`nested\`, 40) + "cache.json"
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "drive path",
			path: `C:\` + longTail,
			want: `\\?\C:\` + longTail,
		},
		{
			name: "UNC path",
			path: `\\server\share\` + longTail,
			want: `\\?\UNC\server\share\` + longTail,
		},
		{
			name: "already extended path",
			path: `\\?\C:\` + longTail,
			want: `\\?\C:\` + longTail,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeMoveFileExPath(test.path)
			if err != nil {
				t.Fatalf("normalizeMoveFileExPath(%q) error = %v", test.path, err)
			}
			if got != test.want {
				t.Fatalf("normalizeMoveFileExPath(%q) = %q, want %q", test.path, got, test.want)
			}
		})
	}
}

func TestNormalizeMoveFileExPathMixedPrefixes(t *testing.T) {
	longTail := strings.Repeat(`nested\`, 40) + "cache.json"
	devicePath := `//./C:\` + longTail
	deviceWant, err := filepath.Abs(devicePath)
	if err != nil {
		t.Fatalf("filepath.Abs(%q) error = %v", devicePath, err)
	}

	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "mixed extended separators",
			path: `\\?/C:\` + longTail,
			want: `\\?/C:\` + longTail,
		},
		{
			name: "forward extended separators",
			path: `//?/C:\` + longTail,
			want: `//?/C:\` + longTail,
		},
		{
			name: "mixed device separators",
			path: devicePath,
			want: deviceWant,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeMoveFileExPath(test.path)
			if err != nil {
				t.Fatalf("normalizeMoveFileExPath(%q) error = %v", test.path, err)
			}
			if got != test.want {
				t.Fatalf("normalizeMoveFileExPath(%q) = %q, want %q", test.path, got, test.want)
			}
		})
	}
}

func TestNormalizeMoveFileExPathExtendsLongUncleanSpelling(t *testing.T) {
	longElement := strings.Repeat("a", 260)
	absolutePath := `C:\` + longElement + `\..\cache.json`
	relativePath := longElement + `\..\cache.json`
	absoluteRelativePath, err := filepath.Abs(relativePath)
	if err != nil {
		t.Fatalf("filepath.Abs(%q) error = %v", relativePath, err)
	}

	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "absolute",
			path: absolutePath,
			want: `\\?\C:\cache.json`,
		},
		{
			name: "relative",
			path: relativePath,
			want: `\\?\` + absoluteRelativePath,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeMoveFileExPath(test.path)
			if err != nil {
				t.Fatalf("normalizeMoveFileExPath(%q) error = %v", test.path, err)
			}
			if got != test.want {
				t.Fatalf("normalizeMoveFileExPath(%q) = %q, want %q", test.path, got, test.want)
			}
		})
	}
}
