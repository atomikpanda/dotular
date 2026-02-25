package runner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/atomikpanda/dotular/internal/config"
)

func newTestRunner(cfg config.Config) *Runner {
	return &Runner{
		Config:      cfg,
		DryRun:      true,
		Verbose:     true,
		Atomic:      false,
		OS:          "darwin",
		MachineTags: []string{"darwin", "amd64", "testhost"},
		Out:         &bytes.Buffer{},
		Command:     "apply",
	}
}

func TestBuildActionPackage(t *testing.T) {
	r := newTestRunner(config.Config{})
	item := config.Item{Package: "git", Via: "brew"}
	action, skip, err := r.buildAction(item, "mymod")
	if err != nil {
		t.Fatal(err)
	}
	if skip {
		t.Error("should not skip brew on darwin")
	}
	if action == nil {
		t.Fatal("action should not be nil")
	}
	if action.Describe() == "" {
		t.Error("Describe() should not be empty")
	}
}

func TestBuildActionPackageSkipWrongOS(t *testing.T) {
	r := newTestRunner(config.Config{})
	r.OS = "linux"
	item := config.Item{Package: "git", Via: "brew"}
	_, skip, err := r.buildAction(item)
	if err != nil {
		t.Fatal(err)
	}
	if !skip {
		t.Error("should skip brew on linux")
	}
}

func TestBuildActionScript(t *testing.T) {
	r := newTestRunner(config.Config{})
	item := config.Item{Script: "setup.sh", Via: "local"}
	action, skip, err := r.buildAction(item)
	if err != nil {
		t.Fatal(err)
	}
	if skip {
		t.Error("should not skip script")
	}
	if action == nil {
		t.Fatal("action should not be nil")
	}
}

func TestBuildActionFile(t *testing.T) {
	r := newTestRunner(config.Config{})
	item := config.Item{
		File:        ".vimrc",
		Destination: config.PlatformMap{MacOS: "~/", Windows: "", Linux: ""},
	}
	action, skip, err := r.buildAction(item, "editor")
	if err != nil {
		t.Fatal(err)
	}
	if skip {
		t.Error("should not skip file with darwin destination")
	}
	if action == nil {
		t.Fatal("action should not be nil")
	}
}

func TestBuildActionFileNoDestination(t *testing.T) {
	r := newTestRunner(config.Config{})
	item := config.Item{
		File:        ".vimrc",
		Destination: config.PlatformMap{MacOS: "", Windows: `C:\`, Linux: ""},
	}
	_, skip, _ := r.buildAction(item)
	if !skip {
		t.Error("should skip file with empty darwin destination")
	}
}

func TestBuildActionDirectory(t *testing.T) {
	r := newTestRunner(config.Config{})
	item := config.Item{
		Directory:   "nvim",
		Destination: config.PlatformMap{MacOS: "~/.config/", Windows: "", Linux: ""},
	}
	action, skip, err := r.buildAction(item, "editor")
	if err != nil {
		t.Fatal(err)
	}
	if skip || action == nil {
		t.Error("should build directory action")
	}
}

func TestBuildActionDirectoryNoDestination(t *testing.T) {
	r := newTestRunner(config.Config{})
	item := config.Item{
		Directory:   "nvim",
		Destination: config.PlatformMap{},
	}
	_, skip, _ := r.buildAction(item)
	if !skip {
		t.Error("should skip directory with empty destination")
	}
}

func TestBuildActionBinary(t *testing.T) {
	r := newTestRunner(config.Config{})
	item := config.Item{
		Binary:  "nvim",
		Version: "0.10.0",
		Source:  config.PlatformMap{MacOS: "https://example.com/nvim.tar.gz"},
	}
	action, skip, err := r.buildAction(item)
	if err != nil {
		t.Fatal(err)
	}
	if skip || action == nil {
		t.Error("should build binary action")
	}
}

func TestBuildActionBinaryNoSource(t *testing.T) {
	r := newTestRunner(config.Config{})
	item := config.Item{
		Binary: "nvim",
		Source: config.PlatformMap{Linux: "https://example.com/nvim"},
	}
	_, skip, _ := r.buildAction(item)
	if !skip {
		t.Error("should skip binary with no darwin source")
	}
}

func TestBuildActionBinaryDefaultInstallTo(t *testing.T) {
	r := newTestRunner(config.Config{})
	item := config.Item{
		Binary: "tool",
		Source: config.PlatformMap{MacOS: "https://example.com/tool"},
	}
	action, skip, err := r.buildAction(item)
	if err != nil {
		t.Fatal(err)
	}
	if skip || action == nil {
		t.Error("should build binary action with default install_to")
	}
}

func TestBuildActionRun(t *testing.T) {
	r := newTestRunner(config.Config{})
	item := config.Item{Run: "echo hello", After: "package"}
	action, skip, err := r.buildAction(item)
	if err != nil {
		t.Fatal(err)
	}
	if skip || action == nil {
		t.Error("should build run action")
	}
}

func TestBuildActionSetting(t *testing.T) {
	r := newTestRunner(config.Config{})
	item := config.Item{Setting: "com.apple.dock", Key: "autohide", Value: true}
	action, skip, err := r.buildAction(item)
	if err != nil {
		t.Fatal(err)
	}
	if skip || action == nil {
		t.Error("should build setting action")
	}
}

func TestBuildActionUnknown(t *testing.T) {
	r := newTestRunner(config.Config{})
	item := config.Item{}
	_, _, err := r.buildAction(item)
	if err == nil {
		t.Error("expected error for unknown item type")
	}
}

func TestBuildActionSourcePrefix(t *testing.T) {
	r := newTestRunner(config.Config{})
	item := config.Item{Script: "install.sh", Via: "local"}

	// With module name.
	action, _, _ := r.buildAction(item, "mymod")
	desc := action.Describe()
	if desc == "" {
		t.Error("Describe() should not be empty")
	}

	// Without module name.
	action2, _, _ := r.buildAction(item)
	desc2 := action2.Describe()
	if desc2 == "" {
		t.Error("Describe() should not be empty")
	}
}

func TestMatchesTags(t *testing.T) {
	r := newTestRunner(config.Config{})
	tests := []struct {
		name string
		mod  config.Module
		want bool
	}{
		{"no tags", config.Module{Name: "a"}, true},
		{"only match", config.Module{Name: "b", OnlyTags: []string{"darwin"}}, true},
		{"only no match", config.Module{Name: "c", OnlyTags: []string{"windows"}}, false},
		{"exclude match", config.Module{Name: "d", ExcludeTags: []string{"darwin"}}, false},
		{"exclude no match", config.Module{Name: "e", ExcludeTags: []string{"windows"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.matchesTags(tt.mod); got != tt.want {
				t.Errorf("matchesTags() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSkipManager(t *testing.T) {
	r := newTestRunner(config.Config{})
	if r.skipManager("brew") {
		t.Error("should not skip brew on darwin")
	}
	if !r.skipManager("apt") {
		t.Error("should skip apt on darwin")
	}
	if r.skipManager("nix") {
		t.Error("should not skip nix (cross-platform)")
	}
	if !r.skipManager("winget") {
		t.Error("should skip winget on darwin")
	}
}

func TestFileDirection(t *testing.T) {
	r := newTestRunner(config.Config{})

	item := config.Item{File: "a", Direction: "pull"}
	if got := r.fileDirection(item); got != "pull" {
		t.Errorf("fileDirection() = %q, want pull", got)
	}

	// Default direction.
	itemDefault := config.Item{File: "a"}
	if got := r.fileDirection(itemDefault); got != "push" {
		t.Errorf("fileDirection() default = %q, want push", got)
	}

	// With override.
	r.DirectionOverride = "sync"
	if got := r.fileDirection(item); got != "sync" {
		t.Errorf("fileDirection() with override = %q, want sync", got)
	}

	// Link items ignore override.
	linkItem := config.Item{File: "a", Link: true}
	if got := r.fileDirection(linkItem); got != "push" {
		t.Errorf("fileDirection() link = %q, want push", got)
	}
}

func TestApplyAllDryRun(t *testing.T) {
	cfg := config.Config{
		Modules: []config.Module{
			{
				Name: "test",
				Items: []config.Item{
					{Run: "echo hello"},
				},
			},
		},
	}
	r := newTestRunner(cfg)
	if err := r.ApplyAll(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestApplyAllTagFilter(t *testing.T) {
	cfg := config.Config{
		Modules: []config.Module{
			{Name: "skipped", OnlyTags: []string{"windows"}, Items: []config.Item{{Run: "echo"}}},
			{Name: "applied", Items: []config.Item{{Run: "echo"}}},
		},
	}
	r := newTestRunner(cfg)
	var buf bytes.Buffer
	r.Out = &buf

	if err := r.ApplyAll(context.Background()); err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	if !containsStr(output, "applied") {
		t.Error("expected 'applied' module in output")
	}
}

func TestApplyModuleDryRun(t *testing.T) {
	mod := config.Module{
		Name: "testmod",
		Items: []config.Item{
			{Package: "git", Via: "brew"},
			{Run: "echo done"},
			{File: ".vimrc", Destination: config.PlatformMap{MacOS: "~/"}},
		},
	}
	r := newTestRunner(config.Config{})
	if err := r.ApplyModule(context.Background(), mod); err != nil {
		t.Fatal(err)
	}
}

func TestApplyModuleDryRunWithHooks(t *testing.T) {
	mod := config.Module{
		Name: "hookmod",
		Items: []config.Item{
			{Run: "echo hello"},
		},
		Hooks: config.ModuleHooks{
			BeforeApply: "echo before",
			AfterApply:  "echo after",
		},
	}
	r := newTestRunner(config.Config{})
	var buf bytes.Buffer
	r.Out = &buf
	if err := r.ApplyModule(context.Background(), mod); err != nil {
		t.Fatal(err)
	}
}

func TestApplyModuleDryRunWithSyncHooks(t *testing.T) {
	mod := config.Module{
		Name: "syncmod",
		Items: []config.Item{
			{File: "test.txt", Destination: config.PlatformMap{MacOS: "~/"}, Direction: "sync"},
		},
		Hooks: config.ModuleHooks{
			BeforeSync: "echo before-sync",
			AfterSync:  "echo after-sync",
		},
	}
	r := newTestRunner(config.Config{})
	r.DirectionOverride = "sync"
	var buf bytes.Buffer
	r.Out = &buf
	if err := r.ApplyModule(context.Background(), mod); err != nil {
		t.Fatal(err)
	}
}

func TestApplyItemSkipIf(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}
	mod := config.Module{
		Name: "skip-test",
		Items: []config.Item{
			{Run: "echo hello", SkipIf: "true"},
		},
	}
	r := newTestRunner(config.Config{})
	r.DryRun = false
	var buf bytes.Buffer
	r.Out = &buf
	if err := r.ApplyModule(context.Background(), mod); err != nil {
		t.Fatal(err)
	}
	if !containsStr(buf.String(), "skip") {
		t.Error("expected skip output")
	}
}

func TestApplyItemVerify(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}
	mod := config.Module{
		Name: "verify-test",
		Items: []config.Item{
			{Run: "true", Verify: "true"},
		},
	}
	r := newTestRunner(config.Config{})
	r.DryRun = false
	var buf bytes.Buffer
	r.Out = &buf
	if err := r.ApplyModule(context.Background(), mod); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyModuleDryRun(t *testing.T) {
	mod := config.Module{
		Name: "verify-mod",
		Items: []config.Item{
			{Run: "echo hello"},
			{Run: "echo world", Verify: "true"},
		},
	}
	r := newTestRunner(config.Config{})
	r.DryRun = false

	// VerifyModule runs verify commands.
	// "true" will pass on Unix.
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}

	var buf bytes.Buffer
	r.Out = &buf
	passed, err := r.VerifyModule(context.Background(), mod)
	if err != nil {
		t.Fatal(err)
	}
	if !passed {
		t.Error("expected all verify checks to pass")
	}
}

func TestVerifyAllDryRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	cfg := config.Config{
		Modules: []config.Module{
			{Name: "a", Items: []config.Item{{Run: "echo", Verify: "true"}}},
			{Name: "b", OnlyTags: []string{"windows"}, Items: []config.Item{{Run: "echo", Verify: "true"}}},
		},
	}
	r := newTestRunner(cfg)
	r.DryRun = false
	var buf bytes.Buffer
	r.Out = &buf
	passed, err := r.VerifyAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !passed {
		t.Error("expected all verify to pass")
	}
}

func TestRunHookEmpty(t *testing.T) {
	r := newTestRunner(config.Config{})
	err := r.runHook(context.Background(), "", "module", "test", "before_apply")
	if err != nil {
		t.Errorf("empty hook should not error: %v", err)
	}
}

func TestRunHookDryRun(t *testing.T) {
	r := newTestRunner(config.Config{})
	var buf bytes.Buffer
	r.Out = &buf
	err := r.runHook(context.Background(), "echo hello", "module", "test", "before_apply")
	if err != nil {
		t.Errorf("dry-run hook should not error: %v", err)
	}
	if !containsStr(buf.String(), "hook") {
		t.Error("expected hook in dry-run output")
	}
}

func TestRunHookVerbose(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	r := newTestRunner(config.Config{})
	r.DryRun = false
	var buf bytes.Buffer
	r.Out = &buf
	err := r.runHook(context.Background(), "true", "module", "test", "before_apply")
	if err != nil {
		t.Errorf("hook should not error: %v", err)
	}
}

func TestNewRunner(t *testing.T) {
	cfg := config.Config{
		Modules: []config.Module{
			{Name: "test", Items: []config.Item{{Run: "echo"}}},
		},
	}
	r := New(cfg, true, false, true)
	if r == nil {
		t.Fatal("New() returned nil")
	}
	if !r.DryRun {
		t.Error("expected DryRun=true")
	}
	if r.OS == "" {
		t.Error("expected non-empty OS")
	}
	if r.Command != "apply" {
		t.Errorf("Command = %q, want apply", r.Command)
	}
}

func TestNewRunnerWithAge(t *testing.T) {
	cfg := config.Config{
		Age: &config.AgeConfig{Passphrase: "secret"},
	}
	r := New(cfg, false, false, true)
	if r.AgeKey == nil {
		t.Error("expected AgeKey to be set")
	}
}

func TestResolveAgeKeyFromConfig(t *testing.T) {
	cfg := &config.AgeConfig{Passphrase: "secret"}
	key := resolveAgeKey(cfg)
	if key == nil {
		t.Fatal("expected key")
	}
	if key.Passphrase != "secret" {
		t.Errorf("Passphrase = %q", key.Passphrase)
	}
}

func TestResolveAgeKeyEnvPassphrase(t *testing.T) {
	cfg := &config.AgeConfig{Passphrase: "env:MY_AGE_PASS"}
	t.Setenv("MY_AGE_PASS", "from-env")
	key := resolveAgeKey(cfg)
	if key == nil {
		t.Fatal("expected key")
	}
	if key.Passphrase != "from-env" {
		t.Errorf("Passphrase = %q", key.Passphrase)
	}
}

func TestResolveAgeKeyIdentity(t *testing.T) {
	cfg := &config.AgeConfig{Identity: "~/.age/key.txt"}
	key := resolveAgeKey(cfg)
	if key == nil {
		t.Fatal("expected key")
	}
	if key.IdentityFile == "" {
		t.Error("expected non-empty IdentityFile")
	}
}

func TestResolveAgeKeyNil(t *testing.T) {
	t.Setenv("DOTULAR_AGE_IDENTITY", "")
	t.Setenv("DOTULAR_AGE_PASSPHRASE", "")
	key := resolveAgeKey(nil)
	if key != nil {
		t.Error("expected nil key when no config")
	}
}

func TestResolveAgeKeyEnvIdentityFallback(t *testing.T) {
	t.Setenv("DOTULAR_AGE_IDENTITY", "/path/to/key")
	t.Setenv("DOTULAR_AGE_PASSPHRASE", "")
	key := resolveAgeKey(nil)
	if key == nil {
		t.Fatal("expected key from env")
	}
	if key.IdentityFile == "" {
		t.Error("expected non-empty IdentityFile")
	}
}

func TestResolveAgeKeyEnvPassphraseFallback(t *testing.T) {
	t.Setenv("DOTULAR_AGE_IDENTITY", "")
	t.Setenv("DOTULAR_AGE_PASSPHRASE", "env-pass")
	key := resolveAgeKey(nil)
	if key == nil {
		t.Fatal("expected key from env")
	}
	if key.Passphrase != "env-pass" {
		t.Errorf("Passphrase = %q", key.Passphrase)
	}
}

func TestResolveAgeKeyEmptyConfig(t *testing.T) {
	t.Setenv("DOTULAR_AGE_IDENTITY", "")
	t.Setenv("DOTULAR_AGE_PASSPHRASE", "")
	cfg := &config.AgeConfig{}
	key := resolveAgeKey(cfg)
	if key != nil {
		t.Error("expected nil key for empty age config")
	}
}

func TestApplyModuleSkipsOSMismatch(t *testing.T) {
	mod := config.Module{
		Name: "os-skip",
		Items: []config.Item{
			{Package: "git", Via: "apt"},
		},
	}
	r := newTestRunner(config.Config{})
	var buf bytes.Buffer
	r.Out = &buf
	if err := r.ApplyModule(context.Background(), mod); err != nil {
		t.Fatal(err)
	}
	if !containsStr(buf.String(), "skip") {
		t.Error("expected skip output for apt on darwin")
	}
}

func TestApplyItemWithItemHooks(t *testing.T) {
	mod := config.Module{
		Name: "item-hooks",
		Items: []config.Item{
			{
				Run: "echo hello",
				Hooks: config.ItemHooks{
					BeforeApply: "echo before-item",
					AfterApply:  "echo after-item",
				},
			},
		},
	}
	r := newTestRunner(config.Config{})
	var buf bytes.Buffer
	r.Out = &buf
	if err := r.ApplyModule(context.Background(), mod); err != nil {
		t.Fatal(err)
	}
}

func TestApplyModuleNonDryRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	mod := config.Module{
		Name: "real-apply",
		Items: []config.Item{
			{Run: "true"},
		},
	}
	r := newTestRunner(config.Config{})
	r.DryRun = false
	r.Atomic = false
	var buf bytes.Buffer
	r.Out = &buf
	if err := r.ApplyModule(context.Background(), mod); err != nil {
		t.Fatal(err)
	}
}

func TestApplyModuleWithAtomic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	mod := config.Module{
		Name: "atomic-test",
		Items: []config.Item{
			{Run: "true"},
		},
	}
	r := newTestRunner(config.Config{})
	r.DryRun = false
	r.Atomic = true
	var buf bytes.Buffer
	r.Out = &buf
	if err := r.ApplyModule(context.Background(), mod); err != nil {
		t.Fatal(err)
	}
}

func TestApplyModuleAtomicRollback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	mod := config.Module{
		Name: "rollback-test",
		Items: []config.Item{
			{Run: "false"}, // This will fail.
		},
	}
	r := newTestRunner(config.Config{})
	r.DryRun = false
	r.Atomic = true
	var buf bytes.Buffer
	r.Out = &buf
	err := r.ApplyModule(context.Background(), mod)
	if err == nil {
		t.Error("expected error from failed command")
	}
	if !containsStr(buf.String(), "rollback") {
		t.Error("expected rollback message")
	}
}

func TestApplyModuleWithHooksNonDryRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	mod := config.Module{
		Name: "hooks-real",
		Items: []config.Item{
			{Run: "true"},
		},
		Hooks: config.ModuleHooks{
			BeforeApply: "true",
			AfterApply:  "true",
		},
	}
	r := newTestRunner(config.Config{})
	r.DryRun = false
	r.Atomic = false
	var buf bytes.Buffer
	r.Out = &buf
	if err := r.ApplyModule(context.Background(), mod); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyModuleFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	mod := config.Module{
		Name: "verify-fail",
		Items: []config.Item{
			{Run: "echo", Verify: "false"},
		},
	}
	r := newTestRunner(config.Config{})
	r.DryRun = false
	var buf bytes.Buffer
	r.Out = &buf
	passed, err := r.VerifyModule(context.Background(), mod)
	if err != nil {
		t.Fatal(err)
	}
	if passed {
		t.Error("expected verification to fail")
	}
}

func TestApplyModuleFileItemWithSnapshot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	dir := t.TempDir()
	// buildAction prepends moduleName to File, creating moduleName/File as the source.
	// So we create "file-snap/source.txt" relative to cwd.
	modDir := filepath.Join(dir, "file-snap")
	os.MkdirAll(modDir, 0o755)
	os.WriteFile(filepath.Join(modDir, "source.txt"), []byte("content"), 0o644)
	destDir := filepath.Join(dir, "dest")

	// Change working dir temporarily so relative paths resolve.
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	mod := config.Module{
		Name: "file-snap",
		Items: []config.Item{
			{
				File:        "source.txt",
				Destination: config.PlatformMap{MacOS: destDir + "/"},
				Direction:   "push",
			},
		},
	}
	r := newTestRunner(config.Config{})
	r.DryRun = false
	r.Atomic = true
	var buf bytes.Buffer
	r.Out = &buf
	if err := r.ApplyModule(context.Background(), mod); err != nil {
		t.Fatal(err)
	}
}

func TestApplyModuleDirItemWithSnapshot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	dir := t.TempDir()
	modDir := filepath.Join(dir, "dir-snap")
	srcDir := filepath.Join(modDir, "srcdir")
	os.MkdirAll(srcDir, 0o755)
	os.WriteFile(filepath.Join(srcDir, "f.txt"), []byte("data"), 0o644)
	destDir := filepath.Join(dir, "dest")

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	mod := config.Module{
		Name: "dir-snap",
		Items: []config.Item{
			{
				Directory:   "srcdir",
				Destination: config.PlatformMap{MacOS: destDir + "/"},
				Direction:   "push",
			},
		},
	}
	r := newTestRunner(config.Config{})
	r.DryRun = false
	r.Atomic = true
	var buf bytes.Buffer
	r.Out = &buf
	if err := r.ApplyModule(context.Background(), mod); err != nil {
		t.Fatal(err)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
