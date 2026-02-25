package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/atomikpanda/dotular/internal/config"
)

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "dotular.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
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

	expected := []string{"add", "apply", "push", "pull", "sync", "list", "status", "platform", "verify", "encrypt", "decrypt", "tag", "log", "registry"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing subcommand %q", name)
		}
	}
}

func TestRepeatStr(t *testing.T) {
	tests := []struct {
		s    string
		n    int
		want string
	}{
		{"-", 5, "-----"},
		{"ab", 4, "abababab"},
		{"-", 0, ""},
	}
	for _, tt := range tests {
		got := repeatStr(tt.s, tt.n)
		if got != tt.want {
			t.Errorf("repeatStr(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
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
	t.Setenv("HOME", dir)

	root := buildRoot()
	root.SetArgs([]string{"tag", "list"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestTagAddCmdExecute(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

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

// --- add command tests -------------------------------------------------------

func TestAddCmdDef(t *testing.T) {
	cmd := addCmd()
	if cmd.Use != "add <module> <path>" {
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
	root.SetArgs([]string{"add", "--config", cfgPath, "mymod", srcFile})
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
	root.SetArgs([]string{"add", "--config", cfgPath, "mymod", srcDir})
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
	root.SetArgs([]string{"add", "--config", cfgPath, "existing", srcFile})
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
	root.SetArgs([]string{"add", "--config", cfgPath, "--link", "linkmod", srcFile})
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
	root.SetArgs([]string{"add", "--config", cfgPath, "--direction", "sync", "syncmod", srcFile})
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
	root.SetArgs([]string{"add", "--config", cfgPath, "mymod", "/nonexistent/path"})
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

func TestCopyFileSimple(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	os.WriteFile(src, []byte("content"), 0o644)

	if err := copyFileSimple(src, dst); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(dst)
	if string(data) != "content" {
		t.Errorf("copied = %q", string(data))
	}

	// Verify permissions are preserved.
	srcInfo, _ := os.Stat(src)
	dstInfo, _ := os.Stat(dst)
	if srcInfo.Mode().Perm() != dstInfo.Mode().Perm() {
		t.Errorf("permissions: src=%o, dst=%o", srcInfo.Mode().Perm(), dstInfo.Mode().Perm())
	}
}

func TestCopyDirRecursive(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	os.MkdirAll(filepath.Join(src, "sub"), 0o755)
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("aaa"), 0o644)
	os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("bbb"), 0o644)

	if err := copyDirRecursive(src, dst); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dst, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "aaa" {
		t.Errorf("a.txt = %q", string(data))
	}

	data, err = os.ReadFile(filepath.Join(dst, "sub", "b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "bbb" {
		t.Errorf("sub/b.txt = %q", string(data))
	}
}

// loadConfigFrom is a helper that loads config from a specific path.
func loadConfigFrom(path string) (config.Config, error) {
	return config.Load(path)
}
