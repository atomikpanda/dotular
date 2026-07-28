package actions

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestBinaryActionDescribe(t *testing.T) {
	a := &BinaryAction{Name: "nvim", Version: "0.10.0", InstallTo: "~/.local/bin"}
	got := a.Describe()
	if got == "" {
		t.Error("Describe() should not be empty")
	}
}

func TestBinaryActionDescribeNoVersion(t *testing.T) {
	a := &BinaryAction{Name: "nvim", InstallTo: "~/.local/bin"}
	got := a.Describe()
	if got == "" {
		t.Error("Describe() should not be empty")
	}
}

func TestBinaryActionDryRun(t *testing.T) {
	a := &BinaryAction{Name: "test", SourceURL: "https://example.com/test", InstallTo: "/tmp"}
	if err := a.Run(context.Background(), true); err != nil {
		t.Errorf("dry run error: %v", err)
	}
}

func TestExtractFromTarGz(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "test.tar.gz")
	destPath := filepath.Join(dir, "mybinary")

	// Create a tar.gz with a binary inside.
	f, _ := os.Create(archivePath)
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	content := []byte("binary-content")
	tw.WriteHeader(&tar.Header{
		Name: "subdir/mybinary",
		Mode: 0o755,
		Size: int64(len(content)),
	})
	tw.Write(content)
	tw.Close()
	gw.Close()
	f.Close()

	if err := extractFromTarGz(archivePath, "mybinary", destPath); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(destPath)
	if string(data) != "binary-content" {
		t.Errorf("extracted = %q", string(data))
	}
}

func TestExtractFromTarGzNotFound(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "empty.tar.gz")

	f, _ := os.Create(archivePath)
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	tw.Close()
	gw.Close()
	f.Close()

	err := extractFromTarGz(archivePath, "missing", filepath.Join(dir, "out"))
	if err == nil {
		t.Error("expected error for missing binary")
	}
}

func TestExtractFromZip(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "test.zip")
	destPath := filepath.Join(dir, "mybinary")

	f, _ := os.Create(archivePath)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("subdir/mybinary")
	w.Write([]byte("zip-binary"))
	zw.Close()
	f.Close()

	if err := extractFromZip(archivePath, "mybinary", destPath); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(destPath)
	if string(data) != "zip-binary" {
		t.Errorf("extracted = %q", string(data))
	}
}

func TestExtractFromZipNotFound(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "empty.zip")

	f, _ := os.Create(archivePath)
	zw := zip.NewWriter(f)
	zw.Close()
	f.Close()

	err := extractFromZip(archivePath, "missing", filepath.Join(dir, "out"))
	if err == nil {
		t.Error("expected error for missing binary")
	}
}

// A failed extraction must leave an already-installed binary exactly as it was.
// Writing destPath in place would truncate it before the failure was known, so
// the user would lose a working tool and get an error at the same time.
func TestWriteBinaryFailureLeavesExistingBinaryIntact(t *testing.T) {
	dir := t.TempDir()
	destPath := filepath.Join(dir, "mybinary")
	original := []byte("original-working-binary\n")
	if err := os.WriteFile(destPath, original, 0o755); err != nil {
		t.Fatal(err)
	}

	// A reader that yields some bytes then fails stands in for a corrupt archive
	// entry (a gzip CRC mismatch reaches writeBinary exactly this way).
	r := io.MultiReader(strings.NewReader("partial"), errReader{})
	if err := writeBinary(r, destPath); err == nil {
		t.Fatal("writeBinary() = nil error, want the read failure to be reported")
	}

	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("os.ReadFile(destPath) error = %v, want the original binary still in place", err)
	}
	if !bytes.Equal(data, original) {
		t.Errorf("destPath content = %q, want the original %q", data, original)
	}
	info, err := os.Stat(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("destPath mode = %v, want the original 0755", info.Mode().Perm())
	}

	// The temp file must not be left behind next to the target.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("install dir contains %v, want only the original binary", names)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("archive entry read failed") }

func TestBinaryActionRunPlainBinary(t *testing.T) {
	dir := t.TempDir()
	binaryContent := []byte("#!/bin/sh\necho hello\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(binaryContent)
	}))
	defer srv.Close()

	a := &BinaryAction{
		Name:      "testbin",
		SourceURL: srv.URL + "/testbin",
		InstallTo: dir,
	}
	if err := a.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	installed := filepath.Join(dir, "testbin")
	data, err := os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(binaryContent) {
		t.Errorf("installed content = %q", string(data))
	}

	// Verify permissions.
	info, _ := os.Stat(installed)
	if info.Mode().Perm()&0o755 != 0o755 {
		t.Errorf("permissions = %o", info.Mode().Perm())
	}
}

func TestBinaryActionRunTarGz(t *testing.T) {
	dir := t.TempDir()

	// Create tar.gz archive in memory.
	archivePath := filepath.Join(dir, "archive.tar.gz")
	f, _ := os.Create(archivePath)
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	content := []byte("binary-data")
	tw.WriteHeader(&tar.Header{Name: "dir/mybin", Mode: 0o755, Size: int64(len(content))})
	tw.Write(content)
	tw.Close()
	gw.Close()
	f.Close()

	archiveData, _ := os.ReadFile(archivePath)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archiveData)
	}))
	defer srv.Close()

	installDir := filepath.Join(dir, "bin")
	a := &BinaryAction{
		Name:      "mybin",
		SourceURL: srv.URL + "/archive.tar.gz",
		InstallTo: installDir,
	}
	if err := a.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(installDir, "mybin"))
	if string(data) != "binary-data" {
		t.Errorf("installed = %q", string(data))
	}
}

func TestBinaryActionRunZip(t *testing.T) {
	dir := t.TempDir()

	archivePath := filepath.Join(dir, "archive.zip")
	f, _ := os.Create(archivePath)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("dir/mybin")
	w.Write([]byte("zip-binary"))
	zw.Close()
	f.Close()

	archiveData, _ := os.ReadFile(archivePath)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archiveData)
	}))
	defer srv.Close()

	installDir := filepath.Join(dir, "bin")
	a := &BinaryAction{
		Name:      "mybin",
		SourceURL: srv.URL + "/archive.zip",
		InstallTo: installDir,
	}
	if err := a.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(installDir, "mybin"))
	if string(data) != "zip-binary" {
		t.Errorf("installed = %q", string(data))
	}
}

// A body that ends before its declared Content-Length must fail the install
// outright. Same class as a truncated registry module, one layer down: a short
// binary is still an executable file, so installing it would leave a broken
// tool that looks installed.
func TestBinaryActionRunRejectsTruncatedDownload(t *testing.T) {
	full := "#!/bin/sh\necho hello\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(full)))
		fmt.Fprint(w, full[:6])
	}))
	defer srv.Close()

	dir := t.TempDir()
	a := &BinaryAction{
		Name:      "truncated",
		SourceURL: srv.URL + "/truncated",
		InstallTo: dir,
	}
	if err := a.Run(context.Background(), false); err == nil {
		t.Fatal("Run() = nil error, want a failure for a body shorter than its Content-Length")
	}
	if _, err := os.Stat(filepath.Join(dir, "truncated")); !os.IsNotExist(err) {
		t.Errorf("os.Stat(install path) error = %v, want the path not to exist after a truncated download", err)
	}
}

func TestBinaryActionRunDownloadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	a := &BinaryAction{
		Name:      "test",
		SourceURL: srv.URL + "/bin",
		InstallTo: t.TempDir(),
	}
	err := a.Run(context.Background(), false)
	if err == nil {
		t.Fatal("expected error from failed download")
	}
	// The download error is joined with the temp file's close error, so guard
	// that the join still reports the download failure rather than hiding it
	// behind a (usually nil) close error.
	if !strings.Contains(err.Error(), "download "+a.SourceURL) {
		t.Errorf("Run() error = %q, want it to name the failed download of %s", err, a.SourceURL)
	}
}
