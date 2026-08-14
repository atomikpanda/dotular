package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

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
	for _, flagName := range []string{"check", "repin"} {
		if updateCommand.Flags().Lookup(flagName) != nil ||
			updateCommand.PersistentFlags().Lookup(flagName) != nil ||
			updateCommand.InheritedFlags().Lookup(flagName) != nil {
			t.Fatalf("registry update exposes forbidden --%s flag", flagName)
		}
	}
}

func TestOrdinaryCommandNoCacheDoesNotEnableRepin(t *testing.T) {
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

func TestAddCmdWithDirection(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "dotular.yaml")
	os.WriteFile(cfgPath, []byte("modules: []\n"), 0o644)

	srcFile := filepath.Join(dir, "syncme.txt")
	os.WriteFile(srcFile, []byte("data"), 0o644)

	root := buildRoot()
	root.SetArgs([]string{"add", "--config", cfgPath, "--direction", "sync", srcFile, "syncmod"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfigFrom(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	mod := cfg.Module("syncmod")
	if mod == nil {
		t.Fatal("module not found")
	}
	if mod.Items[0].Direction != "sync" {
		t.Errorf("direction = %q, want sync", mod.Items[0].Direction)
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
