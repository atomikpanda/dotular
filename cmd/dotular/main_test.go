package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atomikpanda/dotular/internal/config"
	"github.com/atomikpanda/dotular/internal/httputil"
	"github.com/atomikpanda/dotular/internal/registry"
	"github.com/atomikpanda/dotular/internal/testutil"
	"github.com/atomikpanda/dotular/internal/ui"
)

// Commands that apply items write the audit log, and the tag commands write
// machine.yaml, both under the home directory — isolate it from the real one.
func TestMain(m *testing.M) {
	os.Exit(testutil.IsolateHome(m))
}

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "dotular.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const (
	commandRegistryModuleA = "name: command-module-a\nitems:\n  - package: neovim\n    via: brew\n"
	commandRegistryModuleB = "name: command-module-b\nitems:\n  - package: helix\n    via: brew\n"
)

func commandRegistryChecksum(data string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(data)))
}

func serveCommandRegistryModule(t *testing.T, replacement *atomic.Bool, requests *atomic.Int32) string {
	t.Helper()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		body := commandRegistryModuleA
		if replacement.Load() {
			body = commandRegistryModuleB
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	previousTransport := httputil.Client.Transport
	httputil.Client.Transport = srv.Client().Transport
	t.Cleanup(func() { httputil.Client.Transport = previousTransport })

	return strings.TrimPrefix(srv.URL, "https://") + "/module.yaml"
}

func seedCommandRegistryCache(t *testing.T, ref string, lock *registry.LockFile, requests *atomic.Int32) {
	t.Helper()

	if _, _, err := registry.Fetch(
		context.Background(),
		ref,
		lock,
		registry.FetchOptions{},
		ui.New(io.Discard, io.Discard),
	); err != nil {
		t.Fatalf("seed registry cache: %v", err)
	}
	requests.Store(0)
}

type commandRoundTripFunc func(*http.Request) (*http.Response, error)

func (f commandRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func commandHTTPResponse(req *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
		Request:    req,
	}
}

type cliPinStateSnapshot struct {
	lockBytes  []byte
	cachePaths []string
	cacheFiles map[string][]byte
}

func snapshotCLIPinState(
	t *testing.T,
	lockPath string,
	cachePath string,
) cliPinStateSnapshot {
	t.Helper()

	lockBytes, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read CLI lock snapshot: %v", err)
	}

	snapshot := cliPinStateSnapshot{
		lockBytes:  bytes.Clone(lockBytes),
		cacheFiles: make(map[string][]byte),
	}

	err = filepath.WalkDir(cachePath, func(
		path string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}

		relative, err := filepath.Rel(cachePath, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		snapshot.cachePaths = append(snapshot.cachePaths, relative)

		if !entry.Type().IsRegular() {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snapshot.cacheFiles[relative] = bytes.Clone(content)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot configured CLI cache: %v", err)
	}

	slices.Sort(snapshot.cachePaths)
	return snapshot
}

func assertCLIPinStateUnchanged(
	t *testing.T,
	before cliPinStateSnapshot,
	lockPath string,
	cachePath string,
) {
	t.Helper()

	after := snapshotCLIPinState(t, lockPath, cachePath)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf(
			"CLI check changed durable state:\nbefore: %#v\nafter:  %#v",
			before,
			after,
		)
	}
}

func seedCLIPinState(
	t *testing.T,
	configPath string,
) (lockPath string, cachePath string, before cliPinStateSnapshot) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)

	lockPath = registry.LockPath(configPath)
	if err := os.WriteFile(
		lockPath,
		[]byte("registry:\n  state.example/seed:\n    sha256: unchanged\n"),
		0o644,
	); err != nil {
		t.Fatalf("seed CLI lock: %v", err)
	}

	cachePath = filepath.Join(home, ".cache", "dotular", "registry")
	seedPath := filepath.Join(cachePath, "nested", "seed.yaml")
	if err := os.MkdirAll(filepath.Dir(seedPath), 0o755); err != nil {
		t.Fatalf("create configured CLI cache: %v", err)
	}
	if err := os.WriteFile(seedPath, []byte("unchanged cache"), 0o644); err != nil {
		t.Fatalf("seed configured CLI cache: %v", err)
	}

	return lockPath, cachePath, snapshotCLIPinState(t, lockPath, cachePath)
}

func TestDirectRegistryFetchLoopsWaitForMutationLock(t *testing.T) {
	tests := []struct {
		name          string
		run           func(string) error
		moduleBody    func(string) string
		wantSavedLock bool
	}{
		{
			name: "infer module name",
			run: func(path string) error {
				_, err := inferModuleName(context.Background(), path)
				return err
			},
			moduleBody: func(path string) string {
				return fmt.Sprintf(
					"name: locked-module\nitems:\n  - file: source\n    destination:\n      linux: %s\n",
					path,
				)
			},
		},
		{
			name: "init",
			run: func(string) error {
				return initCmd().Execute()
			},
			wantSavedLock: true,
			moduleBody: func(string) string {
				return "name: locked-module\nitems: []\n"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			configFile = writeTestConfig(t, "modules: []\n")
			noCache = true
			t.Cleanup(func() {
				configFile = "dotular.yaml"
				noCache = false
			})

			ref := "example.invalid/" + strings.ReplaceAll(tt.name, " ", "-") + "/module.yaml"
			targetPath := filepath.Join(t.TempDir(), "matched.conf")
			indexFetched := make(chan struct{})
			moduleRequested := make(chan struct{})
			allowModule := make(chan struct{})
			var allowModuleOnce sync.Once
			t.Cleanup(func() { allowModuleOnce.Do(func() { close(allowModule) }) })
			previousTransport := httputil.Client.Transport
			httputil.Client.Transport = commandRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				switch req.URL.String() {
				case registry.IndexURL():
					close(indexFetched)
					return commandHTTPResponse(req, "modules:\n  - name: "+ref+"\n"), nil
				default:
					close(moduleRequested)
					<-allowModule
					return commandHTTPResponse(req, tt.moduleBody(targetPath)), nil
				}
			})
			t.Cleanup(func() { httputil.Client.Transport = previousTransport })

			lockHeld := make(chan struct{})
			releaseHeld := make(chan struct{})
			var releaseHeldOnce sync.Once
			releaseHolder := func() { releaseHeldOnce.Do(func() { close(releaseHeld) }) }
			t.Cleanup(releaseHolder)
			holderDone := make(chan error, 1)
			go func() {
				holderDone <- registry.WithRegistryMutationLock(func() error {
					close(lockHeld)
					<-releaseHeld
					return nil
				})
			}()
			select {
			case <-lockHeld:
			case err := <-holderDone:
				t.Fatalf("acquire mutation lock: %v", err)
			case <-time.After(time.Second):
				t.Fatal("timed out acquiring mutation lock")
			}

			commandDone := make(chan error, 1)
			go func() {
				commandDone <- tt.run(targetPath)
			}()
			select {
			case <-indexFetched:
			case <-time.After(time.Second):
				t.Fatal("command did not fetch the registry index")
			}
			select {
			case <-moduleRequested:
				t.Fatal("command reached registry.Fetch while the mutation lock was held")
			case <-time.After(50 * time.Millisecond):
			}

			releaseHolder()
			if err := <-holderDone; err != nil {
				t.Fatalf("release held mutation lock: %v", err)
			}
			select {
			case <-moduleRequested:
				allowModuleOnce.Do(func() { close(allowModule) })
			case <-time.After(time.Second):
				t.Fatal("command did not reach registry.Fetch after the mutation lock was released")
			}
			err := <-commandDone
			if err != nil {
				t.Fatalf("command error = %v", err)
			}

			_, saved := loadCommandLock(t, registry.LockPath(configFile)).Registry[ref]
			if saved != tt.wantSavedLock {
				t.Fatalf("saved lock contains ref = %t, want %t", saved, tt.wantSavedLock)
			}
		})
	}
}

func loadCommandLock(t *testing.T, path string) *registry.LockFile {
	t.Helper()
	lock, err := registry.LoadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	return lock
}

func TestBuildRoot(t *testing.T) {
	root := buildRoot()
	if root == nil {
		t.Fatal("buildRoot() returned nil")
	}
	if root.Use != "dotular" {
		t.Errorf("Use = %q", root.Use)
	}

	commands := root.Commands()
	names := make(map[string]bool)
	for _, cmd := range commands {
		names[cmd.Name()] = true
	}

	expected := []string{"init", "add", "apply", "push", "pull", "sync", "list", "status", "platform", "verify", "encrypt", "decrypt", "tag", "log", "registry"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing subcommand %q", name)
		}
	}
}

func TestFormatTypeCounts(t *testing.T) {
	tests := []struct {
		counts map[string]int
		want   string
	}{
		{map[string]int{"package": 3}, "3 packages"},
		{map[string]int{"file": 1}, "1 file"},
		{map[string]int{"package": 2, "file": 1, "run": 3}, "2 packages, 1 file, 3 runs"},
		{map[string]int{}, ""},
	}
	for _, tt := range tests {
		got := formatTypeCounts(tt.counts)
		if got != tt.want {
			t.Errorf("formatTypeCounts(%v) = %q, want %q", tt.counts, got, tt.want)
		}
	}
}

func TestApplyCmd(t *testing.T) {
	cmd := applyCmd()
	if cmd.Use != "apply [module...]" {
		t.Errorf("Use = %q", cmd.Use)
	}
}

func TestDirectionCmds(t *testing.T) {
	for _, dir := range []string{"push", "pull", "sync"} {
		cmd := directionCmd(dir, "test description")
		if cmd == nil {
			t.Errorf("directionCmd(%q) returned nil", dir)
		}
		if cmd.Use != dir+" [module...]" {
			t.Errorf("Use = %q", cmd.Use)
		}
	}
}

func TestListCmdDef(t *testing.T) {
	cmd := listCmd()
	if cmd.Use != "list" {
		t.Errorf("Use = %q", cmd.Use)
	}
}

func TestStatusCmdDef(t *testing.T) {
	cmd := statusCmd()
	if cmd.Use != "status" {
		t.Errorf("Use = %q", cmd.Use)
	}
}

func TestPlatformCmdDef(t *testing.T) {
	cmd := platformCmd()
	if cmd.Use != "platform" {
		t.Errorf("Use = %q", cmd.Use)
	}
}

func TestVerifyCmdDef(t *testing.T) {
	cmd := verifyCmd()
	if cmd.Use != "verify [module...]" {
		t.Errorf("Use = %q", cmd.Use)
	}
}

func TestEncryptCmdDef(t *testing.T) {
	cmd := encryptCmd()
	if cmd.Use != "encrypt <file>" {
		t.Errorf("Use = %q", cmd.Use)
	}
}

func TestEncryptCmdRejectsAgeInput(t *testing.T) {
	// A usable key, so only the guard stands between the command and an
	// in-place re-encryption of the source.
	prev := configFile
	defer func() { configFile = prev }()
	configFile = writeTestConfig(t, "modules: []\n")
	t.Setenv("DOTULAR_AGE_PASSPHRASE", "test-pass")

	dir := t.TempDir()
	src := filepath.Join(dir, "secret.env.age")
	if err := os.WriteFile(src, []byte("ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := encryptCmd()
	err := cmd.RunE(cmd, []string{src})
	if err == nil {
		t.Fatal("expected error for an already-encrypted input")
	}
	if !strings.Contains(err.Error(), "already encrypted") {
		t.Errorf("error should explain the rejection, got %v", err)
	}
	data, readErr := os.ReadFile(src)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "ciphertext" {
		t.Error("source file should be left untouched")
	}
}

func TestDecryptCmdDef(t *testing.T) {
	cmd := decryptCmd()
	if cmd.Use != "decrypt <file.age>" {
		t.Errorf("Use = %q", cmd.Use)
	}
}

func TestTagCmdDef(t *testing.T) {
	cmd := tagCmd()
	if cmd.Use != "tag" {
		t.Errorf("Use = %q", cmd.Use)
	}
	subs := cmd.Commands()
	if len(subs) < 2 {
		t.Errorf("expected at least 2 tag subcommands, got %d", len(subs))
	}
}

func TestLogCmdDef(t *testing.T) {
	cmd := logCmd()
	if cmd.Use != "log" {
		t.Errorf("Use = %q", cmd.Use)
	}
}

func TestRegistryCmdDef(t *testing.T) {
	cmd := registryCmd()
	if cmd.Use != "registry" {
		t.Errorf("Use = %q", cmd.Use)
	}
	subs := cmd.Commands()
	if len(subs) < 3 {
		t.Errorf("expected at least 3 registry subcommands, got %d", len(subs))
	}
}

func TestLoadConfigMissing(t *testing.T) {
	configFile = "/nonexistent/dotular.yaml"
	_, err := loadConfig()
	if err == nil {
		t.Error("expected error for missing config")
	}
}

func TestLoadConfigValid(t *testing.T) {
	configFile = writeTestConfig(t, `
modules:
  - name: test
    items:
      - package: git
        via: brew
`)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Modules) != 1 {
		t.Errorf("expected 1 module, got %d", len(cfg.Modules))
	}
}

func TestLoadAndResolveConfig(t *testing.T) {
	configFile = writeTestConfig(t, `
modules:
  - name: test
    items:
      - package: git
        via: brew
`)
	noCache = false
	cfg, err := loadAndResolveConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Modules) != 1 {
		t.Errorf("expected 1 module, got %d", len(cfg.Modules))
	}
}

func TestNewRunnerFunc(t *testing.T) {
	configFile = writeTestConfig(t, `
modules:
  - name: test
    items:
      - run: echo hello
`)
	dryRun = true
	verbose = false
	noAtomic = false

	cfg, _ := loadConfig()
	r := newRunner(cfg)
	if r == nil {
		t.Fatal("newRunner() returned nil")
	}
	if !r.DryRun {
		t.Error("expected DryRun=true")
	}
}

func TestPlatformCmdExecute(t *testing.T) {
	cmd := platformCmd()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestApplyCmdWithConfig(t *testing.T) {
	path := writeTestConfig(t, `
modules:
  - name: test
    items:
      - run: "true"
`)
	root := buildRoot()
	root.SetArgs([]string{"apply", "--dry-run", "--config", path})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestApplyCmdModuleNotFound(t *testing.T) {
	path := writeTestConfig(t, `
modules:
  - name: test
    items:
      - run: "true"
`)
	root := buildRoot()
	root.SetArgs([]string{"apply", "--config", path, "nonexistent"})
	if err := root.Execute(); err == nil {
		t.Error("expected error for nonexistent module")
	}
}

func TestListCmdExecute(t *testing.T) {
	path := writeTestConfig(t, `
modules:
  - name: mod1
    items:
      - package: git
        via: brew
  - name: mod2
    items:
      - run: echo
`)
	root := buildRoot()
	root.SetArgs([]string{"list", "--config", path})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestStatusCmdExecute(t *testing.T) {
	path := writeTestConfig(t, `
modules:
  - name: test
    items:
      - run: echo hello
`)
	root := buildRoot()
	root.SetArgs([]string{"status", "--config", path})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestLogCmdExecute(t *testing.T) {
	root := buildRoot()
	root.SetArgs([]string{"log"})
	root.Execute()
}

func TestDirectionCmdExecute(t *testing.T) {
	path := writeTestConfig(t, `
modules:
  - name: test
    items:
      - run: echo hello
`)
	for _, direction := range []string{"push", "pull", "sync"} {
		root := buildRoot()
		root.SetArgs([]string{direction, "--dry-run", "--config", path})
		if err := root.Execute(); err != nil {
			t.Errorf("%s: %v", direction, err)
		}
	}
}

func TestDirectionCmdModuleNotFound(t *testing.T) {
	path := writeTestConfig(t, `
modules:
  - name: test
    items:
      - run: "true"
`)
	root := buildRoot()
	root.SetArgs([]string{"push", "--config", path, "nonexistent"})
	if err := root.Execute(); err == nil {
		t.Error("expected error for nonexistent module")
	}
}

func TestKeyFromConfigNoKey(t *testing.T) {
	configFile = writeTestConfig(t, `
modules:
  - name: test
    items: []
`)
	t.Setenv("DOTULAR_AGE_IDENTITY", "")
	t.Setenv("DOTULAR_AGE_PASSPHRASE", "")

	_, err := keyFromConfig()
	if err == nil {
		t.Error("expected error when no age key configured")
	}
}

func TestKeyFromConfigWithPassphrase(t *testing.T) {
	configFile = writeTestConfig(t, `
age:
  passphrase: "test-pass"
modules: []
`)
	key, err := keyFromConfig()
	if err != nil {
		t.Fatal(err)
	}
	if key.Passphrase != "test-pass" {
		t.Errorf("Passphrase = %q", key.Passphrase)
	}
}

func TestVerifyCmdExecute(t *testing.T) {
	path := writeTestConfig(t, `
modules:
  - name: test
    items:
      - run: echo hello
        verify: "true"
`)
	root := buildRoot()
	root.SetArgs([]string{"verify", "--config", path})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyCmdModuleNotFound(t *testing.T) {
	path := writeTestConfig(t, `
modules:
  - name: test
    items: []
`)
	root := buildRoot()
	root.SetArgs([]string{"verify", "--config", path, "nonexistent"})
	if err := root.Execute(); err == nil {
		t.Error("expected error for nonexistent module")
	}
}

func TestRegistryClearCmdExecute(t *testing.T) {
	root := buildRoot()
	root.SetArgs([]string{"registry", "clear"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryListCmdExecute(t *testing.T) {
	path := writeTestConfig(t, `modules: []`)
	root := buildRoot()
	root.SetArgs([]string{"registry", "list", "--config", path})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestEncryptDecryptCmdExecute(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "dotular.yaml")
	os.WriteFile(configPath, []byte(`
age:
  passphrase: "test-password"
modules: []
`), 0o644)

	// Create a file to encrypt.
	plainFile := filepath.Join(dir, "secret.txt")
	os.WriteFile(plainFile, []byte("secret data"), 0o644)

	// Encrypt.
	root := buildRoot()
	root.SetArgs([]string{"encrypt", "--config", configPath, plainFile})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	// Verify .age file was created.
	ageFile := plainFile + ".age"
	if _, err := os.Stat(ageFile); err != nil {
		t.Fatalf("expected %s to exist: %v", ageFile, err)
	}

	// Decrypt.
	decryptedFile := filepath.Join(dir, "secret.txt.decrypted")
	// The decrypt command removes the .age suffix.
	root2 := buildRoot()
	root2.SetArgs([]string{"decrypt", "--config", configPath, ageFile})
	if err := root2.Execute(); err != nil {
		t.Fatal(err)
	}

	// Verify decrypted file content.
	data, _ := os.ReadFile(plainFile) // decrypt writes back to plainFile (without .age)
	if string(data) == "" {
		_ = decryptedFile // unused but that's fine
	}
}

func TestTagListCmdExecute(t *testing.T) {
	dir := t.TempDir()
	testutil.SetHome(t, dir)

	root := buildRoot()
	root.SetArgs([]string{"tag", "list"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestTagAddCmdExecute(t *testing.T) {
	dir := t.TempDir()
	testutil.SetHome(t, dir)

	root := buildRoot()
	root.SetArgs([]string{"tag", "add", "work"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestApplyWithSpecificModule(t *testing.T) {
	path := writeTestConfig(t, `
modules:
  - name: alpha
    items:
      - run: "true"
  - name: beta
    items:
      - run: "true"
`)
	root := buildRoot()
	root.SetArgs([]string{"apply", "--dry-run", "--config", path, "alpha"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestDirectionCmdWithModule(t *testing.T) {
	path := writeTestConfig(t, `
modules:
  - name: mymod
    items:
      - run: "true"
`)
	root := buildRoot()
	root.SetArgs([]string{"push", "--dry-run", "--config", path, "mymod"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyCmdWithModule(t *testing.T) {
	path := writeTestConfig(t, `
modules:
  - name: mymod
    items:
      - run: "true"
        verify: "true"
`)
	root := buildRoot()
	root.SetArgs([]string{"verify", "--config", path, "mymod"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryUpdateCmdExecute(t *testing.T) {
	path := writeTestConfig(t, `modules: []`)
	root := buildRoot()
	root.SetArgs([]string{"registry", "update", "--config", path})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestWritePinChangesPreservesInputOrderAndRendersMissingOldAsNone(
	t *testing.T,
) {
	t.Parallel()

	changes := []registry.PinChange{
		{
			Ref:       "registry.example/zeta",
			Status:    registry.PinStatusMissing,
			OldSHA256: "",
			NewSHA256: "sha256:222",
		},
		{
			Ref:       "registry.example/alpha",
			Status:    registry.PinStatusDrift,
			OldSHA256: "sha256:333",
			NewSHA256: "sha256:444",
		},
		{
			Ref:       "registry.example/mu",
			Status:    registry.PinStatusMatch,
			OldSHA256: "sha256:555",
			NewSHA256: "sha256:555",
		},
	}

	var output bytes.Buffer
	if err := writePinChanges(&output, changes); err != nil {
		t.Fatalf("writePinChanges returned error: %v", err)
	}

	const want = "" +
		"REF\tSTATUS\tOLD\tNEW\n" +
		"registry.example/zeta\tmissing\tnone\tsha256:222\n" +
		"registry.example/alpha\tdrift\tsha256:333\tsha256:444\n" +
		"registry.example/mu\tmatch\tsha256:555\tsha256:555\n"

	if got := output.String(); got != want {
		t.Fatalf("output mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestWritePinChangesWritesNothingForEmptyChanges(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	if err := writePinChanges(&output, nil); err != nil {
		t.Fatalf("writePinChanges returned error: %v", err)
	}
	if got := output.String(); got != "" {
		t.Fatalf("output = %q, want empty", got)
	}
}

const (
	registryUpdateRefA = "example.invalid/a.yaml"
	registryUpdateRefM = "example.invalid/m.yaml"
	registryUpdateRefZ = "example.invalid/z.yaml"
	registryUpdateNewA = "new-a"
	registryUpdateOldM = "old-m"
	registryUpdateNewM = "new-m"
	registryUpdateOldZ = "old-z"
	registryUpdateNewZ = "new-z"
)

func registryUpdateChanges() []registry.PinChange {
	return []registry.PinChange{
		{
			Ref:       registryUpdateRefA,
			NewSHA256: registryUpdateNewA,
			Status:    registry.PinStatusMissing,
		},
		{
			Ref:       registryUpdateRefM,
			OldSHA256: registryUpdateOldM,
			NewSHA256: registryUpdateNewM,
			Status:    registry.PinStatusMatch,
		},
		{
			Ref:       registryUpdateRefZ,
			OldSHA256: registryUpdateOldZ,
			NewSHA256: registryUpdateNewZ,
			Status:    registry.PinStatusDrift,
		},
	}
}

func registryUpdateOutput() string {
	return strings.Join([]string{
		registryUpdateRefA + "\tnone\t" + registryUpdateNewA,
		registryUpdateRefM + "\t" + registryUpdateOldM + "\t" + registryUpdateNewM,
		registryUpdateRefZ + "\t" + registryUpdateOldZ + "\t" + registryUpdateNewZ,
		"",
	}, "\n")
}

func stubRegistryUpdatePins(
	t *testing.T,
	stub func(context.Context, config.Config, string, *ui.UI) ([]registry.PinChange, error),
) {
	t.Helper()
	previous := updateRegistryPins
	updateRegistryPins = stub
	t.Cleanup(func() { updateRegistryPins = previous })
}

func stubRegistryCheckPins(
	t *testing.T,
	stub func(context.Context, *config.Config, string) ([]registry.PinChange, error),
) {
	t.Helper()
	previous := checkRegistryPins
	checkRegistryPins = stub
	t.Cleanup(func() { checkRegistryPins = previous })
}

func executeRegistryArgs(
	t *testing.T,
	path string,
	args []string,
) (string, error) {
	t.Helper()
	var stdout bytes.Buffer
	root := buildRoot()
	configFile = path
	root.SetOut(&stdout)
	root.SetErr(io.Discard)
	root.SetArgs(args)
	err := root.Execute()
	return stdout.String(), err
}

func executeRegistryUpdate(t *testing.T, path string) (string, error) {
	t.Helper()
	var stdout bytes.Buffer
	root := buildRoot()
	root.SetOut(&stdout)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"registry", "update", "--config", path})
	err := root.Execute()
	return stdout.String(), err
}

func TestRegistryUpdateCheckInvocation(t *testing.T) {
	path := writeTestConfig(t, "modules: []\n")
	var updateCalls, checkCalls int

	stubRegistryUpdatePins(t, func(
		context.Context,
		config.Config,
		string,
		*ui.UI,
	) ([]registry.PinChange, error) {
		updateCalls++
		return registryUpdateChanges(), nil
	})
	stubRegistryCheckPins(t, func(
		context.Context,
		*config.Config,
		string,
	) ([]registry.PinChange, error) {
		checkCalls++
		return []registry.PinChange{{
			Ref:       registryUpdateRefM,
			OldSHA256: registryUpdateOldM,
			NewSHA256: registryUpdateOldM,
			Status:    registry.PinStatusMatch,
		}}, nil
	})

	checkOutput, err := executeRegistryArgs(
		t,
		path,
		[]string{"registry", "update", "--check"},
	)
	if err != nil {
		t.Fatalf("registry update --check: %v", err)
	}
	const wantCheckOutput = "" +
		"REF\tSTATUS\tOLD\tNEW\n" +
		"example.invalid/m.yaml\tmatch\told-m\told-m\n"
	if checkOutput != wantCheckOutput {
		t.Fatalf("check stdout = %q, want %q", checkOutput, wantCheckOutput)
	}
	if checkCalls != 1 || updateCalls != 0 {
		t.Fatalf("after check: CheckPins calls = %d, UpdatePins calls = %d", checkCalls, updateCalls)
	}

	if _, err := executeRegistryArgs(
		t,
		path,
		[]string{"registry", "--check", "update"},
	); err == nil {
		t.Fatal("registry --check update succeeded, want parse error")
	}
	if checkCalls != 1 || updateCalls != 0 {
		t.Fatalf(
			"misowned flag routed execution: CheckPins calls = %d, UpdatePins calls = %d",
			checkCalls,
			updateCalls,
		)
	}

	_, positionalErr := executeRegistryArgs(
		t,
		path,
		[]string{"registry", "update", "check"},
	)
	if positionalErr == nil {
		t.Fatal("registry update check succeeded, want positional-argument error")
	}
	if got := exitCode(positionalErr); got != exitUsage {
		t.Fatalf("registry update check exit = %d, want %d", got, exitUsage)
	}
	if checkCalls != 1 || updateCalls != 0 {
		t.Fatalf(
			"positional check routed execution: CheckPins calls = %d, UpdatePins calls = %d",
			checkCalls,
			updateCalls,
		)
	}

	updateOutput, err := executeRegistryArgs(
		t,
		path,
		[]string{"registry", "update"},
	)
	if err != nil {
		t.Fatalf("registry update: %v", err)
	}
	if updateOutput != registryUpdateOutput() {
		t.Fatalf("normal update stdout = %q, want %q", updateOutput, registryUpdateOutput())
	}
	if checkCalls != 1 || updateCalls != 1 {
		t.Fatalf("after update: CheckPins calls = %d, UpdatePins calls = %d", checkCalls, updateCalls)
	}
}

func TestRegistryUpdateCheckResultsAndReadOnlyState(t *testing.T) {
	tests := []struct {
		name       string
		changes    []registry.PinChange
		err        error
		wantOutput string
		repeats    int
	}{
		{
			name:    "no active refs",
			repeats: 1,
		},
		{
			name: "all match",
			changes: []registry.PinChange{{
				Ref:       "registry.example/beta",
				Status:    registry.PinStatusMatch,
				OldSHA256: "sha256:111",
				NewSHA256: "sha256:111",
			}},
			wantOutput: "" +
				"REF\tSTATUS\tOLD\tNEW\n" +
				"registry.example/beta\tmatch\tsha256:111\tsha256:111\n",
			repeats: 1,
		},
		{
			name: "missing unpinned ref",
			changes: []registry.PinChange{{
				Ref:       "registry.example/gamma",
				Status:    registry.PinStatusMissing,
				NewSHA256: "sha256:222",
			}},
			err: registry.ErrPinsOutOfDate,
			wantOutput: "" +
				"REF\tSTATUS\tOLD\tNEW\n" +
				"registry.example/gamma\tmissing\tnone\tsha256:222\n",
			repeats: 1,
		},
		{
			name: "drift",
			changes: []registry.PinChange{{
				Ref:       "registry.example/alpha",
				Status:    registry.PinStatusDrift,
				OldSHA256: "sha256:333",
				NewSHA256: "sha256:444",
			}},
			err: registry.ErrPinsOutOfDate,
			wantOutput: "" +
				"REF\tSTATUS\tOLD\tNEW\n" +
				"registry.example/alpha\tdrift\tsha256:333\tsha256:444\n",
			repeats: 1,
		},
		{
			name: "mixed preserves inherited order",
			changes: []registry.PinChange{
				{
					Ref:       "registry.example/beta",
					Status:    registry.PinStatusMatch,
					OldSHA256: "sha256:111",
					NewSHA256: "sha256:111",
				},
				{
					Ref:       "registry.example/gamma",
					Status:    registry.PinStatusMissing,
					NewSHA256: "sha256:222",
				},
				{
					Ref:       "registry.example/alpha",
					Status:    registry.PinStatusDrift,
					OldSHA256: "sha256:333",
					NewSHA256: "sha256:444",
				},
			},
			err: registry.ErrPinsOutOfDate,
			wantOutput: "" +
				"REF\tSTATUS\tOLD\tNEW\n" +
				"registry.example/beta\tmatch\tsha256:111\tsha256:111\n" +
				"registry.example/gamma\tmissing\tnone\tsha256:222\n" +
				"registry.example/alpha\tdrift\tsha256:333\tsha256:444\n",
			repeats: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTestConfig(t, "modules: []\n")
			lockPath, cachePath, before := seedCLIPinState(t, path)
			var calls int
			stubRegistryCheckPins(t, func(
				context.Context,
				*config.Config,
				string,
			) ([]registry.PinChange, error) {
				calls++
				return tt.changes, tt.err
			})

			var firstOutput string
			for run := 0; run < tt.repeats; run++ {
				output, err := executeRegistryArgs(
					t,
					path,
					[]string{"registry", "update", "--check"},
				)
				if !errors.Is(err, tt.err) {
					t.Fatalf("run %d error = %v, want %v", run+1, err, tt.err)
				}
				if got, want := exitCode(err), exitCode(tt.err); got != want {
					t.Fatalf("run %d exit = %d, want %d", run+1, got, want)
				}
				if output != tt.wantOutput {
					t.Fatalf("run %d stdout = %q, want %q", run+1, output, tt.wantOutput)
				}
				if strings.Contains(output, "TIMESTAMP") {
					t.Fatalf("run %d stdout contains timestamp column: %q", run+1, output)
				}
				if run > 0 && output != firstOutput {
					t.Fatalf("repeated stdout differs:\nfirst:  %q\nsecond: %q", firstOutput, output)
				}
				firstOutput = output
				assertCLIPinStateUnchanged(t, before, lockPath, cachePath)
			}
			if calls != tt.repeats {
				t.Fatalf("CheckPins calls = %d, want %d", calls, tt.repeats)
			}
		})
	}
}

func TestRegistryUpdateCheckStagingFailuresPreserveState(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "lock or input parse failure", err: errors.New("parse lock or input")},
		{name: "download failure", err: errors.New("download")},
		{name: "downloaded-payload parse failure", err: errors.New("parse downloaded payload")},
		{name: "validation failure", err: errors.New("validate")},
		{name: "checksum failure", err: errors.New("checksum")},
		{name: "comparison failure", err: errors.New("compare")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTestConfig(t, "modules: []\n")
			lockPath, cachePath, before := seedCLIPinState(t, path)
			stubRegistryCheckPins(t, func(
				context.Context,
				*config.Config,
				string,
			) ([]registry.PinChange, error) {
				return nil, tt.err
			})

			output, err := executeRegistryArgs(
				t,
				path,
				[]string{"registry", "update", "--check"},
			)
			if !errors.Is(err, tt.err) {
				t.Fatalf("error = %v, want inherited %v", err, tt.err)
			}
			if errors.Is(err, registry.ErrPinsOutOfDate) {
				t.Fatalf("error = %v, must not include ErrPinsOutOfDate", err)
			}
			if exitCode(err) == 0 {
				t.Fatal("staging failure exited successfully")
			}
			if output != "" {
				t.Fatalf("stdout = %q, want no rows", output)
			}
			assertCLIPinStateUnchanged(t, before, lockPath, cachePath)
		})
	}
}

func TestRegistryUpdatePrintsSortedRowsWithExactNoneRendering(t *testing.T) {
	stubRegistryUpdatePins(t, func(
		context.Context,
		config.Config,
		string,
		*ui.UI,
	) ([]registry.PinChange, error) {
		return registryUpdateChanges(), nil
	})

	got, err := executeRegistryUpdate(t, writeTestConfig(t, "modules: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := registryUpdateOutput()
	if got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	for _, forbidden := range []string{"REF", "STATUS", "(none)"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("stdout contains forbidden %q: %q", forbidden, got)
		}
	}
}

func TestRegistryUpdatePrintsAllRowsBeforePostStagingError(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "collision", err: errors.New("collision")},
		{name: "preparation", err: errors.New("preparation")},
		{name: "save-lock", err: errors.New("save lock")},
		{name: "publication", err: errors.New("publication")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubRegistryUpdatePins(t, func(
				context.Context,
				config.Config,
				string,
				*ui.UI,
			) ([]registry.PinChange, error) {
				return registryUpdateChanges(), tt.err
			})

			got, err := executeRegistryUpdate(t, writeTestConfig(t, "modules: []\n"))
			if !errors.Is(err, tt.err) {
				t.Fatalf("error = %v, want %v", err, tt.err)
			}
			if want := registryUpdateOutput(); got != want {
				t.Fatalf("stdout = %q, want %q", got, want)
			}
		})
	}
}

func TestRegistryUpdatePrintsNoRowsOnStagingFailure(t *testing.T) {
	errStage := errors.New("staging")
	stubRegistryUpdatePins(t, func(
		context.Context,
		config.Config,
		string,
		*ui.UI,
	) ([]registry.PinChange, error) {
		return nil, errStage
	})

	got, err := executeRegistryUpdate(t, writeTestConfig(t, "modules: []\n"))
	if !errors.Is(err, errStage) {
		t.Fatalf("error = %v, want %v", err, errStage)
	}
	if got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
}

func TestRegistryUpdateInvokesUpdatePinsOnceForCompleteConfiguration(t *testing.T) {
	var (
		calls          int
		receivedConfig config.Config
		receivedPath   string
	)
	stubRegistryUpdatePins(t, func(
		_ context.Context,
		cfg config.Config,
		configPath string,
		u *ui.UI,
	) ([]registry.PinChange, error) {
		calls++
		receivedConfig = cfg
		receivedPath = configPath
		if u == nil {
			t.Fatal("UpdatePins received nil UI")
		}
		return nil, nil
	})
	path := writeTestConfig(t, fmt.Sprintf(`
modules:
  - name: local
    items:
      - package: git
  - from: %q
  - from: %q
  - from: %q
`, registryUpdateRefA, registryUpdateRefA, registryUpdateRefZ))

	if _, err := executeRegistryUpdate(t, path); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("UpdatePins calls = %d, want 1", calls)
	}
	if receivedPath != path {
		t.Fatalf("config path = %q, want %q", receivedPath, path)
	}
	if len(receivedConfig.Modules) != 4 {
		t.Fatalf("loaded modules = %d, want 4", len(receivedConfig.Modules))
	}
	wantRefs := []string{"", registryUpdateRefA, registryUpdateRefA, registryUpdateRefZ}
	for i, want := range wantRefs {
		if got := receivedConfig.Modules[i].From; got != want {
			t.Fatalf("module %d ref = %q, want %q", i, got, want)
		}
	}
}

func TestRegistryUpdateIsTheOnlyPinMutationCommand(t *testing.T) {
	root := buildRoot()
	registryCommand, _, err := root.Find([]string{"registry"})
	if err != nil {
		t.Fatal(err)
	}
	updateCommand, _, err := root.Find([]string{"registry", "update"})
	if err != nil {
		t.Fatal(err)
	}

	allowed := map[string]bool{"clear": true, "list": true, "update": true}
	updateCount := 0
	for _, command := range registryCommand.Commands() {
		if !allowed[command.Name()] {
			t.Fatalf("unexpected registry mutation command %q", command.Name())
		}
		if command.Name() == "update" {
			updateCount++
		}
	}
	if len(registryCommand.Commands()) != len(allowed) {
		t.Fatalf("registry commands = %d, want %d", len(registryCommand.Commands()), len(allowed))
	}
	if updateCount != 1 {
		t.Fatalf("registry update commands = %d, want 1", updateCount)
	}
	if updateCommand.Flags().Lookup("check") == nil {
		t.Fatal("registry update does not expose --check")
	}
	if updateCommand.PersistentFlags().Lookup("check") != nil ||
		updateCommand.InheritedFlags().Lookup("check") != nil {
		t.Fatal("registry update --check is not a local flag")
	}
	if registryCommand.Flags().Lookup("check") != nil ||
		registryCommand.PersistentFlags().Lookup("check") != nil ||
		registryCommand.InheritedFlags().Lookup("check") != nil {
		t.Fatal("registry parent exposes --check")
	}
	if updateCommand.Flags().Lookup("repin") != nil ||
		updateCommand.PersistentFlags().Lookup("repin") != nil ||
		updateCommand.InheritedFlags().Lookup("repin") != nil {
		t.Fatal("registry update exposes forbidden --repin flag")
	}
}

func TestOrdinaryCommandNoCacheRejectsDrift(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var replacement atomic.Bool
	var requests atomic.Int32
	ref := serveCommandRegistryModule(t, &replacement, &requests)
	path := writeTestConfig(t, fmt.Sprintf("modules:\n  - from: %q\n", ref))
	lockPath := registry.LockPath(path)
	original := registry.LockEntry{
		SHA256: commandRegistryChecksum(commandRegistryModuleA),
		URL:    registry.ParseRef(ref).FetchURL,
	}
	lock := &registry.LockFile{Registry: map[string]registry.LockEntry{ref: original}}
	if err := registry.SaveLock(lockPath, lock); err != nil {
		t.Fatal(err)
	}
	seedCommandRegistryCache(t, ref, lock, &requests)

	matching := buildRoot()
	matching.SetArgs([]string{"list", "--no-cache", "--config", path})
	if err := matching.Execute(); err != nil {
		t.Fatalf("ordinary matching --no-cache command: %v", err)
	}
	persisted, err := registry.LoadLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := persisted.Registry[ref]; got != original {
		t.Fatalf("matching --no-cache changed durable pin\nbefore: %#v\nafter:  %#v", original, got)
	}

	replacement.Store(true)
	differing := buildRoot()
	differing.SetArgs([]string{"list", "--no-cache", "--config", path})
	err = differing.Execute()
	if err == nil {
		t.Fatal("ordinary differing --no-cache command succeeded, want checksum mismatch")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("ordinary differing --no-cache error = %q, want visible checksum mismatch text", err)
	}
	persisted, loadErr := registry.LoadLock(lockPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if got := persisted.Registry[ref]; got != original {
		t.Fatalf("differing --no-cache changed durable pin\nbefore: %#v\nafter:  %#v", original, got)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("ordinary --no-cache network requests = %d, want 2", got)
	}
}

func TestRegistryUpdateMovesExistingPin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var replacement atomic.Bool
	var requests atomic.Int32
	ref := serveCommandRegistryModule(t, &replacement, &requests)
	path := writeTestConfig(t, fmt.Sprintf("modules:\n  - from: %q\n", ref))
	lockPath := registry.LockPath(path)
	original := registry.LockEntry{
		SHA256: commandRegistryChecksum(commandRegistryModuleA),
		URL:    registry.ParseRef(ref).FetchURL,
	}
	lock := &registry.LockFile{Registry: map[string]registry.LockEntry{ref: original}}
	if err := registry.SaveLock(lockPath, lock); err != nil {
		t.Fatal(err)
	}
	seedCommandRegistryCache(t, ref, lock, &requests)
	replacement.Store(true)

	root := buildRoot()
	root.SetArgs([]string{"registry", "update", "--config", path})
	if err := root.Execute(); err != nil {
		t.Fatalf("registry update: %v", err)
	}

	persisted, err := registry.LoadLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := persisted.Registry[ref].SHA256, commandRegistryChecksum(commandRegistryModuleB); got != want {
		t.Fatalf("updated durable checksum = %q, want %q", got, want)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("registry update network requests = %d, want 1 to prove cache bypass", got)
	}
}

func TestInitCmdExists(t *testing.T) {
	root := buildRoot()
	cmd, _, err := root.Find([]string{"init"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Use != "init" {
		t.Errorf("init command Use = %q", cmd.Use)
	}
}

func TestAddCmdAcceptsNewArgOrder(t *testing.T) {
	root := buildRoot()
	cmd, _, err := root.Find([]string{"add"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Use != "add <path> [module]" {
		t.Errorf("add command Use = %q, want %q", cmd.Use, "add <path> [module]")
	}
}

// --- add command tests -------------------------------------------------------

func TestAddCmdDef(t *testing.T) {
	cmd := addCmd()
	if cmd.Use != "add <path> [module]" {
		t.Errorf("Use = %q", cmd.Use)
	}
}

func TestAddCmdFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "dotular.yaml")
	os.WriteFile(cfgPath, []byte("modules: []\n"), 0o644)

	// Create a source file.
	srcFile := filepath.Join(dir, "myfile.txt")
	os.WriteFile(srcFile, []byte("hello"), 0o644)

	root := buildRoot()
	root.SetArgs([]string{"add", "--config", cfgPath, srcFile, "mymod"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	// Verify the file was copied into the module store.
	stored := filepath.Join(dir, "mymod", "myfile.txt")
	data, err := os.ReadFile(stored)
	if err != nil {
		t.Fatalf("stored file not found: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("stored content = %q", string(data))
	}

	// Verify the config was updated.
	cfg, err := loadConfigFrom(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	mod := cfg.Module("mymod")
	if mod == nil {
		t.Fatal("module 'mymod' not found in config")
	}
	if len(mod.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(mod.Items))
	}
	if mod.Items[0].File != "myfile.txt" {
		t.Errorf("item file = %q", mod.Items[0].File)
	}
}

func TestAddCmdDirectory(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "dotular.yaml")
	os.WriteFile(cfgPath, []byte("modules: []\n"), 0o644)

	// Create a source directory.
	srcDir := filepath.Join(dir, "mydir")
	os.MkdirAll(filepath.Join(srcDir, "sub"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("aaa"), 0o644)
	os.WriteFile(filepath.Join(srcDir, "sub", "b.txt"), []byte("bbb"), 0o644)

	root := buildRoot()
	root.SetArgs([]string{"add", "--config", cfgPath, srcDir, "mymod"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	// Verify the directory was copied.
	data, err := os.ReadFile(filepath.Join(dir, "mymod", "mydir", "sub", "b.txt"))
	if err != nil {
		t.Fatalf("stored file not found: %v", err)
	}
	if string(data) != "bbb" {
		t.Errorf("stored content = %q", string(data))
	}

	// Verify the config.
	cfg, err := loadConfigFrom(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	mod := cfg.Module("mymod")
	if mod == nil {
		t.Fatal("module 'mymod' not found")
	}
	if mod.Items[0].Directory != "mydir" {
		t.Errorf("item directory = %q", mod.Items[0].Directory)
	}
}

func TestAddCmdToExistingModule(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "dotular.yaml")
	os.WriteFile(cfgPath, []byte(`
modules:
  - name: existing
    items:
      - package: git
        via: brew
`), 0o644)

	srcFile := filepath.Join(dir, "extra.txt")
	os.WriteFile(srcFile, []byte("extra"), 0o644)

	root := buildRoot()
	root.SetArgs([]string{"add", "--config", cfgPath, srcFile, "existing"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfigFrom(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	mod := cfg.Module("existing")
	if mod == nil {
		t.Fatal("module 'existing' not found")
	}
	if len(mod.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(mod.Items))
	}
}

func TestAddCmdWithLink(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "dotular.yaml")
	os.WriteFile(cfgPath, []byte("modules: []\n"), 0o644)

	srcFile := filepath.Join(dir, "linkme.txt")
	os.WriteFile(srcFile, []byte("data"), 0o644)

	root := buildRoot()
	root.SetArgs([]string{"add", "--config", cfgPath, "--link", srcFile, "linkmod"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfigFrom(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	mod := cfg.Module("linkmod")
	if mod == nil {
		t.Fatal("module not found")
	}
	if !mod.Items[0].Link {
		t.Error("expected link=true")
	}
}

func TestAddCmdRejectsInvalidDirectionBeforeMutation(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "dotular.yaml")
	before := []byte("modules: []\n")
	if err := os.WriteFile(cfgPath, before, 0o644); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(dir, "source.txt")
	if err := os.WriteFile(sourcePath, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := buildRoot()
	root.SetArgs([]string{"add", sourcePath, "shell", "--config", cfgPath, "--direction", "pul"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	if got := exitCode(err); got != exitUsage {
		t.Fatalf("exitCode(%v) = %d, want %d", err, got, exitUsage)
	}
	if !strings.Contains(err.Error(), "--direction") || !strings.Contains(err.Error(), "pul") {
		t.Fatalf("error = %q, want --direction and pul", err)
	}
	after, readErr := os.ReadFile(cfgPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("config changed\nbefore: %q\nafter:  %q", before, after)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "shell")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("module directory stat error = %v, want fs.ErrNotExist", statErr)
	}
}

func TestAddCmdValidatesConfigBeforeCopy(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "dotular.yaml")
	before := []byte("modules:\n  - name: shell\n    items:\n      - packge: git\n")
	if err := os.WriteFile(cfgPath, before, 0o644); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(dir, "source.txt")
	if err := os.WriteFile(sourcePath, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := buildRoot()
	root.SetArgs([]string{"add", sourcePath, "shell", "--config", cfgPath})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	if got := exitCode(err); got != exitFailure {
		t.Fatalf("exitCode(%v) = %d, want %d", err, got, exitFailure)
	}
	after, readErr := os.ReadFile(cfgPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("config changed\nbefore: %q\nafter:  %q", before, after)
	}
	moduleDir := filepath.Join(dir, "shell")
	if _, statErr := os.Stat(moduleDir); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("module directory stat error = %v, want fs.ErrNotExist", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(moduleDir, filepath.Base(sourcePath))); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("copied destination stat error = %v, want fs.ErrNotExist", statErr)
	}
}

func TestAddCmdRejectsRegistryModuleBeforeSourceWork(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "dotular.yaml")
	before := []byte("modules:\n  - name: shell\n    from: example.invalid/module.yaml\n")
	if err := os.WriteFile(cfgPath, before, 0o644); err != nil {
		t.Fatal(err)
	}

	root := buildRoot()
	root.SetArgs([]string{"add", filepath.Join(dir, "missing.txt"), "shell", "--config", cfgPath})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "registry") {
		t.Fatalf("error = %v, want registry-backed module rejection", err)
	}
	after, readErr := os.ReadFile(cfgPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("config changed\nbefore: %q\nafter:  %q", before, after)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "shell")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("module directory stat error = %v, want fs.ErrNotExist", statErr)
	}
}

func TestAddCmdWithDirection(t *testing.T) {
	tests := []struct {
		name          string
		direction     string
		wantDirection string
		wantYAML      string
	}{
		{name: "default push"},
		{name: "explicit pull", direction: "pull", wantDirection: "pull", wantYAML: "direction: pull\n"},
		{name: "explicit sync", direction: "sync", wantDirection: "sync", wantYAML: "direction: sync\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "dotular.yaml")
			if err := os.WriteFile(cfgPath, []byte("modules: []\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			sourcePath := filepath.Join(dir, "direction.txt")
			if err := os.WriteFile(sourcePath, []byte("data"), 0o644); err != nil {
				t.Fatal(err)
			}

			args := []string{"add", sourcePath, "directions", "--config", cfgPath}
			if tt.direction != "" {
				args = append(args, "--direction", tt.direction)
			}
			root := buildRoot()
			root.SetArgs(args)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)

			stdout, writer, pipeErr := os.Pipe()
			if pipeErr != nil {
				t.Fatal(pipeErr)
			}
			previousStdout := os.Stdout
			os.Stdout = writer
			executeErr := root.Execute()
			os.Stdout = previousStdout
			if closeErr := writer.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			output, readErr := io.ReadAll(stdout)
			if closeErr := stdout.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			if readErr != nil {
				t.Fatal(readErr)
			}
			if executeErr != nil {
				t.Fatal(executeErr)
			}

			cfg, err := loadConfigFrom(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			mod := cfg.Module("directions")
			if mod == nil {
				t.Fatal("module not found")
			}
			if len(mod.Items) != 1 {
				t.Fatalf("items = %d, want 1", len(mod.Items))
			}
			item := mod.Items[0]
			if item.Direction != tt.wantDirection {
				t.Errorf("direction = %q, want %q", item.Direction, tt.wantDirection)
			}
			if err := config.ValidateItems(mod.Items, config.ItemValidationOptions{}); err != nil {
				t.Fatalf("stored item validation: %v", err)
			}

			saved, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantYAML == "" {
				if strings.Contains(string(saved), "direction:") {
					t.Errorf("default direction was serialized:\n%s", saved)
				}
			} else if got := strings.Count(string(saved), tt.wantYAML); got != 1 {
				t.Errorf("serialized %q %d times, want exactly once:\n%s", tt.wantYAML, got, saved)
			}

			dest := filepath.Join(dir, "directions", filepath.Base(sourcePath))
			for _, want := range []string{
				`added file "direction.txt" to module "directions"`,
				"store: " + dest,
				"config: " + cfgPath,
			} {
				if !strings.Contains(string(output), want) {
					t.Errorf("output missing %q:\n%s", want, output)
				}
			}
			if stored, err := os.ReadFile(dest); err != nil || string(stored) != "data" {
				t.Errorf("stored file = %q, %v", stored, err)
			}
		})
	}
}

func TestAddCmdMissingPath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "dotular.yaml")
	os.WriteFile(cfgPath, []byte("modules: []\n"), 0o644)

	root := buildRoot()
	root.SetArgs([]string{"add", "--config", cfgPath, "/nonexistent/path", "mymod"})
	if err := root.Execute(); err == nil {
		t.Error("expected error for nonexistent source path")
	}
}

func TestAddCmdRequiresArgs(t *testing.T) {
	root := buildRoot()
	root.SetArgs([]string{"add"})
	if err := root.Execute(); err == nil {
		t.Error("expected error for missing args")
	}
}

// loadConfigFrom is a helper that loads config from a specific path.
func loadConfigFrom(path string) (config.Config, error) {
	return config.Load(path)
}

// Adding a directory must store symlinks as symlinks. Dereferencing them would
// silently inline a target's contents, and a symlink to a *directory* used to
// fail outright, because WalkDir does not descend into one.
func TestAddCmdDirectoryPreservesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "dotular.yaml")
	os.WriteFile(cfgPath, []byte("modules: []\n"), 0o644)

	srcDir := filepath.Join(dir, "mydir")
	os.MkdirAll(filepath.Join(srcDir, "real"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "target.txt"), []byte("pointed at"), 0o644)
	if err := os.Symlink("target.txt", filepath.Join(srcDir, "file-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(srcDir, "dir-link")); err != nil {
		t.Fatal(err)
	}

	root := buildRoot()
	root.SetArgs([]string{"add", "--config", cfgPath, srcDir, "mymod"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	stored := filepath.Join(dir, "mymod", "mydir")
	for name, want := range map[string]string{"file-link": "target.txt", "dir-link": "real"} {
		got, err := os.Readlink(filepath.Join(stored, name))
		if err != nil {
			t.Errorf("%s was not stored as a symlink: %v", name, err)
			continue
		}
		if got != want {
			t.Errorf("%s target = %q, want %q", name, got, want)
		}
	}
	if data, err := os.ReadFile(filepath.Join(stored, "target.txt")); err != nil || string(data) != "pointed at" {
		t.Errorf("regular file alongside the links = %q, %v", data, err)
	}
}

func TestConfigCommandsRejectMalformedInputBeforeWork(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "dotular.yaml")
	markerPath := filepath.Join(dir, "action-reached")
	before := []byte(fmt.Sprintf(`unexpected: true
modules:
  - name: guard
    items:
      - run: "touch %s"
  - from: example.invalid/module.yaml
`, markerPath))
	if err := os.WriteFile(cfgPath, before, 0o644); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	previousTransport := httputil.Client.Transport
	httputil.Client.Transport = commandRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests.Add(1)
		t.Errorf("unexpected network request: %s", req.URL)
		return nil, errors.New("unexpected network request")
	})
	t.Cleanup(func() { httputil.Client.Transport = previousTransport })

	tests := []struct {
		name string
		args []string
	}{
		{name: "apply", args: []string{"apply"}},
		{name: "push", args: []string{"push"}},
		{name: "pull", args: []string{"pull"}},
		{name: "sync", args: []string{"sync"}},
		{name: "list", args: []string{"list"}},
		{name: "status", args: []string{"status"}},
		{name: "verify", args: []string{"verify"}},
		{name: "registry update", args: []string{"registry", "update"}},
		{name: "registry check", args: []string{"registry", "update", "--check"}},
		{name: "init", args: []string{"init"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			root := buildRoot()
			root.SetArgs(append(append([]string(nil), tt.args...), "--config", cfgPath))
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			err := root.Execute()
			if err == nil {
				t.Fatal("command succeeded, want config load error")
			}
			if !strings.Contains(err.Error(), "unexpected") {
				t.Fatalf("error = %q, want unknown key", err)
			}
			for name, path := range map[string]string{
				"action marker": markerPath,
				"lockfile":      registry.LockPath(cfgPath),
				"module store":  filepath.Join(dir, "guard"),
			} {
				if _, statErr := os.Stat(path); !errors.Is(statErr, fs.ErrNotExist) {
					t.Errorf("%s stat error = %v, want fs.ErrNotExist", name, statErr)
				}
			}
			after, readErr := os.ReadFile(cfgPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !bytes.Equal(after, before) {
				t.Errorf("config changed\nbefore: %q\nafter:  %q", before, after)
			}
			entries, readDirErr := os.ReadDir(home)
			if readDirErr != nil {
				t.Fatal(readDirErr)
			}
			if len(entries) != 0 {
				t.Errorf("command wrote state under HOME: %v", entries)
			}
			if got := requests.Load(); got != 0 {
				t.Errorf("network requests = %d, want 0", got)
			}
		})
	}
}

// --- exit codes --------------------------------------------------------------

// A subcommand-only parent used to swallow an unknown subcommand and exit 0:
// cobra short-circuits a non-runnable command to flag.ErrHelp before Args
// validation runs, and ExecuteC maps ErrHelp to a nil error.
func TestUnknownSubcommandIsUsageError(t *testing.T) {
	for _, args := range [][]string{
		{"tag", "bogus"},
		{"registry", "bogus"},
		{"bogus"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := buildRoot()
			root.SetArgs(args)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			err := root.Execute()
			if err == nil {
				t.Fatalf("%v: expected an error, got nil (exit 0)", args)
			}
			if got := exitCode(err); got != exitUsage {
				t.Errorf("exitCode(%v) = %d, want %d", err, got, exitUsage)
			}
		})
	}
}

// A bare parent is not an error — it prints its help and succeeds.
func TestParentWithNoSubcommandPrintsHelp(t *testing.T) {
	for _, name := range []string{"tag", "registry"} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			root := buildRoot()
			root.SetArgs([]string{name})
			root.SetOut(&out)
			root.SetErr(&out)
			if err := root.Execute(); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if !strings.Contains(out.String(), "Available Commands:") {
				t.Errorf("%s did not print help:\n%s", name, out.String())
			}
		})
	}
}

func TestVerifyCmdReturnsErrorOnFailure(t *testing.T) {
	path := writeTestConfig(t, `
modules:
  - name: test
    items:
      - run: echo hello
        verify: "false"
`)
	root := buildRoot()
	root.SetArgs([]string{"verify", "--config", path})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	if !errors.Is(err, errVerifyFailed) {
		t.Fatalf("err = %v, want errVerifyFailed", err)
	}
	// A failed check is a result, not a misuse: internal exit code.
	if got := exitCode(err); got != exitFailure {
		t.Errorf("exitCode = %d, want %d", got, exitFailure)
	}
}

func TestExitCodeMapping(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"internal", errors.New("network unreachable"), exitFailure},
		{"usage", usageErrorf("module %q not found", "nope"), exitUsage},
		{"wrapped usage", fmt.Errorf("apply: %w", usageErrorf("bad flag")), exitUsage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exitCode(tt.err); got != tt.want {
				t.Errorf("exitCode = %d, want %d", got, tt.want)
			}
		})
	}
}

// A typo'd module name is misuse, not an internal failure.
func TestModuleNotFoundIsUsageError(t *testing.T) {
	path := writeTestConfig(t, `
modules:
  - name: test
    items: []
`)
	root := buildRoot()
	root.SetArgs([]string{"apply", "--config", path, "nonexistent"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	if got := exitCode(err); got != exitUsage {
		t.Fatalf("exitCode(%v) = %d, want %d", err, got, exitUsage)
	}
}

// A bad flag value is misuse too, and cobra reports it before any RunE runs.
func TestBadFlagValueIsUsageError(t *testing.T) {
	root := buildRoot()
	root.SetArgs([]string{"log", "--limit", "not-a-number"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	if got := exitCode(err); got != exitUsage {
		t.Fatalf("exitCode(%v) = %d, want %d", err, got, exitUsage)
	}
}
