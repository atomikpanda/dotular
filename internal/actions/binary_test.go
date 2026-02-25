package actions

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
		t.Error("expected error from failed download")
	}
}

func TestCopyFilePath(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	os.WriteFile(src, []byte("hello"), 0o644)

	if err := copyFilePath(src, dst); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(dst)
	if string(data) != "hello" {
		t.Errorf("copied = %q", string(data))
	}
}
