package actions

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/atomikpanda/dotular/internal/ageutil"
	"github.com/atomikpanda/dotular/internal/color"
	"github.com/atomikpanda/dotular/internal/platform"
)

// FileAction copies, symlinks, or syncs a config file between the repo and the system.
//
// Idempotency:
//   - Link=true: implements Idempotent. IsApplied checks that the symlink at
//     the destination already exists and resolves to the correct absolute source.
//   - Push/pull/sync (copy): does not implement Idempotent. Direction logic
//     (e.g. filesEqual in sync) provides implicit idempotency; use skip_if for
//     custom guards.
//
// Permissions: when Permissions is non-empty (Unix octal, e.g. "0600"), the
// mode is enforced on the destination file after every write. On apply, if
// the existing file's mode does not match, it is corrected.
//
// Encryption: when Encrypted is true and AgeKey is set, files are stored in
// the repo with an ".age" extension. On push the repo file is decrypted to the
// destination; on pull the system file is re-encrypted before writing to the repo.
type FileAction struct {
	Source      string // repo-side path
	Destination string // system-side directory (may contain ~ and $VARS)
	Direction   string // "push" | "pull" | "sync"
	Link        bool
	Permissions string       // Unix octal string, e.g. "0600"
	Encrypted   bool
	AgeKey      *ageutil.Key // required when Encrypted is true
}

// resolvedTarget returns the fully expanded destination file path.
// If the destination has a file extension (e.g. ~/.wezterm.lua), it is treated
// as a complete file path. Otherwise it is treated as a directory and the
// source basename is appended. A trailing "/" always forces directory treatment.
func (a *FileAction) ResolvedTarget() string {
	expanded := platform.ExpandPath(a.Destination)
	base := filepath.Base(expanded)
	if !strings.HasSuffix(a.Destination, "/") && filepath.Ext(base) != "" {
		return expanded
	}
	return filepath.Join(expanded, filepath.Base(a.Source))
}

// resolvedDir returns the parent directory of the resolved target.
func (a *FileAction) ResolvedDir() string {
	return filepath.Dir(a.ResolvedTarget())
}

func (a *FileAction) Describe() string {
	dest := a.ResolvedTarget()
	enc := ""
	if a.Encrypted {
		enc = " [encrypted]"
	}
	if a.Link {
		return fmt.Sprintf("link   %s -> %s%s", a.Source, dest, enc)
	}
	switch a.Direction {
	case "pull":
		return fmt.Sprintf("pull   %s <- %s%s", a.Source, dest, enc)
	case "sync":
		return fmt.Sprintf("sync   %s <-> %s%s", a.Source, dest, enc)
	default:
		return fmt.Sprintf("push   %s -> %s%s", a.Source, dest, enc)
	}
}

// PermissionsStatus returns a human-readable permissions annotation for use in
// status output, or "" when not applicable or already correct.
func (a *FileAction) PermissionsStatus() string {
	if a.Permissions == "" || a.Link {
		return ""
	}
	dest := a.ResolvedTarget()
	info, err := os.Stat(dest)
	if err != nil {
		return "" // file doesn't exist yet
	}
	mode, err := parseMode(a.Permissions)
	if err != nil {
		return fmt.Sprintf("[permissions: invalid %q]", a.Permissions)
	}
	actual := info.Mode().Perm()
	if actual == mode {
		return fmt.Sprintf("[permissions: %s ✓]", a.Permissions)
	}
	return fmt.Sprintf("[permissions: want %s, got %04o ⚠]", a.Permissions, actual)
}

// IsApplied implements Idempotent for link items.
func (a *FileAction) IsApplied(ctx context.Context) (bool, error) {
	if !a.Link {
		// Only link items support auto idempotency.
		return false, nil
	}
	target := a.ResolvedTarget()

	linkDest, err := os.Readlink(target)
	if err != nil {
		return false, nil // not a symlink or doesn't exist
	}
	abs, err := filepath.Abs(a.Source)
	if err != nil {
		return false, nil
	}
	return linkDest == abs, nil
}

func (a *FileAction) Run(ctx context.Context, dryRun bool) error {
	target := a.ResolvedTarget()
	dest := a.ResolvedDir()

	if dryRun {
		fmt.Printf("    %s\n", color.Dim("[dry-run] "+a.Describe()))
		if ps := a.PermissionsStatus(); ps != "" {
			fmt.Printf("    %s\n", color.Dim("          "+ps))
		}
		return nil
	}

	if a.Link {
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return fmt.Errorf("create destination directory: %w", err)
		}
		return createSymlink(a.Source, target)
	}

	var err error
	switch a.Direction {
	case "pull":
		err = a.runPull(target)
	case "sync":
		err = a.runSync(target)
	default:
		err = a.runPush(dest, target)
	}
	if err != nil {
		return err
	}

	return a.enforcePermissions(target)
}

// --- direction implementations -----------------------------------------------

func (a *FileAction) runPush(destDir, target string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	if a.Encrypted {
		return a.decryptTo(ageutil.RepoPath(a.Source), target)
	}
	return copyFile(a.Source, target)
}

func (a *FileAction) runPull(target string) error {
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return fmt.Errorf("pull: system file does not exist: %s", target)
	}
	if err := os.MkdirAll(filepath.Dir(a.Source), 0o755); err != nil {
		return fmt.Errorf("create repo directory: %w", err)
	}
	if a.Encrypted {
		return a.encryptFrom(target, ageutil.RepoPath(a.Source))
	}
	return copyFile(target, a.Source)
}

func (a *FileAction) runSync(target string) error {
	repoPath := a.Source
	if a.Encrypted {
		repoPath = ageutil.RepoPath(a.Source)
	}

	repoExists := fileExists(repoPath)
	sysExists := fileExists(target)

	switch {
	case !repoExists && !sysExists:
		return fmt.Errorf("sync: neither repo nor system file exists (%s)", filepath.Base(a.Source))

	case repoExists && !sysExists:
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create destination directory: %w", err)
		}
		fmt.Printf("    %s\n", color.Cyan("sync: system copy missing, pushing repo -> system"))
		if a.Encrypted {
			return a.decryptTo(repoPath, target)
		}
		return copyFile(repoPath, target)

	case !repoExists && sysExists:
		if err := os.MkdirAll(filepath.Dir(a.Source), 0o755); err != nil {
			return fmt.Errorf("create repo directory: %w", err)
		}
		fmt.Printf("    %s\n", color.Cyan("sync: repo copy missing, pulling system -> repo"))
		if a.Encrypted {
			return a.encryptFrom(target, repoPath)
		}
		return copyFile(target, a.Source)

	default:
		// Both exist — compare (decrypt repo copy for comparison if encrypted).
		equal, err := a.syncEqual(repoPath, target)
		if err != nil {
			return fmt.Errorf("sync: compare: %w", err)
		}
		if equal {
			fmt.Printf("    %s\n", color.Dim("sync: already in sync"))
			return nil
		}
		return a.resolveConflict(repoPath, target)
	}
}

// syncEqual compares the effective plaintext of both sides.
func (a *FileAction) syncEqual(repoPath, sysPath string) (bool, error) {
	if !a.Encrypted {
		return filesEqual(repoPath, sysPath)
	}
	// Decrypt repo side to a temp file for comparison.
	tmp, err := os.CreateTemp("", "dotular-cmp-*")
	if err != nil {
		return false, err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	if err := a.decryptTo(repoPath, tmpPath); err != nil {
		return false, err
	}
	return filesEqual(tmpPath, sysPath)
}

func (a *FileAction) resolveConflict(repoPath, sysPath string) error {
	name := filepath.Base(a.Source)
	fmt.Printf("\n    %s\n", color.BoldYellow("CONFLICT: "+name+" differs between repo and system"))
	fmt.Printf("      [1] keep repo   (push repo -> system)\n")
	fmt.Printf("      [2] keep system (pull system -> repo)\n")
	fmt.Printf("      [s] skip\n")
	fmt.Printf("    %s ", color.Bold(">"))

	choice, err := readLine(os.Stdin)
	if err != nil {
		return fmt.Errorf("read conflict choice: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "1":
		fmt.Printf("    %s pushing repo copy to system\n", color.Dim("->"))
		if a.Encrypted {
			return a.decryptTo(repoPath, sysPath)
		}
		return copyFile(repoPath, sysPath)
	case "2":
		fmt.Printf("    %s pulling system copy to repo\n", color.Dim("->"))
		if a.Encrypted {
			return a.encryptFrom(sysPath, repoPath)
		}
		return copyFile(sysPath, a.Source)
	default:
		fmt.Printf("    %s\n", color.Dim("-> skipped"))
		return nil
	}
}

// --- permissions -------------------------------------------------------------

func (a *FileAction) enforcePermissions(target string) error {
	if a.Permissions == "" {
		return nil
	}
	mode, err := parseMode(a.Permissions)
	if err != nil {
		return fmt.Errorf("invalid permissions %q: %w", a.Permissions, err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil // file may not exist yet (e.g. pull with no system file)
	}
	if info.Mode().Perm() != mode {
		if err := os.Chmod(target, mode); err != nil {
			return fmt.Errorf("chmod %s to %s: %w", target, a.Permissions, err)
		}
	}
	return nil
}

func parseMode(s string) (os.FileMode, error) {
	v, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return 0, err
	}
	return os.FileMode(v), nil
}

// --- encryption helpers ------------------------------------------------------

func (a *FileAction) decryptTo(src, dst string) error {
	if a.AgeKey == nil {
		return fmt.Errorf("encrypted file %s requires an age key (set age.identity or age.passphrase in dotular.yaml)", src)
	}
	return a.AgeKey.DecryptFile(src, dst)
}

func (a *FileAction) encryptFrom(src, dst string) error {
	if a.AgeKey == nil {
		return fmt.Errorf("encrypted file %s requires an age key (set age.identity or age.passphrase in dotular.yaml)", src)
	}
	return a.AgeKey.EncryptFile(src, dst)
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
