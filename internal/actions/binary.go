package actions

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/atomikpanda/dotular/internal/color"
	"github.com/atomikpanda/dotular/internal/fsutil"
	"github.com/atomikpanda/dotular/internal/httputil"
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
		fmt.Printf("    %s\n", color.Dim("[dry-run] "+a.Describe()))
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
		// The download error is the useful one, but a close failure alongside it
		// says the partial bytes never landed either, so keep both.
		return errors.Join(fmt.Errorf("download %s: %w", a.SourceURL, err), tmpFile.Close())
	}
	// Close reports flush errors that io.Copy cannot see; dropping it would
	// install and chmod 0755 a short binary as though the download succeeded.
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("finish download %s: %w", a.SourceURL, err)
	}

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
			if err := fsutil.CopyContents(tmpPath, destPath); err != nil {
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

	// StreamClient, not Client: a released binary can legitimately take longer
	// than an end-to-end timeout allows, and io.Copy below reads the body inside
	// that window. StreamClient bounds a stall instead, so a hung server still
	// fails rather than hanging the run.
	resp, err := httputil.StreamClient.Do(req)
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

// writeBinary streams r into destPath by writing a temp file and renaming it
// into place, like the plain-binary path in Run does. Creating destPath directly
// would truncate it before the first byte arrives, so any failure mid-stream
// would leave a working binary replaced by a corrupt one that keeps its
// executable mode — reporting failure while making things worse. An atomic
// replace leaves the original untouched instead.
//
// The caller chmods destPath afterwards; the temp file's 0600 is deliberate
// until then, so a half-written binary is never executable.
func writeBinary(r io.Reader, destPath string) error {
	// Same directory as the target: a cross-filesystem rename is not atomic.
	tmp, err := os.CreateTemp(filepath.Dir(destPath), "."+filepath.Base(destPath)+"-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	if _, err := io.Copy(tmp, r); err != nil {
		return errors.Join(err, tmp.Close())
	}
	// Close reports flush errors that io.Copy cannot see; dropping it would
	// extract a truncated binary and report success.
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, destPath)
}
