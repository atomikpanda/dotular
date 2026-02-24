package actions

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/atomikpanda/dotular/internal/platform"
)

// BinaryAction downloads a pre-built binary from a URL, optionally extracts
// it from a tar.gz or zip archive, and installs it to a target directory.
//
// Idempotency: BinaryAction does not implement Idempotent. Use skip_if to
// guard against redundant downloads (e.g. skip_if: test -f ~/.local/bin/nvim).
// The verify field is recommended for version-aware checks.
type BinaryAction struct {
	Name      string // binary name (used to locate within archive)
	Version   string // version string for display only
	SourceURL string // resolved for current OS
	InstallTo string // destination directory (may contain ~ / $VARS)
}

func (a *BinaryAction) Describe() string {
	v := ""
	if a.Version != "" {
		v = "@" + a.Version
	}
	dest := platform.ExpandPath(a.InstallTo)
	return fmt.Sprintf("install binary %s%s -> %s", a.Name, v, dest)
}

func (a *BinaryAction) Run(ctx context.Context, dryRun bool) error {
	if dryRun {
		fmt.Printf("    [dry-run] %s\n", a.Describe())
		return nil
	}

	destDir := platform.ExpandPath(a.InstallTo)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("create install dir: %w", err)
	}

	// Download to a temp file.
	tmpFile, err := os.CreateTemp("", "dotular-bin-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if err := downloadTo(ctx, a.SourceURL, tmpFile); err != nil {
		tmpFile.Close()
		return fmt.Errorf("download %s: %w", a.SourceURL, err)
	}
	tmpFile.Close()

	destPath := filepath.Join(destDir, a.Name)

	// Extract or install depending on the URL extension.
	lower := strings.ToLower(a.SourceURL)
	switch {
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		if err := extractFromTarGz(tmpPath, a.Name, destPath); err != nil {
			return fmt.Errorf("extract %s from archive: %w", a.Name, err)
		}
	case strings.HasSuffix(lower, ".zip"):
		if err := extractFromZip(tmpPath, a.Name, destPath); err != nil {
			return fmt.Errorf("extract %s from zip: %w", a.Name, err)
		}
	default:
		// Treat as a plain binary.
		if err := os.Rename(tmpPath, destPath); err != nil {
			if err := copyFilePath(tmpPath, destPath); err != nil {
				return fmt.Errorf("install binary: %w", err)
			}
		}
	}

	return os.Chmod(destPath, 0o755)
}

// --- download ----------------------------------------------------------------

func downloadTo(ctx context.Context, url string, dst *os.File) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "dotular/1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	_, err = io.Copy(dst, resp.Body)
	return err
}

// --- extraction --------------------------------------------------------------

func extractFromTarGz(archivePath, binaryName, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// Match the binary by its base name.
		if filepath.Base(hdr.Name) == binaryName {
			return writeBinary(tr, destPath)
		}
	}
	return fmt.Errorf("binary %q not found in archive", binaryName)
}

func extractFromZip(archivePath, binaryName, destPath string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		if filepath.Base(f.Name) == binaryName {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			return writeBinary(rc, destPath)
		}
	}
	return fmt.Errorf("binary %q not found in zip", binaryName)
}

func writeBinary(r io.Reader, destPath string) error {
	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, r)
	return err
}

func copyFilePath(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
