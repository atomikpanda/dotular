package actions

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/atomikpanda/dotular/internal/platform"
)

// FileAction copies, symlinks, or syncs a config file between the repo and the system.
type FileAction struct {
	Source      string // repo-side path (relative, from config dir)
	Destination string // system-side directory (may contain ~ and $VARS)
	Direction   string // "push" | "pull" | "sync"
	Link        bool   // create a symlink instead of copying (push only)
}

func (a *FileAction) Describe() string {
	dest := filepath.Join(platform.ExpandPath(a.Destination), filepath.Base(a.Source))
	if a.Link {
		return fmt.Sprintf("link   %s -> %s", a.Source, dest)
	}
	switch a.Direction {
	case "pull":
		return fmt.Sprintf("pull   %s <- %s", a.Source, dest)
	case "sync":
		return fmt.Sprintf("sync   %s <-> %s", a.Source, dest)
	default: // push
		return fmt.Sprintf("push   %s -> %s", a.Source, dest)
	}
}

func (a *FileAction) Run(ctx context.Context, dryRun bool) error {
	dest := platform.ExpandPath(a.Destination)
	target := filepath.Join(dest, filepath.Base(a.Source))

	if dryRun {
		fmt.Printf("    [dry-run] %s\n", a.Describe())
		return nil
	}

	// Symlinks are always push — the system path points into the repo.
	if a.Link {
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return fmt.Errorf("create destination directory: %w", err)
		}
		return createSymlink(a.Source, target)
	}

	switch a.Direction {
	case "pull":
		return a.runPull(target)
	case "sync":
		return a.runSync(target)
	default: // push
		return a.runPush(dest, target)
	}
}

// runPush copies the repo file to the system destination (repo → system).
func (a *FileAction) runPush(destDir, target string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	return copyFile(a.Source, target)
}

// runPull copies the system file back into the repo (system → repo).
func (a *FileAction) runPull(target string) error {
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return fmt.Errorf("pull: system file does not exist: %s", target)
	}
	if err := os.MkdirAll(filepath.Dir(a.Source), 0o755); err != nil {
		return fmt.Errorf("create repo directory: %w", err)
	}
	return copyFile(target, a.Source)
}

// runSync compares repo and system files, copies if one side is missing,
// and prompts the user to resolve any conflict when both exist but differ.
func (a *FileAction) runSync(target string) error {
	repoExists := fileExists(a.Source)
	sysExists := fileExists(target)

	switch {
	case !repoExists && !sysExists:
		return fmt.Errorf("sync: neither repo nor system file exists (%s)", filepath.Base(a.Source))

	case repoExists && !sysExists:
		// System copy is missing — push repo → system.
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create destination directory: %w", err)
		}
		fmt.Printf("    sync: system copy missing, pushing repo -> system\n")
		return copyFile(a.Source, target)

	case !repoExists && sysExists:
		// Repo copy is missing — pull system → repo.
		if err := os.MkdirAll(filepath.Dir(a.Source), 0o755); err != nil {
			return fmt.Errorf("create repo directory: %w", err)
		}
		fmt.Printf("    sync: repo copy missing, pulling system -> repo\n")
		return copyFile(target, a.Source)

	default:
		// Both exist — check for differences.
		equal, err := filesEqual(a.Source, target)
		if err != nil {
			return fmt.Errorf("sync: compare files: %w", err)
		}
		if equal {
			fmt.Printf("    sync: already in sync\n")
			return nil
		}
		return resolveConflict(a.Source, target)
	}
}

// resolveConflict prompts the user to choose how to resolve a sync conflict.
func resolveConflict(repoPath, sysPath string) error {
	name := filepath.Base(repoPath)
	fmt.Printf("\n    CONFLICT: %s differs between repo and system\n", name)
	fmt.Printf("      [1] keep repo   (push repo -> system)\n")
	fmt.Printf("      [2] keep system (pull system -> repo)\n")
	fmt.Printf("      [s] skip\n")
	fmt.Printf("    > ")

	choice, err := readLine(os.Stdin)
	if err != nil {
		return fmt.Errorf("read conflict choice: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "1":
		fmt.Printf("    -> pushing repo copy to system\n")
		return copyFile(repoPath, sysPath)
	case "2":
		fmt.Printf("    -> pulling system copy to repo\n")
		return copyFile(sysPath, repoPath)
	case "s", "":
		fmt.Printf("    -> skipped\n")
		return nil
	default:
		fmt.Printf("    unrecognised choice %q — skipping\n", choice)
		return nil
	}
}

// --- helpers -----------------------------------------------------------------

func createSymlink(src, dst string) error {
	abs, err := filepath.Abs(src)
	if err != nil {
		return fmt.Errorf("resolve source path: %w", err)
	}
	if _, err := os.Lstat(dst); err == nil {
		if err := os.Remove(dst); err != nil {
			return fmt.Errorf("remove existing destination: %w", err)
		}
	}
	return os.Symlink(abs, dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy contents: %w", err)
	}
	return out.Close()
}

func filesEqual(a, b string) (bool, error) {
	aData, err := os.ReadFile(a)
	if err != nil {
		return false, err
	}
	bData, err := os.ReadFile(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(aData, bData), nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readLine(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	if scanner.Scan() {
		return scanner.Text(), nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", nil
}
