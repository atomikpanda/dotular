package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/atomikpanda/dotular/internal/actions"
	"github.com/atomikpanda/dotular/internal/audit"
	"github.com/atomikpanda/dotular/internal/config"
	"github.com/atomikpanda/dotular/internal/testutil"
	"github.com/atomikpanda/dotular/internal/ui"
)

// Applying items calls audit.Log, which resolves its path from the home
// directory, so without this the suite writes to the developer's real one.
func TestMain(m *testing.M) {
	os.Exit(testutil.IsolateHome(m))
}

func newTestRunner(cfg config.Config) *Runner {
	var buf bytes.Buffer
	return &Runner{
		Config:      cfg,
		DryRun:      true,
		Verbose:     true,
		Atomic:      false,
		OS:          "darwin",
		MachineTags: []string{"darwin", "amd64", "testhost"},
		Out:         &buf,
		UI:          ui.New(&buf, &bytes.Buffer{}),
		Command:     "apply",
	}
}

func TestBuildActionPackage(t *testing.T) {
	r := newTestRunner(config.Config{})
	item := config.Item{Package: "git", Via: "brew"}
	action, skipReason, err := r.buildAction(item, "mymod")
	if err != nil {
		t.Fatal(err)
	}
	if skipReason != "" {
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
	_, skipReason, err := r.buildAction(item)
	if err != nil {
		t.Fatal(err)
	}
	if skipReason == "" {
		t.Error("should skip brew on linux")
	}
}

func TestBuildActionScript(t *testing.T) {
	r := newTestRunner(config.Config{})
	item := config.Item{Script: "setup.sh", Via: "local"}
	action, skipReason, err := r.buildAction(item)
	if err != nil {
		t.Fatal(err)
	}
	if skipReason != "" {
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
	action, skipReason, err := r.buildAction(item, "editor")
	if err != nil {
		t.Fatal(err)
	}
	if skipReason != "" {
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
	_, skipReason, _ := r.buildAction(item)
	if skipReason == "" {
		t.Error("should skip file with empty darwin destination")
	}
}

func TestBuildActionDirectory(t *testing.T) {
	r := newTestRunner(config.Config{})
	item := config.Item{
		Directory:   "nvim",
		Destination: config.PlatformMap{MacOS: "~/.config/", Windows: "", Linux: ""},
	}
	action, skipReason, err := r.buildAction(item, "editor")
	if err != nil {
		t.Fatal(err)
	}
	if skipReason != "" || action == nil {
		t.Error("should build directory action")
	}
}

func TestBuildActionDirectoryNoDestination(t *testing.T) {
	r := newTestRunner(config.Config{})
	item := config.Item{
		Directory:   "nvim",
		Destination: config.PlatformMap{},
	}
	_, skipReason, _ := r.buildAction(item)
	if skipReason == "" {
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
	action, skipReason, err := r.buildAction(item)
	if err != nil {
		t.Fatal(err)
	}
	if skipReason != "" || action == nil {
		t.Error("should build binary action")
	}
}

func TestBuildActionBinaryNoSource(t *testing.T) {
	r := newTestRunner(config.Config{})
	item := config.Item{
		Binary: "nvim",
		Source: config.PlatformMap{Linux: "https://example.com/nvim"},
	}
	_, skipReason, _ := r.buildAction(item)
	if skipReason == "" {
		t.Error("should skip binary with no darwin source")
	}
}

func TestBuildActionBinaryDefaultInstallTo(t *testing.T) {
	r := newTestRunner(config.Config{})
	item := config.Item{
		Binary: "tool",
		Source: config.PlatformMap{MacOS: "https://example.com/tool"},
	}
	action, skipReason, err := r.buildAction(item)
	if err != nil {
		t.Fatal(err)
	}
	if skipReason != "" || action == nil {
		t.Error("should build binary action with default install_to")
	}
}

func TestBuildActionRun(t *testing.T) {
	r := newTestRunner(config.Config{})
	item := config.Item{Run: "echo hello", After: "package"}
	action, skipReason, err := r.buildAction(item)
	if err != nil {
		t.Fatal(err)
	}
	if skipReason != "" || action == nil {
		t.Error("should build run action")
	}
}

func TestBuildActionRunSkippedOnPull(t *testing.T) {
	r := newTestRunner(config.Config{})
	r.DirectionOverride = "pull"
	item := config.Item{Run: "echo hello"}
	_, skipReason, err := r.buildAction(item)
	if err != nil {
		t.Fatal(err)
	}
	if skipReason == "" {
		t.Error("run action should be skipped on pull")
	}
}

func TestBuildActionSetting(t *testing.T) {
	r := newTestRunner(config.Config{})
	item := config.Item{Setting: "com.apple.dock", Key: "autohide", Value: true}
	action, skipReason, err := r.buildAction(item)
	if err != nil {
		t.Fatal(err)
	}
	if skipReason != "" || action == nil {
		t.Error("should build setting action")
	}
}

func TestBuildActionSettingSkipWrongOS(t *testing.T) {
	r := newTestRunner(config.Config{})
	r.OS = "linux"
	item := config.Item{Setting: "com.apple.dock", Key: "autohide", Value: true}
	_, skipReason, err := r.buildAction(item)
	if err != nil {
		t.Fatal(err)
	}
	if skipReason == "" {
		t.Error("setting should be skipped on linux")
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

// directionGateItems holds one item of every type: the five that only push state
// onto the machine, then the two that move files between repo and machine.
//
// The package item omits via so that PackageAction.IsApplied has no check
// command to run — the outcome must not depend on what the test host has
// installed.
var directionGateItems = []struct {
	name         string
	item         config.Item
	fileOriented bool // has something to do under pull and sync
}{
	{"package", config.Item{Package: "git"}, false},
	{"script", config.Item{Script: "install.sh", Via: "local"}, false},
	{"binary", config.Item{Binary: "tool", Source: config.PlatformMap{MacOS: "https://example.com/tool"}}, false},
	{"setting", config.Item{Setting: "com.apple.dock", Key: "autohide", Value: true}, false},
	{"run", config.Item{Run: "echo hello"}, false},
	{"file", config.Item{File: ".vimrc", Destination: config.PlatformMap{MacOS: "~/"}}, true},
	{"directory", config.Item{Directory: "nvim", Destination: config.PlatformMap{MacOS: "~/.config/"}}, true},
}

// pull and sync must not install packages, execute scripts, download binaries or
// write system settings; push must keep running all of them.
func TestApplyModuleDirectionGate(t *testing.T) {
	for _, override := range []string{"push", "pull", "sync"} {
		for _, tt := range directionGateItems {
			t.Run(override+"/"+tt.name, func(t *testing.T) {
				t.Setenv("HOME", t.TempDir()) // keep audit.Log out of the real home
				r := newTestRunner(config.Config{})
				r.Command = override
				r.DirectionOverride = override
				var buf bytes.Buffer
				r.Out = &buf
				r.UI = ui.New(&buf, &bytes.Buffer{})

				mod := config.Module{Name: "gate", Items: []config.Item{tt.item}}
				result := r.ApplyModule(context.Background(), mod)
				if result.Err != nil {
					t.Fatalf("unexpected error: %v", result.Err)
				}

				wantApplied, wantSkipped := 1, 0
				if override != "push" && !tt.fileOriented {
					wantApplied, wantSkipped = 0, 1
				}
				if result.Applied != wantApplied || result.Skipped != wantSkipped || result.Failed != 0 {
					t.Errorf("applied/skipped/failed = %d/%d/%d, want %d/%d/0",
						result.Applied, result.Skipped, result.Failed, wantApplied, wantSkipped)
				}
			})
		}
	}
}

func TestApplyModuleDirectionSkipVerbosity(t *testing.T) {
	const reason = "nothing to pull for a package item"
	tests := []struct {
		name    string
		verbose bool
	}{
		{"verbose", true},
		{"quiet", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir()) // keep audit.Log out of the real home
			r := newTestRunner(config.Config{})
			r.Verbose = tt.verbose
			r.Command = "pull"
			r.DirectionOverride = "pull"
			var buf bytes.Buffer
			r.Out = &buf
			r.UI = ui.New(&buf, &bytes.Buffer{})

			mod := config.Module{Name: "gate", Items: []config.Item{{Package: "git"}}}
			if result := r.ApplyModule(context.Background(), mod); result.Err != nil {
				t.Fatal(result.Err)
			}
			if got := containsStr(buf.String(), reason); got != tt.verbose {
				t.Errorf("reason in output = %v, want %v", got, tt.verbose)
			}
		})
	}
}

func TestApplyModuleDirectionSkipIsAudited(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("audit.Log resolves its path from HOME only on Unix")
	}
	t.Setenv("HOME", t.TempDir())
	r := newTestRunner(config.Config{})
	r.Command = "pull"
	r.DirectionOverride = "pull"
	var buf bytes.Buffer
	r.Out = &buf
	r.UI = ui.New(&buf, &bytes.Buffer{})

	mod := config.Module{Name: "gate", Items: []config.Item{{Package: "git", Via: "brew"}}}
	if result := r.ApplyModule(context.Background(), mod); result.Err != nil {
		t.Fatal(result.Err)
	}

	entries, err := audit.Read("gate", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d audit entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Outcome != "skipped" {
		t.Errorf("Outcome = %q, want skipped", e.Outcome)
	}
	if e.Reason != "nothing to pull for a package item" {
		t.Errorf("Reason = %q", e.Reason)
	}
	// The action's own description, exactly as for every other audit entry.
	if want := `install package "git" via brew`; e.Item != want {
		t.Errorf("Item = %q, want %q", e.Item, want)
	}
}

func TestApplyModuleSyncConflictEOFIsFailure(t *testing.T) {
	const (
		moduleName = "eof-conflict"
		fileName   = "test.txt"
	)
	dir := t.TempDir()
	chdir(t, dir)

	repoFile := filepath.Join(dir, moduleName, fileName)
	systemDir := filepath.Join(dir, "system")
	systemFile := filepath.Join(systemDir, fileName)
	write(t, repoFile, "repo version", 0o644)
	write(t, systemFile, "system version", 0o644)

	stdin, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() {
		os.Stdin = oldStdin
		stdin.Close()
	})

	r := newTestRunner(config.Config{})
	r.DryRun = false
	r.Command = "sync"
	mod := config.Module{
		Name: moduleName,
		Items: []config.Item{{
			File:        fileName,
			Destination: config.PlatformMap{MacOS: systemDir + string(os.PathSeparator)},
			Direction:   "sync",
		}},
	}

	result := r.ApplyModule(context.Background(), mod)
	if result.Err == nil {
		t.Fatal("expected unresolved conflict at EOF to fail")
	}
	if result.Applied != 0 || result.Skipped != 0 || result.Failed != 1 {
		t.Errorf("applied/skipped/failed = %d/%d/%d, want 0/0/1",
			result.Applied, result.Skipped, result.Failed)
	}

	entries, err := audit.Read(moduleName, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d audit entries, want 1", len(entries))
	}
	if entries[0].Outcome != "failure" {
		t.Errorf("audit outcome = %q, want failure", entries[0].Outcome)
	}
}

func TestApplyModuleAtomicSyncConflictSkipIsSkipped(t *testing.T) {
	const (
		moduleName = "skipped-conflict"
		fileName   = "test.txt"
	)
	dir := t.TempDir()
	chdir(t, dir)

	repoFile := filepath.Join(dir, moduleName, fileName)
	systemDir := filepath.Join(dir, "system")
	systemFile := filepath.Join(systemDir, fileName)
	write(t, repoFile, "repo version", 0o644)
	write(t, systemFile, "system version", 0o644)

	stdin, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString("s\n"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() {
		os.Stdin = oldStdin
		stdin.Close()
	})

	r := newTestRunner(config.Config{})
	r.Atomic = true
	r.DryRun = false
	r.Command = "sync"
	mod := config.Module{
		Name: moduleName,
		Items: []config.Item{{
			File:        fileName,
			Destination: config.PlatformMap{MacOS: systemDir + string(os.PathSeparator)},
			Direction:   "sync",
		}},
	}

	result := r.ApplyModule(context.Background(), mod)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.Applied != 0 || result.Skipped != 1 || result.RolledBack != 0 {
		t.Fatalf("ModuleResult = %+v, want exactly one skipped item", result)
	}
	entries, err := audit.Read(moduleName, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Outcome != "skipped" {
		t.Fatalf("audit entries = %+v, want one skipped outcome", entries)
	}
	repoContent, err := os.ReadFile(repoFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(repoContent); got != "repo version" {
		t.Fatalf("repo file = %q, want unchanged", got)
	}
	systemContent, err := os.ReadFile(systemFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(systemContent); got != "system version" {
		t.Fatalf("system file = %q, want unchanged", got)
	}
}

// Real modules list the same package once per manager (git via brew, apt, dnf,
// winget, …), so most of them are platform-skipped on any given machine. Their
// audit entries have to stay distinguishable.
func TestApplyModuleSkipAuditDistinguishesSamePackage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("audit.Log resolves its path from HOME only on Unix")
	}
	t.Setenv("HOME", t.TempDir())
	r := newTestRunner(config.Config{})
	// Neither manager belongs to this OS, so both items skip while building —
	// no installed-check runs and the outcome cannot depend on the test host.
	r.OS = "windows"
	var buf bytes.Buffer
	r.Out = &buf
	r.UI = ui.New(&buf, &bytes.Buffer{})

	mod := config.Module{Name: "git", Items: []config.Item{
		{Package: "git", Via: "brew"},
		{Package: "git", Via: "apt"},
	}}
	result := r.ApplyModule(context.Background(), mod)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.Skipped != 2 {
		t.Fatalf("skipped = %d, want 2", result.Skipped)
	}

	entries, err := audit.Read("git", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d audit entries, want 2", len(entries))
	}
	if entries[0].Item == entries[1].Item {
		t.Errorf("both skipped items logged as %q; entries must be distinguishable", entries[0].Item)
	}
	for _, e := range entries {
		if e.Reason != "package not applicable on windows" {
			t.Errorf("Reason = %q", e.Reason)
		}
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

	r.IgnoreTags = true
	if !r.matchesTags(config.Module{OnlyTags: []string{"windows"}}) {
		t.Error("IgnoreTags should bypass a tag mismatch")
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

func TestApplyAllValidationFailureMarksFinalSummaryFailed(t *testing.T) {
	cfg := config.Config{
		Modules: []config.Module{{
			Name:  "invalid",
			Items: []config.Item{{Run: "true", Script: "setup.sh"}},
		}},
	}
	r := newTestRunner(cfg)
	var out bytes.Buffer
	r.Out = &out
	r.UI = ui.New(&out, &bytes.Buffer{})

	if err := r.ApplyAll(context.Background()); err == nil {
		t.Fatal("ApplyAll() error = nil, want validation failure")
	}
	const failedSummary = "[FAIL] 0 applied, 0 skipped, 0 failed"
	if output := out.String(); !strings.Contains(output, failedSummary) {
		t.Fatalf("validation failure summary = %q, want %q", output, failedSummary)
	}
}

func TestApplyAllPanicMarksFinalSummaryFailed(t *testing.T) {
	panicValue := &struct{ message string }{message: "forward panic"}
	action := &lifecycleAction{
		description: "panicking action",
		run: func(context.Context) error {
			panic(panicValue)
		},
	}
	r, out, _ := newLifecycleRunner(t, map[string]actions.Action{"panic": action})
	r.Config = config.Config{Modules: []config.Module{{
		Name:  "panic-summary",
		Items: []config.Item{{Run: "panic"}},
	}}}

	var recovered any
	func() {
		defer func() {
			recovered = recover()
		}()
		_ = r.ApplyAll(context.Background())
	}()

	if recovered != panicValue {
		t.Fatalf("recovered panic = %#v, want identical panic %#v", recovered, panicValue)
	}
	if output := out.String(); !strings.Contains(output, "[FAIL]") {
		t.Fatalf("panic summary = %q, want failure severity", output)
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
	r.UI = ui.New(&buf, &bytes.Buffer{})

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
	if result := r.ApplyModule(context.Background(), mod); result.Err != nil {
		t.Fatal(result.Err)
	}
}

func TestApplyModuleDryRunDoesNotEvaluateSkipIf(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}
	marker := filepath.Join(t.TempDir(), "skip-if-ran")
	t.Setenv("DOTULAR_SKIP_IF_MARKER", marker)
	mod := config.Module{
		Name: "skip-if-dry-run",
		Items: []config.Item{
			{Run: "true", SkipIf: `touch "$DOTULAR_SKIP_IF_MARKER"`},
		},
	}
	r := newTestRunner(config.Config{})
	var output bytes.Buffer
	r.Out = &output
	r.UI = ui.New(&output, &bytes.Buffer{})

	result := r.ApplyModule(context.Background(), mod)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.Applied != 0 || result.Unresolved != 1 {
		t.Fatalf("dry-run result = %+v, want one unresolved item and no applied items", result)
	}
	if got := output.String(); !strings.Contains(got, "skip_if not evaluated") {
		t.Fatalf("dry-run output = %q, want unresolved skip_if notice", got)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("skip_if executed during dry-run; marker stat error = %v", err)
	}
}

func TestApplyModuleDryRunSkipIfDoesNotCheckIdempotency(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	marker := filepath.Join(home, "idempotency-check-ran")
	t.Setenv("DOTULAR_IDEMPOTENCY_MARKER", marker)
	binDir := t.TempDir()
	manager := filepath.Join(binDir, "brew")
	if err := os.WriteFile(manager, []byte("#!/bin/sh\ntouch \"$DOTULAR_IDEMPOTENCY_MARKER\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	mod := config.Module{
		Name: "guarded-package-dry-run",
		Items: []config.Item{
			{Package: "git", Via: "brew", SkipIf: "false"},
		},
	}
	r := newTestRunner(config.Config{})

	result := r.ApplyModule(context.Background(), mod)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.Applied != 0 || result.Skipped != 0 || result.Unresolved != 1 {
		t.Fatalf("dry-run result = %+v, want one unresolved item only", result)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("idempotency check executed during guarded dry-run; marker stat error = %v", err)
	}
	entries, err := audit.Read(mod.Name, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("guarded dry-run wrote audit entries: %+v", entries)
	}
}

func TestApplyModuleDryRunDoesNotWriteSuccessAudit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("audit.Log resolves its path from HOME only on Unix")
	}
	t.Setenv("HOME", t.TempDir())
	mod := config.Module{
		Name:  "dry-run-audit",
		Items: []config.Item{{Run: "true"}},
	}
	r := newTestRunner(config.Config{})

	if result := r.ApplyModule(context.Background(), mod); result.Err != nil {
		t.Fatal(result.Err)
	}
	entries, err := audit.Read("dry-run-audit", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Outcome == "success" {
			t.Fatalf("dry-run wrote a successful apply audit entry: %+v", entry)
		}
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
	r.UI = ui.New(&buf, &bytes.Buffer{})
	if result := r.ApplyModule(context.Background(), mod); result.Err != nil {
		t.Fatal(result.Err)
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
	r.UI = ui.New(&buf, &bytes.Buffer{})
	if result := r.ApplyModule(context.Background(), mod); result.Err != nil {
		t.Fatal(result.Err)
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
	r.UI = ui.New(&buf, &bytes.Buffer{})
	if result := r.ApplyModule(context.Background(), mod); result.Err != nil {
		t.Fatal(result.Err)
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
	r.UI = ui.New(&buf, &bytes.Buffer{})
	if result := r.ApplyModule(context.Background(), mod); result.Err != nil {
		t.Fatal(result.Err)
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
	r.UI = ui.New(&buf, &bytes.Buffer{})
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
	r.UI = ui.New(&buf, &bytes.Buffer{})
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
	r.UI = ui.New(&buf, &bytes.Buffer{})
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
	r.UI = ui.New(&buf, &bytes.Buffer{})
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
	if r.UI == nil {
		t.Error("expected UI to be initialized")
	}
	if r.RollbackTimeout != 2*time.Minute {
		t.Errorf("RollbackTimeout = %v, want 2m", r.RollbackTimeout)
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
	r.UI = ui.New(&buf, &bytes.Buffer{})
	if result := r.ApplyModule(context.Background(), mod); result.Err != nil {
		t.Fatal(result.Err)
	}
	if !containsStr(buf.String(), "skip") {
		t.Error("expected skip output for apt on darwin")
	}
}

func TestApplyModuleSkipsSettingOnUnsupportedOS(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	mod := config.Module{
		Name: "cross-platform",
		Items: []config.Item{
			{Run: "true"},
			{Setting: "com.apple.dock", Key: "autohide", Value: true},
		},
	}
	r := newTestRunner(config.Config{})
	r.OS = "linux"
	r.DryRun = false
	var buf bytes.Buffer
	r.Out = &buf
	r.UI = ui.New(&buf, &bytes.Buffer{})

	result := r.ApplyModule(context.Background(), mod)
	if result.Err != nil {
		t.Fatalf("module should not fail on an inapplicable setting: %v", result.Err)
	}
	if result.Failed != 0 {
		t.Errorf("Failed = %d, want 0", result.Failed)
	}
	if result.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", result.Skipped)
	}
	if result.Applied != 1 {
		t.Errorf("Applied = %d, want 1", result.Applied)
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
	r.UI = ui.New(&buf, &bytes.Buffer{})
	if result := r.ApplyModule(context.Background(), mod); result.Err != nil {
		t.Fatal(result.Err)
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
	r.UI = ui.New(&buf, &bytes.Buffer{})
	if result := r.ApplyModule(context.Background(), mod); result.Err != nil {
		t.Fatal(result.Err)
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
	r.UI = ui.New(&buf, &bytes.Buffer{})
	if result := r.ApplyModule(context.Background(), mod); result.Err != nil {
		t.Fatal(result.Err)
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
	var errBuf bytes.Buffer
	r.Out = &buf
	r.UI = ui.New(&buf, &errBuf)
	result := r.ApplyModule(context.Background(), mod)
	if result.Err == nil {
		t.Error("expected error from failed command")
	}
	if !containsStr(errBuf.String(), "rollback") {
		t.Error("expected rollback message in error output")
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
	r.UI = ui.New(&buf, &bytes.Buffer{})
	if result := r.ApplyModule(context.Background(), mod); result.Err != nil {
		t.Fatal(result.Err)
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
	r.UI = ui.New(&buf, &bytes.Buffer{})
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
	r.UI = ui.New(&buf, &bytes.Buffer{})
	if result := r.ApplyModule(context.Background(), mod); result.Err != nil {
		t.Fatal(result.Err)
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
	r.UI = ui.New(&buf, &bytes.Buffer{})
	if result := r.ApplyModule(context.Background(), mod); result.Err != nil {
		t.Fatal(result.Err)
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

type lifecycleAction struct {
	description string
	writePaths  []string
	prepare     func(context.Context) (actions.CompensationPreparation, error)
	run         func(context.Context) error
}

func (a *lifecycleAction) Describe() string {
	return a.description
}

func (a *lifecycleAction) WritePaths() []string {
	return a.writePaths
}

func (a *lifecycleAction) PrepareCompensation(ctx context.Context) (actions.CompensationPreparation, error) {
	if a.prepare == nil {
		return actions.CompensationPreparation{
			UnavailableReason: "no automatic compensation",
		}, nil
	}
	return a.prepare(ctx)
}

func (a *lifecycleAction) Run(ctx context.Context, _ bool) error {
	if a.run == nil {
		return nil
	}
	return a.run(ctx)
}

type lifecycleCompensation struct {
	description string
	run         func(context.Context) error
}

func (c lifecycleCompensation) Describe() string {
	return c.description
}

func (c lifecycleCompensation) Run(ctx context.Context) error {
	if c.run == nil {
		return nil
	}
	return c.run(ctx)
}

func newLifecycleRunner(
	t *testing.T,
	actionByPrimary map[string]actions.Action,
) (*Runner, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var out, errOut bytes.Buffer
	r := newTestRunner(config.Config{})
	r.DryRun = false
	r.Atomic = true
	r.RollbackTimeout = time.Second
	r.Out = &out
	r.UI = ui.New(&out, &errOut)
	r.actionBuilder = func(item config.Item, _ string) (actions.Action, string, error) {
		action := actionByPrimary[item.PrimaryValue()]
		if action == nil {
			return nil, "", errors.New("missing lifecycle test action")
		}
		return action, "", nil
	}
	return r, &out, &errOut
}

func assertNoSnapshotFiles(t *testing.T, tempRoot string) {
	t.Helper()
	entries, err := os.ReadDir(tempRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("snapshot temp root contains %v, want empty", entries)
	}
}

func TestApplyModuleAuditUsesTransactionalOutcomeVocabulary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("audit.Log resolves its path from HOME only on Unix")
	}
	tests := []struct {
		name    string
		atomic  bool
		outcome string
	}{
		{name: "atomic applied item", atomic: true, outcome: "applied"},
		{name: "non-transactional legacy success", atomic: false, outcome: "success"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			action := &lifecycleAction{
				description: "audit action",
				prepare: func(context.Context) (actions.CompensationPreparation, error) {
					return actions.CompensationPreparation{
						Compensation: lifecycleCompensation{description: "undo audit action"},
					}, nil
				},
			}
			r, _, _ := newLifecycleRunner(t, map[string]actions.Action{"audit-action": action})
			r.Atomic = tt.atomic
			r.Command = "apply"

			result := r.ApplyModule(context.Background(), config.Module{
				Name:  "audit-outcome",
				Items: []config.Item{{Run: "audit-action"}},
			})
			if result.Err != nil {
				t.Fatal(result.Err)
			}
			if result.Applied != 1 {
				t.Fatalf("Applied = %d, want 1", result.Applied)
			}

			entries, err := audit.Read("audit-outcome", 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("audit entries = %d, want 1", len(entries))
			}
			if entries[0].Outcome != tt.outcome {
				t.Errorf("Outcome = %q, want %q", entries[0].Outcome, tt.outcome)
			}
		})
	}
}

func TestApplyModuleAtomicPreflightStopsBeforeEffects(t *testing.T) {
	t.Run("invalid applicable rollback syntax", func(t *testing.T) {
		tempRoot := t.TempDir()
		t.Setenv("TMPDIR", tempRoot)
		actionRan := false
		hookRan := false
		action := &lifecycleAction{
			description: "invalid rollback action",
			run: func(context.Context) error {
				actionRan = true
				return nil
			},
		}
		r, _, _ := newLifecycleRunner(t, map[string]actions.Action{"forward": action})
		r.shellRun = func(context.Context, string) error {
			hookRan = true
			return nil
		}
		mod := config.Module{
			Name: "invalid-preflight",
			Hooks: config.ModuleHooks{
				BeforeApply: ": forward-hook",
				Rollback: config.RollbackHooks{
					BeforeApply: "'unterminated",
				},
			},
			Items: []config.Item{{Run: "forward", Rollback: ": undo-forward"}},
		}

		result := r.ApplyModule(context.Background(), mod)
		if result.Err == nil {
			t.Fatal("ApplyModule() error = nil, want rollback syntax error")
		}
		if actionRan || hookRan {
			t.Fatalf("forward effects ran: action=%v hook=%v", actionRan, hookRan)
		}
		assertNoSnapshotFiles(t, tempRoot)
	})

	t.Run("invalid rollback on skip_if item is ignored", func(t *testing.T) {
		action := &lifecycleAction{
			description: "skipped invalid rollback action",
			run: func(context.Context) error {
				t.Fatal("skipped action ran")
				return nil
			},
		}
		r, _, _ := newLifecycleRunner(t, map[string]actions.Action{"skipped": action})
		mod := config.Module{
			Name: "skip-invalid-rollback",
			Items: []config.Item{{
				Run:      "skipped",
				SkipIf:   "true",
				Rollback: "'unterminated",
			}},
		}

		result := r.ApplyModule(context.Background(), mod)
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		if result.Skipped != 1 {
			t.Fatalf("ModuleResult = %+v, want one skipped item", result)
		}
	})

	t.Run("invalid module sync rollback is ignored when all sync items skip", func(t *testing.T) {
		action := &lifecycleAction{
			description: "skipped sync action",
			run: func(context.Context) error {
				t.Fatal("skipped sync action ran")
				return nil
			},
		}
		r, _, _ := newLifecycleRunner(t, map[string]actions.Action{"sync-file": action})
		r.shellRun = func(context.Context, string) error {
			t.Fatal("sync hook ran without an applicable sync item")
			return nil
		}
		mod := config.Module{
			Name: "skip-invalid-sync-rollback",
			Items: []config.Item{{
				File:        "sync-file",
				Destination: config.PlatformMap{MacOS: "unused"},
				Direction:   "sync",
				SkipIf:      "true",
			}},
			Hooks: config.ModuleHooks{
				BeforeSync: ": should-not-run",
				Rollback: config.RollbackHooks{
					BeforeSync: "'unterminated",
				},
			},
		}

		result := r.ApplyModule(context.Background(), mod)
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		if result.Skipped != 1 {
			t.Fatalf("ModuleResult = %+v, want one skipped sync item", result)
		}
	})

	t.Run("fatal typed capture discards snapshot", func(t *testing.T) {
		tempRoot := t.TempDir()
		t.Setenv("TMPDIR", tempRoot)
		captureErr := errors.New("capture failed")
		actionRan := false
		hookRan := false
		action := &lifecycleAction{
			description: "capture failure action",
			writePaths:  []string{filepath.Join(t.TempDir(), "created", "target")},
			prepare: func(context.Context) (actions.CompensationPreparation, error) {
				return actions.CompensationPreparation{}, captureErr
			},
			run: func(context.Context) error {
				actionRan = true
				return nil
			},
		}
		r, _, _ := newLifecycleRunner(t, map[string]actions.Action{"capture": action})
		r.shellRun = func(context.Context, string) error {
			hookRan = true
			return nil
		}
		mod := config.Module{
			Name:  "capture-preflight",
			Hooks: config.ModuleHooks{BeforeApply: ": before"},
			Items: []config.Item{{Run: "capture", Rollback: ": undo-capture"}},
		}

		result := r.ApplyModule(context.Background(), mod)
		if !errors.Is(result.Err, captureErr) {
			t.Fatalf("ApplyModule() error = %v, want errors.Is(captureErr)", result.Err)
		}
		if actionRan || hookRan {
			t.Fatalf("forward effects ran: action=%v hook=%v", actionRan, hookRan)
		}
		assertNoSnapshotFiles(t, tempRoot)
	})
}

func TestApplyModuleValidatesConfigBeforeEffectsInEveryMode(t *testing.T) {
	for _, atomic := range []bool{false, true} {
		t.Run(map[bool]string{false: "non-atomic", true: "atomic"}[atomic], func(t *testing.T) {
			actionRan := false
			action := &lifecycleAction{
				description: "invalid config action",
				run: func(context.Context) error {
					actionRan = true
					return nil
				},
			}
			r, _, _ := newLifecycleRunner(t, map[string]actions.Action{"source": action})
			r.Atomic = atomic
			mod := config.Module{
				Name: "invalid-config",
				Items: []config.Item{{
					File:        "source",
					Destination: config.PlatformMap{MacOS: "target"},
					Rollback:    ": unsupported",
				}},
			}

			result := r.ApplyModule(context.Background(), mod)
			if result.Err == nil {
				t.Fatal("ApplyModule() error = nil, want strict config validation error")
			}
			if actionRan {
				t.Fatal("action ran after strict config validation failure")
			}
		})
	}
	t.Run("ApplyAll validates later modules before the first effect", func(t *testing.T) {
		actionRan := false
		action := &lifecycleAction{
			description: "must not run",
			run: func(context.Context) error {
				actionRan = true
				return nil
			},
		}
		cfg := config.Config{Modules: []config.Module{
			{Name: "otherwise-valid", Items: []config.Item{{Run: "effect"}}},
			{
				Name: "later-invalid",
				Items: []config.Item{{
					File:        "source",
					Destination: config.PlatformMap{MacOS: "target"},
					Rollback:    ": unsupported",
				}},
			},
		}}
		r, _, _ := newLifecycleRunner(t, map[string]actions.Action{"effect": action})
		r.Config = cfg

		if err := r.ApplyAll(context.Background()); err == nil {
			t.Fatal("ApplyAll() error = nil, want strict config validation error")
		}
		if actionRan {
			t.Fatal("first module action ran before validation of later module")
		}
	})
}

func TestApplyModuleAtomicPreparesAllStateBeforeModuleHook(t *testing.T) {
	tempRoot := t.TempDir()
	t.Setenv("TMPDIR", tempRoot)
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("original"), 0o640); err != nil {
		t.Fatal(err)
	}
	prepared := false
	forwardErr := errors.New("forward failed")
	action := &lifecycleAction{
		description: "prepared action",
		writePaths:  []string{target},
		prepare: func(context.Context) (actions.CompensationPreparation, error) {
			prepared = true
			return actions.CompensationPreparation{
				Compensation: lifecycleCompensation{description: "undo prepared action"},
			}, nil
		},
		run: func(context.Context) error {
			return forwardErr
		},
	}
	r, _, _ := newLifecycleRunner(t, map[string]actions.Action{"prepared": action})
	r.shellRun = func(_ context.Context, command string) error {
		if command == ": before" {
			if !prepared {
				t.Fatal("module before_apply ran before typed capture")
			}
			if err := os.WriteFile(target, []byte("changed by hook"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}
	mod := config.Module{
		Name:  "prepared-before-hook",
		Hooks: config.ModuleHooks{BeforeApply: ": before"},
		Items: []config.Item{{Run: "prepared", Rollback: ": explicit fallback"}},
	}

	result := r.ApplyModule(context.Background(), mod)
	if !errors.Is(result.Err, forwardErr) {
		t.Fatalf("ApplyModule() error = %v, want errors.Is(forwardErr)", result.Err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Fatalf("restored content = %q, want original pre-hook baseline", got)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("restored mode = %04o, want 0640", info.Mode().Perm())
	}
	assertNoSnapshotFiles(t, tempRoot)
}

func TestApplyModuleAtomicHookAndActionRollbackIsStrictLIFO(t *testing.T) {
	var trace []string
	appendTrace := func(value string) {
		trace = append(trace, value)
	}
	action := &lifecycleAction{
		description: "sync action",
		prepare: func(context.Context) (actions.CompensationPreparation, error) {
			return actions.CompensationPreparation{
				Compensation: lifecycleCompensation{
					description: "undo sync action",
					run: func(context.Context) error {
						appendTrace("undo-action")
						return nil
					},
				},
			}, nil
		},
		run: func(context.Context) error {
			appendTrace("action")
			return nil
		},
	}
	r, _, _ := newLifecycleRunner(t, map[string]actions.Action{"sync-file": action})
	afterApplyErr := errors.New("module after_apply failed")
	r.shellRun = func(_ context.Context, command string) error {
		appendTrace(command)
		if command == ": module-after-apply" {
			return afterApplyErr
		}
		return nil
	}
	item := config.Item{
		File:        "sync-file",
		Destination: config.PlatformMap{MacOS: "unused"},
		Direction:   "sync",
		Hooks: config.ItemHooks{
			BeforeApply: ": item-before-apply",
			BeforeSync:  ": item-before-sync",
			AfterSync:   ": item-after-sync",
			AfterApply:  ": item-after-apply",
			Rollback: config.RollbackHooks{
				BeforeApply: ": undo-item-before-apply",
				BeforeSync:  ": undo-item-before-sync",
				AfterSync:   ": undo-item-after-sync",
				AfterApply:  ": undo-item-after-apply",
			},
		},
	}
	mod := config.Module{
		Name:  "hook-lifo",
		Items: []config.Item{item},
		Hooks: config.ModuleHooks{
			BeforeApply: ": module-before-apply",
			BeforeSync:  ": module-before-sync",
			AfterSync:   ": module-after-sync",
			AfterApply:  ": module-after-apply",
			Rollback: config.RollbackHooks{
				BeforeApply: ": undo-module-before-apply",
				BeforeSync:  ": undo-module-before-sync",
				AfterSync:   ": undo-module-after-sync",
				AfterApply:  ": undo-module-after-apply",
			},
		},
	}

	result := r.ApplyModule(context.Background(), mod)
	if !errors.Is(result.Err, afterApplyErr) {
		t.Fatalf("ApplyModule() error = %v, want errors.Is(afterApplyErr)", result.Err)
	}
	wantTail := []string{
		": undo-module-after-apply",
		": undo-module-after-sync",
		": undo-item-after-apply",
		": undo-item-after-sync",
		"undo-action",
		": undo-item-before-sync",
		": undo-item-before-apply",
		": undo-module-before-sync",
		": undo-module-before-apply",
	}
	if len(trace) < len(wantTail) {
		t.Fatalf("trace = %v, want rollback tail %v", trace, wantTail)
	}
	gotTail := trace[len(trace)-len(wantTail):]
	if !reflect.DeepEqual(gotTail, wantTail) {
		t.Fatalf("rollback order = %v, want %v", gotTail, wantTail)
	}
	if result.Applied != 0 || result.RolledBack != 1 ||
		result.RollbackFailed != 0 || result.Uncompensated != 0 {
		t.Fatalf("ModuleResult = %+v, want one rolled-back item outcome", result)
	}
}

func TestApplyModuleAtomicCountsOneRollbackOutcomePerAttemptedFilesystemItem(t *testing.T) {
	first := &lifecycleAction{
		description: "first filesystem item",
		writePaths:  []string{filepath.Join(t.TempDir(), "first")},
	}
	second := &lifecycleAction{
		description: "second filesystem item",
		writePaths:  []string{filepath.Join(t.TempDir(), "second")},
	}
	r, out, _ := newLifecycleRunner(t, map[string]actions.Action{
		"first":  first,
		"second": second,
	})
	forwardErr := errors.New("module after_apply failed")
	r.shellRun = func(_ context.Context, command string) error {
		if command == ": fail-module" {
			return forwardErr
		}
		return nil
	}
	mod := config.Module{
		Name:  "filesystem-item-outcomes",
		Items: []config.Item{{Run: "first"}, {Run: "second"}},
		Hooks: config.ModuleHooks{
			BeforeApply: ": before-module",
			AfterApply:  ": fail-module",
			Rollback: config.RollbackHooks{
				BeforeApply: ": undo-before-module",
				AfterApply:  ": undo-after-module",
			},
		},
	}

	result := r.ApplyModule(context.Background(), mod)
	if !errors.Is(result.Err, forwardErr) {
		t.Fatalf("ApplyModule() error = %v, want errors.Is(forwardErr)", result.Err)
	}
	if result.Applied != 0 || result.RolledBack != 2 ||
		result.RollbackFailed != 0 || result.Uncompensated != 0 {
		t.Fatalf("ModuleResult = %+v, want exactly two rolled-back item outcomes", result)
	}
	if !strings.Contains(out.String(), "2 rolled back, 0 rollback failed, 0 uncompensated") {
		t.Fatalf("module summary = %q, want two rolled-back item outcomes", out.String())
	}
}

func TestApplyModuleAtomicMixedRollbackJournalCountsOnlyItems(t *testing.T) {
	filesystem := &lifecycleAction{
		description: "filesystem item",
		writePaths:  []string{filepath.Join(t.TempDir(), "filesystem")},
	}
	typed := &lifecycleAction{
		description: "typed item",
		prepare: func(context.Context) (actions.CompensationPreparation, error) {
			return actions.CompensationPreparation{
				Compensation: lifecycleCompensation{description: "undo typed item"},
			}, nil
		},
	}
	explicit := &lifecycleAction{description: "explicit item"}
	r, out, _ := newLifecycleRunner(t, map[string]actions.Action{
		"filesystem": filesystem,
		"typed":      typed,
		"explicit":   explicit,
	})
	forwardErr := errors.New("module after_apply failed")
	r.shellRun = func(_ context.Context, command string) error {
		if command == ": fail-module" {
			return forwardErr
		}
		return nil
	}
	mod := config.Module{
		Name: "mixed-item-outcomes",
		Items: []config.Item{
			{Run: "filesystem", Hooks: config.ItemHooks{
				BeforeApply: ": before-filesystem",
				Rollback:    config.RollbackHooks{BeforeApply: ": undo-before-filesystem"},
			}},
			{Run: "typed"},
			{Run: "explicit", Rollback: ": undo-explicit"},
		},
		Hooks: config.ModuleHooks{
			AfterApply: ": fail-module",
			Rollback:   config.RollbackHooks{AfterApply: ": undo-after-module"},
		},
	}

	result := r.ApplyModule(context.Background(), mod)
	if !errors.Is(result.Err, forwardErr) {
		t.Fatalf("ApplyModule() error = %v, want errors.Is(forwardErr)", result.Err)
	}
	outcomeCount := result.RolledBack + result.RollbackFailed + result.Uncompensated
	if result.Applied != 0 || result.RolledBack != 3 ||
		result.RollbackFailed != 0 || result.Uncompensated != 0 || outcomeCount != len(mod.Items) {
		t.Fatalf("ModuleResult = %+v, want one rolled-back outcome per attempted item", result)
	}

	entries, err := audit.Read(mod.Name, 0)
	if err != nil {
		t.Fatal(err)
	}
	var sawHook, sawSnapshot bool
	var rollbackDetails int
	rollbackDetailsByItem := make(map[string]int)
	for _, entry := range entries {
		if entry.Phase != "rollback" {
			continue
		}
		rollbackDetails++
		rollbackDetailsByItem[entry.Item]++
		sawHook = sawHook || strings.Contains(entry.Item, "hook ")
		sawSnapshot = sawSnapshot || strings.Contains(entry.Item, "snapshot restore")
	}
	if !sawHook || !sawSnapshot {
		t.Fatalf("rollback audit details lack hook or snapshot row: %+v", entries)
	}
	if rollbackDetails <= outcomeCount {
		t.Fatalf("rollback detail rows = %d, want more than %d item outcomes", rollbackDetails, outcomeCount)
	}
	for _, item := range []string{
		"filesystem item [action]",
		"typed item [action]",
		"explicit item [action]",
		"filesystem [snapshot restore]",
	} {
		if rollbackDetailsByItem[item] != 1 {
			t.Fatalf("rollback audit detail count for %q = %d, want 1: %+v", item, rollbackDetailsByItem[item], entries)
		}
	}
	if !strings.Contains(out.String(), `[rollback] mixed-item-outcomes item "filesystem item" action: rolled_back`) ||
		!strings.Contains(out.String(), `[rollback] mixed-item-outcomes module "filesystem" snapshot restore: rolled_back`) {
		t.Fatalf("rollback output lacks filesystem item or snapshot detail: %q", out.String())
	}
}

func TestApplyModuleAtomicSnapshotFailureMarksFilesystemItemsRollbackFailed(t *testing.T) {
	parent := t.TempDir()
	firstPath := filepath.Join(parent, "first")
	secondPath := filepath.Join(parent, "second")
	for _, path := range []string{firstPath, secondPath} {
		if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	first := &lifecycleAction{
		description: "first filesystem item",
		writePaths:  []string{firstPath},
	}
	second := &lifecycleAction{
		description: "second filesystem item",
		writePaths:  []string{secondPath},
		run: func(context.Context) error {
			if err := os.RemoveAll(parent); err != nil {
				return err
			}
			return os.WriteFile(parent, []byte("blocks snapshot restore"), 0o600)
		},
	}
	r, out, _ := newLifecycleRunner(t, map[string]actions.Action{
		"first":  first,
		"second": second,
	})
	forwardErr := errors.New("module after_apply failed")
	r.shellRun = func(_ context.Context, command string) error {
		if command == ": fail-module" {
			return forwardErr
		}
		return nil
	}
	mod := config.Module{
		Name:  "filesystem-snapshot-failure-outcomes",
		Items: []config.Item{{Run: "first"}, {Run: "second"}},
		Hooks: config.ModuleHooks{
			AfterApply: ": fail-module",
			Rollback:   config.RollbackHooks{AfterApply: ": undo-after-module"},
		},
	}

	result := r.ApplyModule(context.Background(), mod)
	if !errors.Is(result.Err, forwardErr) {
		t.Fatalf("ApplyModule() error = %v, want errors.Is(forwardErr)", result.Err)
	}
	if result.Applied != 0 || result.RolledBack != 0 ||
		result.RollbackFailed != 2 || result.Uncompensated != 0 {
		t.Fatalf("ModuleResult = %+v, want exactly two rollback-failed filesystem item outcomes", result)
	}
	entries, err := audit.Read(mod.Name, 0)
	if err != nil {
		t.Fatal(err)
	}
	rollbackOutcomes := make(map[string][]string)
	for _, entry := range entries {
		if entry.Phase == "rollback" {
			rollbackOutcomes[entry.Item] = append(rollbackOutcomes[entry.Item], entry.Outcome)
		}
	}
	for _, item := range []string{"first filesystem item [action]", "second filesystem item [action]"} {
		if !reflect.DeepEqual(rollbackOutcomes[item], []string{rollbackOutcomeFailed}) {
			t.Fatalf("rollback audit outcomes for %q = %v, want [%s]", item, rollbackOutcomes[item], rollbackOutcomeFailed)
		}
	}
	if !reflect.DeepEqual(rollbackOutcomes["filesystem [snapshot restore]"], []string{rollbackOutcomeFailed}) {
		t.Fatalf("snapshot rollback audit outcomes = %v, want [%s]", rollbackOutcomes["filesystem [snapshot restore]"], rollbackOutcomeFailed)
	}
	for _, item := range []string{"first filesystem item", "second filesystem item"} {
		if !strings.Contains(out.String(), fmt.Sprintf(`item %q action: rollback_failed`, item)) {
			t.Fatalf("rollback output lacks failed item outcome for %q: %q", item, out.String())
		}
	}
}

func TestApplyModuleAtomicCompensatesFailedActionAttemptAndVerifyFailure(t *testing.T) {
	tests := []struct {
		name      string
		actionErr error
		verify    string
	}{
		{name: "action failure", actionErr: errors.New("action failed")},
		{name: "verify failure", verify: ": verify"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			compensated := false
			action := &lifecycleAction{
				description: "attempted action",
				prepare: func(context.Context) (actions.CompensationPreparation, error) {
					return actions.CompensationPreparation{
						Compensation: lifecycleCompensation{
							description: "undo attempted action",
							run: func(context.Context) error {
								compensated = true
								return nil
							},
						},
					}, nil
				},
				run: func(context.Context) error {
					return test.actionErr
				},
			}
			r, _, _ := newLifecycleRunner(t, map[string]actions.Action{"attempt": action})
			verifyErr := errors.New("verify failed")
			r.shellRun = func(_ context.Context, command string) error {
				if command == test.verify && test.verify != "" {
					return verifyErr
				}
				return nil
			}
			mod := config.Module{
				Name:  "failed-attempt-" + test.name,
				Items: []config.Item{{Run: "attempt", Verify: test.verify}},
			}

			result := r.ApplyModule(context.Background(), mod)
			if result.Err == nil {
				t.Fatal("ApplyModule() error = nil, want forward error")
			}
			if !compensated {
				t.Fatal("failed attempted action was not compensated")
			}
			if result.Applied != 0 || result.Failed != 0 || result.RolledBack != 1 {
				t.Fatalf("ModuleResult = %+v, want only one rolled-back final item outcome", result)
			}
		})
	}
}

func TestApplyModuleAtomicItemHookFailureBeforeAttemptDoesNotCountFailedItem(t *testing.T) {
	actionRan := false
	action := &lifecycleAction{
		description: "not attempted",
		run: func(context.Context) error {
			actionRan = true
			return nil
		},
	}
	r, _, _ := newLifecycleRunner(t, map[string]actions.Action{"not-attempted": action})
	hookErr := errors.New("before_apply failed")
	r.shellRun = func(_ context.Context, command string) error {
		if command == ": fail-before" {
			return hookErr
		}
		return nil
	}
	mod := config.Module{
		Name: "item-hook-before-attempt",
		Items: []config.Item{{
			Run: "not-attempted",
			Hooks: config.ItemHooks{
				BeforeApply: ": fail-before",
				Rollback:    config.RollbackHooks{BeforeApply: ": undo-before"},
			},
		}},
	}

	result := r.ApplyModule(context.Background(), mod)
	if !errors.Is(result.Err, hookErr) {
		t.Fatalf("ApplyModule() error = %v, want errors.Is(hookErr)", result.Err)
	}
	if actionRan {
		t.Fatal("action ran after its before_apply hook failed")
	}
	if result.Failed != 0 || result.RolledBack != 0 ||
		result.RollbackFailed != 0 || result.Uncompensated != 0 {
		t.Fatalf("ModuleResult = %+v, want no final item outcome before action attempt", result)
	}
}

func TestApplyModuleAtomicUsesTypedCompensationBeforeExplicitFallback(t *testing.T) {
	var trace []string
	typed := &lifecycleAction{
		description: "typed",
		prepare: func(context.Context) (actions.CompensationPreparation, error) {
			return actions.CompensationPreparation{
				Compensation: lifecycleCompensation{
					description: "typed rollback",
					run: func(context.Context) error {
						trace = append(trace, "typed")
						return nil
					},
				},
			}, nil
		},
	}
	fallback := &lifecycleAction{
		description: "fallback",
		prepare: func(context.Context) (actions.CompensationPreparation, error) {
			return actions.CompensationPreparation{UnavailableReason: "capture unavailable"}, nil
		},
	}
	failureErr := errors.New("stop")
	failure := &lifecycleAction{
		description: "failure",
		run: func(context.Context) error {
			return failureErr
		},
	}
	r, _, errOut := newLifecycleRunner(t, map[string]actions.Action{
		"typed":    typed,
		"fallback": fallback,
		"failure":  failure,
	})
	warningSeenBeforeExecution := false
	failure.run = func(context.Context) error {
		warningSeenBeforeExecution = containsStr(errOut.String(), "no automatic compensation")
		return failureErr
	}
	r.shellRun = func(_ context.Context, command string) error {
		trace = append(trace, command)
		return nil
	}
	mod := config.Module{
		Name: "compensation-precedence",
		Items: []config.Item{
			{Run: "typed", Rollback: ": explicit-typed"},
			{Run: "fallback", Rollback: ": explicit-fallback"},
			{Run: "failure"},
		},
	}

	result := r.ApplyModule(context.Background(), mod)
	if !errors.Is(result.Err, failureErr) {
		t.Fatalf("ApplyModule() error = %v, want errors.Is(failureErr)", result.Err)
	}
	if strings.Contains(strings.Join(trace, "\n"), "explicit-typed") {
		t.Fatalf("typed compensation incorrectly ran explicit fallback: %v", trace)
	}
	if !containsStr(strings.Join(trace, "\n"), "explicit-fallback") {
		t.Fatalf("unavailable typed capture did not run explicit fallback: %v", trace)
	}
	if result.RolledBack != 2 || result.Uncompensated != 1 {
		t.Fatalf("ModuleResult = %+v, want typed and fallback rolled back plus one uncompensated item", result)
	}
	if !containsStr(errOut.String(), "no automatic compensation") {
		t.Fatalf("warning output = %q, want unavailable warning before execution", errOut.String())
	}
	if !warningSeenBeforeExecution {
		t.Fatal("unavailable compensation warning was not emitted before action execution")
	}
}

func TestApplyModuleAtomicRollbackFailuresContinueAndAuditTruthfully(t *testing.T) {
	rollbackErr := errors.New("explicit rollback failed")
	var trace []string
	succeeded := &lifecycleAction{
		description: "typed success",
		prepare: func(context.Context) (actions.CompensationPreparation, error) {
			return actions.CompensationPreparation{
				Compensation: lifecycleCompensation{
					description: "typed success rollback",
					run: func(context.Context) error {
						trace = append(trace, "typed-success")
						return nil
					},
				},
			}, nil
		},
	}
	fallbackFailure := &lifecycleAction{
		description: "fallback failure",
		prepare: func(context.Context) (actions.CompensationPreparation, error) {
			return actions.CompensationPreparation{UnavailableReason: "typed unavailable"}, nil
		},
	}
	forwardErr := errors.New("forward failure")
	uncompensatedFailure := &lifecycleAction{
		description: "uncompensated failure",
		run: func(context.Context) error {
			return forwardErr
		},
	}
	r, _, _ := newLifecycleRunner(t, map[string]actions.Action{
		"success":            succeeded,
		"fallback-failure":   fallbackFailure,
		"uncompensated-stop": uncompensatedFailure,
	})
	r.shellRun = func(_ context.Context, command string) error {
		trace = append(trace, command)
		if command == ": failing-fallback" {
			return rollbackErr
		}
		return nil
	}
	mod := config.Module{
		Name: "truthful-rollback-report",
		Items: []config.Item{
			{Run: "success"},
			{Run: "fallback-failure", Rollback: ": failing-fallback"},
			{Run: "uncompensated-stop"},
		},
	}

	result := r.ApplyModule(context.Background(), mod)
	if !errors.Is(result.Err, forwardErr) || !errors.Is(result.Err, rollbackErr) {
		t.Fatalf("ApplyModule() error = %v, want joined forward and rollback errors", result.Err)
	}
	if result.Applied != 0 || result.RolledBack != 1 ||
		result.RollbackFailed != 1 || result.Uncompensated != 1 {
		t.Fatalf("ModuleResult = %+v, want one outcome per attempted item", result)
	}
	if !reflect.DeepEqual(trace, []string{": failing-fallback", "typed-success"}) {
		t.Fatalf("rollback trace = %v, want failure then continued typed compensation", trace)
	}
	entries, err := audit.Read(mod.Name, 0)
	if err != nil {
		t.Fatal(err)
	}
	var rollbackEntries []audit.Entry
	for _, entry := range entries {
		if entry.Phase == "rollback" {
			rollbackEntries = append(rollbackEntries, entry)
		}
	}
	var outcomes []string
	for _, entry := range rollbackEntries {
		outcomes = append(outcomes, entry.Outcome)
		if entry.Scope == "" || entry.Item == "" {
			t.Fatalf("rollback audit entry lacks identity: %+v", entry)
		}
	}
	wantOutcomes := []string{"uncompensated", "rollback_failed", "rolled_back", "rolled_back"}
	if !reflect.DeepEqual(outcomes, wantOutcomes) {
		t.Fatalf("rollback audit outcomes = %v, want %v", outcomes, wantOutcomes)
	}
}

type delayedCancellationContext struct {
	context.Context
	suppressErrors int
}

func (c *delayedCancellationContext) Err() error {
	err := c.Context.Err()
	if err != nil && c.suppressErrors > 0 {
		c.suppressErrors--
		return nil
	}
	return err
}

func TestApplyModuleAtomicRollsBackCancellationBeforeCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	delayedCtx := &delayedCancellationContext{Context: ctx, suppressErrors: 1}
	compensated := false
	action := &lifecycleAction{
		description: "cancel before commit",
		prepare: func(context.Context) (actions.CompensationPreparation, error) {
			return actions.CompensationPreparation{
				Compensation: lifecycleCompensation{
					description: "undo canceled action",
					run: func(context.Context) error {
						compensated = true
						return nil
					},
				},
			}, nil
		},
		run: func(context.Context) error {
			cancel()
			return nil
		},
	}
	r, _, _ := newLifecycleRunner(t, map[string]actions.Action{"cancel-before-commit": action})
	mod := config.Module{Name: "cancel-before-commit", Items: []config.Item{{Run: "cancel-before-commit"}}}

	result := r.ApplyModule(delayedCtx, mod)
	if !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("ApplyModule() error = %v, want context.Canceled", result.Err)
	}
	if !compensated {
		t.Fatal("cancellation committed without compensation")
	}
	if result.Applied != 0 || result.RolledBack != 1 {
		t.Fatalf("ModuleResult = %+v, want one rolled-back item", result)
	}
}

func TestApplyModuleAtomicRollbackUsesFreshContext(t *testing.T) {
	type contextKey string
	const key contextKey = "rollback-value"
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key, "kept"))
	compensated := false
	action := &lifecycleAction{
		description: "cancel forward",
		prepare: func(context.Context) (actions.CompensationPreparation, error) {
			return actions.CompensationPreparation{
				Compensation: lifecycleCompensation{
					description: "fresh cleanup",
					run: func(cleanupCtx context.Context) error {
						if cleanupCtx.Err() != nil {
							t.Fatalf("cleanup context already canceled: %v", cleanupCtx.Err())
						}
						if cleanupCtx.Value(key) != "kept" {
							t.Fatalf("cleanup context lost caller value: %v", cleanupCtx.Value(key))
						}
						compensated = true
						return nil
					},
				},
			}, nil
		},
		run: func(context.Context) error {
			cancel()
			return context.Canceled
		},
	}
	r, _, _ := newLifecycleRunner(t, map[string]actions.Action{"cancel": action})
	mod := config.Module{Name: "fresh-context", Items: []config.Item{{Run: "cancel"}}}

	result := r.ApplyModule(ctx, mod)
	if !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("ApplyModule() error = %v, want context.Canceled", result.Err)
	}
	if !compensated {
		t.Fatal("compensation did not run on a fresh cleanup context")
	}
}

func TestApplyModuleNoAtomicBypassesRollbackPreparation(t *testing.T) {
	prepared := false
	compensated := false
	actionErr := errors.New("non-atomic failure")
	action := &lifecycleAction{
		description: "non-atomic action",
		prepare: func(context.Context) (actions.CompensationPreparation, error) {
			prepared = true
			return actions.CompensationPreparation{
				Compensation: lifecycleCompensation{
					description: "must not run",
					run: func(context.Context) error {
						compensated = true
						return nil
					},
				},
			}, nil
		},
		run: func(context.Context) error {
			return actionErr
		},
	}
	r, _, errOut := newLifecycleRunner(t, map[string]actions.Action{"non-atomic": action})
	r.Atomic = false
	mod := config.Module{
		Name:  "no-atomic",
		Items: []config.Item{{Run: "non-atomic", Rollback: "'invalid syntax"}},
	}

	result := r.ApplyModule(context.Background(), mod)
	if !errors.Is(result.Err, actionErr) {
		t.Fatalf("ApplyModule() error = %v, want action error without rollback validation", result.Err)
	}
	if result.Failed != 1 {
		t.Fatalf("non-atomic ModuleResult Failed = %d, want 1", result.Failed)
	}
	if prepared || compensated {
		t.Fatalf("non-atomic rollback state used: prepared=%v compensated=%v", prepared, compensated)
	}
	if result.RolledBack != 0 || result.RollbackFailed != 0 || result.Uncompensated != 0 {
		t.Fatalf("non-atomic ModuleResult has rollback counts: %+v", result)
	}
	if containsStr(errOut.String(), "rollback") || containsStr(errOut.String(), "compensation") {
		t.Fatalf("non-atomic warning output = %q, want no rollback warning", errOut.String())
	}
}

func TestApplyModuleAtomicPanicUnwindsAndRepanicsSameValue(t *testing.T) {
	tempRoot := t.TempDir()
	t.Setenv("TMPDIR", tempRoot)
	panicValue := &struct{ message string }{message: "identical panic"}
	compensated := false
	action := &lifecycleAction{
		description: "panic action",
		prepare: func(context.Context) (actions.CompensationPreparation, error) {
			return actions.CompensationPreparation{
				Compensation: lifecycleCompensation{
					description: "undo panic action",
					run: func(context.Context) error {
						compensated = true
						return nil
					},
				},
			}, nil
		},
		run: func(context.Context) error {
			panic(panicValue)
		},
	}
	r, _, _ := newLifecycleRunner(t, map[string]actions.Action{"panic": action})
	mod := config.Module{Name: "panic-unwind", Items: []config.Item{{Run: "panic"}}}

	var recovered any
	func() {
		defer func() {
			recovered = recover()
		}()
		r.ApplyModule(context.Background(), mod)
	}()
	if recovered != panicValue {
		t.Fatalf("recovered panic = %#v, want identical %#v", recovered, panicValue)
	}
	if !compensated {
		t.Fatal("panic did not unwind active action")
	}
	assertNoSnapshotFiles(t, tempRoot)
}

func TestApplyModuleAtomicCapturesPackageAndSettingBeforeModuleHook(t *testing.T) {
	tests := []struct {
		name string
		item config.Item
	}{
		{name: "package", item: config.Item{Package: "pkg", Via: "brew"}},
		{name: "setting", item: config.Item{Setting: "domain", Key: "key", Value: "value"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prepared := false
			actionRan := false
			action := &lifecycleAction{
				description: test.name + " action",
				prepare: func(context.Context) (actions.CompensationPreparation, error) {
					prepared = true
					return actions.CompensationPreparation{AlreadyApplied: true}, nil
				},
				run: func(context.Context) error {
					actionRan = true
					return nil
				},
			}
			r, _, _ := newLifecycleRunner(t, map[string]actions.Action{
				test.item.PrimaryValue(): action,
			})
			r.shellRun = func(context.Context, string) error {
				if !prepared {
					t.Fatalf("module hook ran before %s state capture", test.name)
				}
				return nil
			}
			mod := config.Module{
				Name:  "capture-" + test.name,
				Hooks: config.ModuleHooks{BeforeApply: ": before"},
				Items: []config.Item{test.item},
			}

			result := r.ApplyModule(context.Background(), mod)
			if result.Err != nil {
				t.Fatal(result.Err)
			}
			if actionRan {
				t.Fatalf("conclusively present %s action ran", test.name)
			}
			if result.Skipped != 1 {
				t.Fatalf("ModuleResult = %+v, want one already-applied skip", result)
			}
		})
	}
}

func TestApplyModuleAtomicCommitsOnlyAfterModuleAfterApply(t *testing.T) {
	tempRoot := t.TempDir()
	t.Setenv("TMPDIR", tempRoot)
	compensationRan := false
	action := &lifecycleAction{
		description: "successful action",
		prepare: func(context.Context) (actions.CompensationPreparation, error) {
			return actions.CompensationPreparation{
				Compensation: lifecycleCompensation{
					description: "unused compensation",
					run: func(context.Context) error {
						compensationRan = true
						return nil
					},
				},
			}, nil
		},
	}
	r, _, _ := newLifecycleRunner(t, map[string]actions.Action{"success": action})
	afterApplySawSnapshot := false
	r.shellRun = func(_ context.Context, command string) error {
		if command == ": after" {
			entries, err := os.ReadDir(tempRoot)
			if err != nil {
				t.Fatal(err)
			}
			afterApplySawSnapshot = len(entries) == 1
		}
		return nil
	}
	mod := config.Module{
		Name:  "commit-after-hook",
		Items: []config.Item{{Run: "success"}},
		Hooks: config.ModuleHooks{AfterApply: ": after"},
	}

	result := r.ApplyModule(context.Background(), mod)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if !afterApplySawSnapshot {
		t.Fatal("snapshot was discarded before module after_apply")
	}
	if compensationRan {
		t.Fatal("successful transaction ran compensation")
	}
	assertNoSnapshotFiles(t, tempRoot)
}

type uncompensatedIdempotentAction struct {
	*lifecycleAction
}

func (*uncompensatedIdempotentAction) IsApplied(context.Context) (bool, error) {
	return false, nil
}

type liveIdempotentAction struct {
	description string
	isApplied   func(context.Context) (bool, error)
	run         func(context.Context) error
}

func (a *liveIdempotentAction) Describe() string {
	return a.description
}

func (a *liveIdempotentAction) IsApplied(ctx context.Context) (bool, error) {
	return a.isApplied(ctx)
}

func (a *liveIdempotentAction) Run(ctx context.Context, _ bool) error {
	return a.run(ctx)
}

type recordingSnapshotRecorder struct {
	paths []string
}

func (r *recordingSnapshotRecorder) Record(path string) error {
	r.paths = append(r.paths, path)
	return nil
}

func TestApplyModuleAtomicEvaluatesSkipIfAtOriginalItemPosition(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "earlier-item-finished")
	t.Setenv("DOTULAR_LIVE_MARKER", marker)
	laterRan := false
	earlier := &lifecycleAction{
		description: "create live skip state",
		run: func(context.Context) error {
			return os.WriteFile(marker, []byte("ready"), 0o600)
		},
	}
	later := &lifecycleAction{
		description: "live skip_if action",
		run: func(context.Context) error {
			laterRan = true
			return nil
		},
	}
	r, _, _ := newLifecycleRunner(t, map[string]actions.Action{
		"earlier": earlier,
		"later":   later,
	})
	mod := config.Module{
		Name: "live-skip-if",
		Items: []config.Item{
			{Run: "earlier"},
			{Run: "later", SkipIf: `test -f "$DOTULAR_LIVE_MARKER"`},
		},
	}

	result := r.ApplyModule(context.Background(), mod)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if laterRan {
		t.Fatal("skip_if was evaluated before the earlier item established its live state")
	}
	if result.Applied != 1 || result.Skipped != 1 {
		t.Fatalf("ModuleResult = %+v, want one applied and one live skip", result)
	}
}

func TestApplyModuleAtomicEvaluatesSkipIfBeforeSnapshottingItem(t *testing.T) {
	action := &lifecycleAction{
		description: "skipped snapshot path",
		writePaths:  []string{filepath.Join(t.TempDir(), "destination")},
		run: func(context.Context) error {
			t.Fatal("skipped action ran")
			return nil
		},
	}
	r, _, _ := newLifecycleRunner(t, map[string]actions.Action{
		"skip-snapshot": action,
	})
	mod := config.Module{
		Name: "skip-before-snapshot",
		Items: []config.Item{{
			Run:    "skip-snapshot",
			SkipIf: "true",
		}},
	}
	prepared, err := r.prepareAtomicModule(context.Background(), mod)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &recordingSnapshotRecorder{}

	if err := r.capturePreparedModule(context.Background(), mod, &prepared, recorder); err != nil {
		t.Fatal(err)
	}
	if len(recorder.paths) != 0 {
		t.Fatalf("recorded snapshot paths = %q, want none", recorder.paths)
	}
	if got := prepared.items[0].skipReason; got != "skip_if" {
		t.Fatalf("skip reason = %q, want skip_if", got)
	}
}

func TestApplyModuleAtomicWarnsForUncompensatedItemBeforeModuleHook(t *testing.T) {
	action := &uncompensatedIdempotentAction{
		lifecycleAction: &lifecycleAction{description: "later uncompensated item"},
	}
	r, _, errOut := newLifecycleRunner(t, map[string]actions.Action{
		"uncompensated": action,
	})
	const itemWarning = `[rollback] item "later uncompensated item" will be uncompensated`
	r.shellRun = func(_ context.Context, command string) error {
		if command == ": before-module" && !strings.Contains(errOut.String(), itemWarning) {
			t.Fatalf("module hook ran before item warning: %q", errOut.String())
		}
		return nil
	}
	mod := config.Module{
		Name:  "warn-before-module-hook",
		Items: []config.Item{{Run: "uncompensated"}},
		Hooks: config.ModuleHooks{
			BeforeApply: ": before-module",
			Rollback:    config.RollbackHooks{BeforeApply: ": undo-before-module"},
		},
	}

	result := r.ApplyModule(context.Background(), mod)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
}

func TestApplyModuleAtomicChecksIdempotencyAtOriginalItemPosition(t *testing.T) {
	stateEstablished := false
	idempotencyChecks := 0
	laterRan := false
	earlier := &lifecycleAction{
		description: "establish idempotent state",
		run: func(context.Context) error {
			stateEstablished = true
			return nil
		},
	}
	later := &liveIdempotentAction{
		description: "live idempotent action",
		isApplied: func(context.Context) (bool, error) {
			idempotencyChecks++
			return stateEstablished, nil
		},
		run: func(context.Context) error {
			laterRan = true
			return nil
		},
	}
	r, _, _ := newLifecycleRunner(t, map[string]actions.Action{
		"earlier-idempotent": earlier,
		"later-idempotent":   later,
	})
	mod := config.Module{
		Name: "live-idempotency",
		Items: []config.Item{
			{Run: "earlier-idempotent"},
			{Run: "later-idempotent"},
		},
	}

	result := r.ApplyModule(context.Background(), mod)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if laterRan {
		t.Fatal("idempotency was checked before the earlier item established live state")
	}
	if idempotencyChecks != 1 {
		t.Fatalf("idempotency checks = %d, want one live check", idempotencyChecks)
	}
	if result.Applied != 1 || result.Skipped != 1 {
		t.Fatalf("ModuleResult = %+v, want one applied and one already-applied skip", result)
	}
}

func TestApplyModuleAtomicPreservesForwardPanicWhenRollbackPanics(t *testing.T) {
	tempRoot := t.TempDir()
	t.Setenv("TMPDIR", tempRoot)
	forwardPanic := &struct{ message string }{message: "forward panic"}
	rollbackPanic := errors.New("rollback panic")
	earlierCompensated := false
	earlier := &lifecycleAction{
		description: "earlier action",
		prepare: func(context.Context) (actions.CompensationPreparation, error) {
			return actions.CompensationPreparation{
				Compensation: lifecycleCompensation{
					description: "earlier compensation",
					run: func(context.Context) error {
						earlierCompensated = true
						return nil
					},
				},
			}, nil
		},
	}
	panicking := &lifecycleAction{
		description: "panicking action",
		prepare: func(context.Context) (actions.CompensationPreparation, error) {
			return actions.CompensationPreparation{
				Compensation: lifecycleCompensation{
					description: "panicking compensation",
					run: func(context.Context) error {
						panic(rollbackPanic)
					},
				},
			}, nil
		},
		run: func(context.Context) error {
			panic(forwardPanic)
		},
	}
	r, _, _ := newLifecycleRunner(t, map[string]actions.Action{
		"earlier-panic": earlier,
		"forward-panic": panicking,
	})
	mod := config.Module{
		Name: "preserve-forward-panic",
		Items: []config.Item{
			{Run: "earlier-panic"},
			{Run: "forward-panic"},
		},
	}

	var recovered any
	func() {
		defer func() {
			recovered = recover()
		}()
		r.ApplyModule(context.Background(), mod)
	}()
	if recovered != forwardPanic {
		t.Fatalf("recovered panic = %#v, want identical forward panic %#v", recovered, forwardPanic)
	}
	if !earlierCompensated {
		t.Fatal("rollback stopped after a compensation panic")
	}
	assertNoSnapshotFiles(t, tempRoot)
}

type preparingIdempotentAction struct {
	description string
	prepare     func(context.Context) (actions.CompensationPreparation, error)
	isApplied   func(context.Context) (bool, error)
	run         func(context.Context) error
}

func (a *preparingIdempotentAction) Describe() string {
	return a.description
}

func (a *preparingIdempotentAction) PrepareCompensation(ctx context.Context) (actions.CompensationPreparation, error) {
	return a.prepare(ctx)
}

func (a *preparingIdempotentAction) IsApplied(ctx context.Context) (bool, error) {
	return a.isApplied(ctx)
}

func (a *preparingIdempotentAction) Run(ctx context.Context, _ bool) error {
	return a.run(ctx)
}

func TestApplyModuleAtomicSkipsConclusivePreparedPresenceWithoutLiveCheck(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "skip-if-ran")
	t.Setenv("DOTULAR_SKIP_IF_MARKER", marker)
	idempotencyChecks := 0
	actionRan := false
	action := &preparingIdempotentAction{
		description: "conclusively present package",
		prepare: func(context.Context) (actions.CompensationPreparation, error) {
			return actions.CompensationPreparation{AlreadyApplied: true}, nil
		},
		isApplied: func(context.Context) (bool, error) {
			idempotencyChecks++
			return false, nil
		},
		run: func(context.Context) error {
			actionRan = true
			return nil
		},
	}
	r, _, _ := newLifecycleRunner(t, map[string]actions.Action{"present-package": action})
	mod := config.Module{
		Name: "conclusive-prepared-presence",
		Items: []config.Item{{
			Run:    "present-package",
			SkipIf: `touch "$DOTULAR_SKIP_IF_MARKER"; false`,
		}},
	}

	result := r.ApplyModule(context.Background(), mod)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("skip_if did not run before conclusive prepared skip: %v", err)
	}
	if idempotencyChecks != 0 {
		t.Fatalf("live idempotency checks = %d, want none after conclusive prepared presence", idempotencyChecks)
	}
	if actionRan {
		t.Fatal("conclusively present prepared action ran")
	}
	if result.Applied != 0 || result.Skipped != 1 {
		t.Fatalf("ModuleResult = %+v, want one conclusive prepared skip", result)
	}
}

func TestApplyModuleAtomicLiveChecksPreparingIdempotentBeforeActivation(t *testing.T) {
	stateEstablished := false
	prepared := false
	idempotencyChecks := 0
	laterRan := false
	laterCompensated := false
	earlier := &lifecycleAction{
		description: "establish package-like state",
		run: func(context.Context) error {
			stateEstablished = true
			return nil
		},
	}
	later := &preparingIdempotentAction{
		description: "package-like preparing idempotent",
		prepare: func(context.Context) (actions.CompensationPreparation, error) {
			prepared = true
			return actions.CompensationPreparation{
				Compensation: lifecycleCompensation{
					description: "prepared package-like compensation",
					run: func(context.Context) error {
						laterCompensated = true
						return nil
					},
				},
			}, nil
		},
		isApplied: func(context.Context) (bool, error) {
			idempotencyChecks++
			if !prepared {
				t.Fatal("live idempotency ran before compensation preflight")
			}
			return stateEstablished, nil
		},
		run: func(context.Context) error {
			laterRan = true
			return nil
		},
	}
	stopErr := errors.New("stop after live skip")
	stop := &lifecycleAction{
		description: "later failure",
		run: func(context.Context) error {
			return stopErr
		},
	}
	r, _, _ := newLifecycleRunner(t, map[string]actions.Action{
		"establish-state": earlier,
		"package-like":    later,
		"stop-live-skip":  stop,
	})
	mod := config.Module{
		Name: "preparing-live-idempotency",
		Items: []config.Item{
			{Run: "establish-state"},
			{Run: "package-like"},
			{Run: "stop-live-skip"},
		},
	}

	result := r.ApplyModule(context.Background(), mod)
	if !errors.Is(result.Err, stopErr) {
		t.Fatalf("ApplyModule() error = %v, want stop error", result.Err)
	}
	if idempotencyChecks != 1 {
		t.Fatalf("idempotency checks = %d, want one live check", idempotencyChecks)
	}
	if laterRan {
		t.Fatal("present preparing idempotent action ran")
	}
	if laterCompensated {
		t.Fatal("prepared compensation activated for a live already-applied skip")
	}
	if result.Skipped != 1 {
		t.Fatalf("ModuleResult = %+v, want one live already-applied skip", result)
	}
}

func TestApplyModuleAtomicWarnsConservativelyForLiveIdempotencyChecks(t *testing.T) {
	tests := []struct {
		name        string
		item        config.Item
		action      actions.Action
		wantWarning bool
	}{
		{
			name: "skip_if",
			item: config.Item{
				Run:    "skip-if-warning",
				SkipIf: "true",
				Hooks:  config.ItemHooks{BeforeApply: ": should-not-run"},
			},
			action: &lifecycleAction{
				description: "skip_if uncompensated warning target",
				run: func(context.Context) error {
					t.Fatal("skip_if action ran")
					return nil
				},
			},
		},
		{
			name: "preparing idempotent",
			item: config.Item{
				Run:   "idempotent-warning",
				Hooks: config.ItemHooks{BeforeApply: ": should-not-run"},
			},
			action: &preparingIdempotentAction{
				description: "idempotent uncompensated warning target",
				prepare: func(context.Context) (actions.CompensationPreparation, error) {
					return actions.CompensationPreparation{
						UnavailableReason: "capture unavailable",
					}, nil
				},
				isApplied: func(context.Context) (bool, error) {
					return true, nil
				},
				run: func(context.Context) error {
					t.Fatal("already-applied action ran")
					return nil
				},
			},
			wantWarning: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r, _, errOut := newLifecycleRunner(t, map[string]actions.Action{
				test.item.PrimaryValue(): test.action,
			})
			r.shellRun = func(context.Context, string) error {
				t.Fatal("hook for skipped item ran")
				return nil
			}
			mod := config.Module{
				Name:  "no-warning-" + test.name,
				Items: []config.Item{test.item},
			}

			result := r.ApplyModule(context.Background(), mod)
			if result.Err != nil {
				t.Fatal(result.Err)
			}
			if result.Skipped != 1 {
				t.Fatalf("ModuleResult = %+v, want one live skip", result)
			}
			if gotWarning := errOut.Len() != 0; gotWarning != test.wantWarning {
				t.Fatalf("warning present = %t, want %t; output %q", gotWarning, test.wantWarning, errOut.String())
			}
		})
	}
}
