package registry

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

type fakeCacheTempFile struct {
	name       string
	writeN     int
	writeErr   error
	closeErr   error
	chmodErr   error
	chmodMode  os.FileMode
	writeCalls int
	closeCalls int
	closed     bool
}

func (f *fakeCacheTempFile) Write(_ []byte) (int, error) {
	f.writeCalls++
	return f.writeN, f.writeErr
}

func (f *fakeCacheTempFile) Close() error {
	f.closeCalls++
	f.closed = true
	return f.closeErr
}

func (f *fakeCacheTempFile) Chmod(mode os.FileMode) error {
	f.chmodMode = mode
	return f.chmodErr
}

func (f *fakeCacheTempFile) Name() string {
	return f.name
}

func TestWriteCacheFileWithOpsCreateError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	writeTestFile(t, path, []byte("old"))
	createErr := errors.New("create failed")

	err := writeCacheFileWithOps(path, []byte("new"), cacheWriteOps{
		createTemp: func(gotDir string, gotPattern string) (cacheTempFile, error) {
			if gotDir != dir {
				t.Fatalf("createTemp directory = %q, want %q", gotDir, dir)
			}
			if gotPattern != ".cache.json.tmp-*" {
				t.Fatalf("createTemp pattern = %q, want %q", gotPattern, ".cache.json.tmp-*")
			}
			return nil, createErr
		},
		replace: func(string, string) error {
			t.Fatal("replace called after create failure")
			return nil
		},
	})
	if !errors.Is(err, createErr) {
		t.Fatalf("writeCacheFileWithOps() error = %v, want errors.Is(createErr)", err)
	}

	assertTestFileData(t, path, []byte("old"))
	assertDirectoryNames(t, dir, []string{"cache.json"})
}

func TestWriteCacheFileWithOpsChmodError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	tempPath := filepath.Join(dir, ".cache.json.tmp-fake")
	writeTestFile(t, path, []byte("old"))
	writeTestFile(t, tempPath, []byte("temporary"))

	chmodErr := errors.New("chmod failed")
	file := &fakeCacheTempFile{
		name:     tempPath,
		writeN:   len("new"),
		chmodErr: chmodErr,
	}

	err := writeCacheFileWithOps(path, []byte("new"), cacheWriteOps{
		createTemp: func(gotDir string, gotPattern string) (cacheTempFile, error) {
			if gotDir != dir {
				t.Fatalf("createTemp directory = %q, want %q", gotDir, dir)
			}
			if gotPattern != ".cache.json.tmp-*" {
				t.Fatalf("createTemp pattern = %q, want %q", gotPattern, ".cache.json.tmp-*")
			}
			return file, nil
		},
		replace: func(string, string) error {
			t.Fatal("replace called after chmod failure")
			return nil
		},
	})
	if !errors.Is(err, chmodErr) {
		t.Fatalf("writeCacheFileWithOps() error = %v, want errors.Is(chmodErr)", err)
	}
	if file.chmodMode.Perm() != 0o644 {
		t.Fatalf("Chmod() mode = %04o, want 0644", file.chmodMode.Perm())
	}
	if file.writeCalls != 0 {
		t.Fatalf("Write() calls = %d, want 0", file.writeCalls)
	}
	if file.closeCalls != 1 || !file.closed {
		t.Fatalf("Close() calls = %d, closed = %v; want one released handle", file.closeCalls, file.closed)
	}

	assertTestFileData(t, path, []byte("old"))
	assertDirectoryNames(t, dir, []string{"cache.json"})
}

func TestWriteCacheFileWithOpsWriteFailures(t *testing.T) {
	writeErr := errors.New("write failed")
	data := []byte("new")
	tests := []struct {
		name     string
		writeN   int
		writeErr error
		wantErr  error
	}{
		{
			name:    "short write",
			writeN:  len(data) - 1,
			wantErr: io.ErrShortWrite,
		},
		{
			name:     "explicit write error",
			writeN:   0,
			writeErr: writeErr,
			wantErr:  writeErr,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "cache.json")
			tempPath := filepath.Join(dir, ".cache.json.tmp-fake")
			writeTestFile(t, path, []byte("old"))
			writeTestFile(t, tempPath, []byte("temporary"))

			file := &fakeCacheTempFile{
				name:     tempPath,
				writeN:   test.writeN,
				writeErr: test.writeErr,
			}
			replaceCalled := false

			err := writeCacheFileWithOps(path, data, cacheWriteOps{
				createTemp: func(gotDir string, gotPattern string) (cacheTempFile, error) {
					if gotDir != dir {
						t.Fatalf("createTemp directory = %q, want %q", gotDir, dir)
					}
					if gotPattern != ".cache.json.tmp-*" {
						t.Fatalf("createTemp pattern = %q, want %q", gotPattern, ".cache.json.tmp-*")
					}
					return file, nil
				},
				replace: func(string, string) error {
					replaceCalled = true
					return nil
				},
			})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("writeCacheFileWithOps() error = %v, want errors.Is(%v)", err, test.wantErr)
			}
			if file.chmodMode.Perm() != 0o644 {
				t.Fatalf("Chmod() mode = %04o, want 0644", file.chmodMode.Perm())
			}
			if file.writeCalls != 1 {
				t.Fatalf("Write() calls = %d, want 1", file.writeCalls)
			}
			if file.closeCalls != 1 || !file.closed {
				t.Fatalf("Close() calls = %d, closed = %v; want one released handle", file.closeCalls, file.closed)
			}
			if replaceCalled {
				t.Fatal("replace called after write failure")
			}

			assertTestFileData(t, path, []byte("old"))
			assertDirectoryNames(t, dir, []string{"cache.json"})
		})
	}
}

func TestWriteCacheFileWithOpsCloseErrorReleasesHandle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	tempPath := filepath.Join(dir, ".cache.json.tmp-fake")
	writeTestFile(t, path, []byte("old"))
	writeTestFile(t, tempPath, []byte("temporary"))

	closeErr := errors.New("close failed")
	file := &fakeCacheTempFile{
		name:     tempPath,
		writeN:   len("new"),
		closeErr: closeErr,
	}
	replaceCalled := false

	err := writeCacheFileWithOps(path, []byte("new"), cacheWriteOps{
		createTemp: func(string, string) (cacheTempFile, error) {
			return file, nil
		},
		replace: func(string, string) error {
			replaceCalled = true
			return nil
		},
	})
	if !errors.Is(err, closeErr) {
		t.Fatalf("writeCacheFileWithOps() error = %v, want errors.Is(closeErr)", err)
	}
	if file.closeCalls != 1 || !file.closed {
		t.Fatalf("Close() calls = %d, closed = %v; want one released handle", file.closeCalls, file.closed)
	}
	if replaceCalled {
		t.Fatal("replace called after close failure")
	}

	assertTestFileData(t, path, []byte("old"))
	assertDirectoryNames(t, dir, []string{"cache.json"})
}

func TestWriteCacheFileWithOpsCommitFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	tempPath := filepath.Join(dir, ".cache.json.tmp-fake")
	writeTestFile(t, path, []byte("old"))
	writeTestFile(t, tempPath, []byte("new"))

	commitErr := errors.New("commit failed")
	file := &fakeCacheTempFile{
		name:   tempPath,
		writeN: len("new"),
	}

	err := writeCacheFileWithOps(path, []byte("new"), cacheWriteOps{
		createTemp: func(string, string) (cacheTempFile, error) {
			return file, nil
		},
		replace: func(gotTempPath string, gotPath string) error {
			if !file.closed {
				t.Fatal("replace called before the temporary-file handle was released")
			}
			if gotTempPath != tempPath {
				t.Fatalf("replace temporary path = %q, want %q", gotTempPath, tempPath)
			}
			if gotPath != path {
				t.Fatalf("replace destination path = %q, want %q", gotPath, path)
			}
			return commitErr
		},
	})
	if !errors.Is(err, commitErr) {
		t.Fatalf("writeCacheFileWithOps() error = %v, want errors.Is(commitErr)", err)
	}
	if file.closeCalls != 1 || !file.closed {
		t.Fatalf("Close() calls = %d, closed = %v; want one released handle", file.closeCalls, file.closed)
	}

	assertTestFileData(t, path, []byte("old"))
	assertDirectoryNames(t, dir, []string{"cache.json"})
}

func TestWriteCacheFileWithOpsUnsupportedAtomicOperation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	tempPath := filepath.Join(dir, ".cache.json.tmp-fake")
	oldData := []byte("old destination must survive unsupported commit")
	writeTestFile(t, path, oldData)
	writeTestFile(t, tempPath, []byte("new"))

	file := &fakeCacheTempFile{
		name:   tempPath,
		writeN: len("new"),
	}

	err := writeCacheFileWithOps(path, []byte("new"), cacheWriteOps{
		createTemp: func(string, string) (cacheTempFile, error) {
			return file, nil
		},
		replace: func(string, string) error {
			if !file.closed {
				t.Fatal("unsupported commit reported before the temporary-file handle was released")
			}
			return errors.ErrUnsupported
		},
	})
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("writeCacheFileWithOps() error = %v, want errors.Is(errors.ErrUnsupported)", err)
	}
	if file.closeCalls != 1 || !file.closed {
		t.Fatalf("Close() calls = %d, closed = %v; want one released handle", file.closeCalls, file.closed)
	}

	assertTestFileData(t, path, oldData)
	assertDirectoryNames(t, dir, []string{"cache.json"})
}

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", path, err)
	}
}

func assertTestFileData(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", path, err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("file %q data = %q, want %q", path, got, want)
	}
}

func assertDirectoryNames(t *testing.T, dir string, want []string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error = %v", dir, err)
	}

	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	slices.Sort(got)

	wantNames := append([]string(nil), want...)
	slices.Sort(wantNames)

	if !slices.Equal(got, wantNames) {
		t.Fatalf("directory %q names = %q, want %q", dir, got, wantNames)
	}
}
