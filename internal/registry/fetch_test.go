package registry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atomikpanda/dotular/internal/config"
	"github.com/atomikpanda/dotular/internal/testutil"
	"github.com/atomikpanda/dotular/internal/ui"
)

// The registry cache lives under the home directory, so the whole suite needs a
// home of its own or it would read and overwrite the developer's real cache.
func TestMain(m *testing.M) {
	os.Exit(testutil.IsolateHome(m))
}

func TestModuleCachePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(home, ".cache", "dotular", "registry")

	type testCase struct {
		name     string
		ref      string
		filename string
	}
	tests := []testCase{
		{
			name:     "ordinary ref retains its cache name",
			ref:      "github.com/atomikpanda/dotular/modules/neovim@main",
			filename: "github_com_atomikpanda_dotular_modules_neovim_main.yaml",
		},
		{
			name:     "query ref is sanitized",
			ref:      "github.com/atomikpanda/dotular@main?raw=true",
			filename: "github_com_atomikpanda_dotular_main_raw=true.yaml",
		},
		{
			name:     "backslash is sanitized",
			ref:      `github.com\atomikpanda\dotular\modules\neovim@main`,
			filename: "github_com_atomikpanda_dotular_modules_neovim_main.yaml",
		},
		{
			name:     "mixed traversal separators are sanitized",
			ref:      `github.com/atomikpanda/dotular\..\..\neovim@main`,
			filename: "github_com_atomikpanda_dotular_______neovim_main.yaml",
		},
		{
			name:     "non-reserved device prefix retains its cache name",
			ref:      "com0",
			filename: "com0.yaml",
		},
		{
			name:     "non-reserved superscript device prefix retains its cache name",
			ref:      "CoM⁴",
			filename: "CoM⁴.yaml",
		},
	}

	for _, invalid := range `<>:"/\|?*` {
		tests = append(tests, testCase{
			name:     fmt.Sprintf("Windows-invalid character U+%04X is sanitized", invalid),
			ref:      "ref" + string(invalid) + "name",
			filename: "ref_name.yaml",
		})
	}
	for invalid := rune(0); invalid < ' '; invalid++ {
		tests = append(tests, testCase{
			name:     fmt.Sprintf("control character U+%04X is sanitized", invalid),
			ref:      "ref" + string(invalid) + "name",
			filename: "ref_name.yaml",
		})
	}

	reserved := []string{
		"con", "PRN", "Aux", "nUl",
		"CoM¹", "cOm²", "Com³",
		"lPt¹", "LpT²", "lPT³",
	}
	for i := 1; i <= 9; i++ {
		reserved = append(reserved, fmt.Sprintf("CoM%d", i), fmt.Sprintf("lPt%d", i))
	}
	for _, ref := range reserved {
		tests = append(tests, testCase{
			name:     "reserved device name " + ref + " is made safe",
			ref:      ref,
			filename: ref + "_.yaml",
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := moduleCachePath(tt.ref)
			if dir := filepath.Dir(got); dir != cacheRoot {
				t.Errorf("cache directory = %q, want %q", dir, cacheRoot)
			}
			if filename := filepath.Base(got); filename != tt.filename {
				t.Errorf("cache filename = %q, want %q", filename, tt.filename)
			}
			relative, err := filepath.Rel(cacheRoot, got)
			if err != nil {
				t.Fatal(err)
			}
			if relative != filepath.Base(got) {
				t.Errorf("cache path does not name one file beneath root: %q", got)
			}
		})
	}
}

func TestCachedRefs(t *testing.T) {
	lock := &LockFile{
		Registry: map[string]LockEntry{
			"ref1": {},
			"ref2": {},
		},
	}
	refs := CachedRefs(lock)
	if len(refs) != 2 {
		t.Errorf("expected 2 refs, got %d", len(refs))
	}
}

func TestCachedRefsEmpty(t *testing.T) {
	lock := &LockFile{Registry: map[string]LockEntry{}}
	refs := CachedRefs(lock)
	if len(refs) != 0 {
		t.Errorf("expected 0 refs, got %d", len(refs))
	}
}

func TestCollectActiveRefs(t *testing.T) {
	cfg := config.Config{
		Modules: []config.Module{
			{Name: "local", Items: []config.Item{{Package: "git"}}},
			{Name: "remote", From: "github.com/atomikpanda/dotular/modules/neovim@main"},
			{Name: "remote2", From: "github.com/user/repo"},
		},
	}
	refs := CollectActiveRefs(cfg)
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
	if !refs["github.com/atomikpanda/dotular/modules/neovim@main"] {
		t.Error("missing neovim ref")
	}
	if !refs["github.com/user/repo"] {
		t.Error("missing user/repo ref")
	}
}

func TestParseModule(t *testing.T) {
	data := []byte(`
name: test-module
version: "1.0"
params:
  editor:
    default: vim
items:
  - package: neovim
    via: brew
`)
	mod, err := parseModule(data)
	if err != nil {
		t.Fatal(err)
	}
	if mod.Name != "test-module" {
		t.Errorf("Name = %q", mod.Name)
	}
	if mod.Version != "1.0" {
		t.Errorf("Version = %q", mod.Version)
	}
	if len(mod.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(mod.Items))
	}
	if mod.Items[0].Package != "neovim" {
		t.Errorf("Package = %q", mod.Items[0].Package)
	}
}

func TestParseModuleInvalid(t *testing.T) {
	_, err := parseModule([]byte("{{invalid"))
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestParseModuleRejectsUnknownFields(t *testing.T) {
	tests := []struct {
		name string
		data string
		key  string
	}{
		{
			name: "root",
			data: "nmae: typo\nitems:\n  - package: neovim\n",
			key:  "nmae",
		},
		{
			name: "parameter",
			data: "name: typo\nparams:\n  editor:\n    descrption: preferred editor\nitems:\n  - package: neovim\n",
			key:  "descrption",
		},
		{
			name: "item",
			data: "name: typo\nitems:\n  - packge: neovim\n",
			key:  "packge",
		},
		{
			name: "item hook",
			data: "name: typo\nitems:\n  - package: neovim\n    hooks:\n      after_aply: echo done\n",
			key:  "after_aply",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseModule([]byte(tt.data))
			if err == nil {
				t.Fatal("parseModule() = nil error, want unknown-field rejection")
			}
			for _, want := range []string{"parse registry module", tt.key, "line"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("parseModule() error = %q, want %q", err, want)
				}
			}
		})
	}
}

func TestParseModuleValidatesRawItems(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{
			name: "zero primary fields",
			data: "name: empty-item\nitems:\n  - via: brew\n",
			want: `validate registry module "empty-item": items: item 1: expected exactly one primary field; found none`,
		},
		{
			name: "multiple primary fields",
			data: "name: ambiguous-item\nitems:\n  - package: neovim\n    script: install.sh\n",
			want: `validate registry module "ambiguous-item": items: item 1: expected exactly one primary field; found package, script`,
		},
		{
			name: "invalid literal direction",
			data: "name: bad-direction\nitems:\n  - file: config\n    direction: pul\n",
			want: `validate registry module "bad-direction": items: item 1: direction "pul" must be push, pull, or sync`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseModule([]byte(tt.data))
			if err == nil {
				t.Fatal("parseModule() = nil error, want item validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parseModule() error = %q, want %q", err, tt.want)
			}
		})
	}
}

func TestParseModuleDefersTemplatedDirection(t *testing.T) {
	for _, direction := range []string{"{{ .dir }}", "push-{{ .suffix }}"} {
		t.Run(direction, func(t *testing.T) {
			mod, err := parseModule([]byte(
				"name: templated\nparams:\n  dir:\n    default: push\nitems:\n  - file: config\n    direction: '" + direction + "'\n",
			))
			if err != nil {
				t.Fatalf("parseModule() error = %v", err)
			}
			if got := mod.Items[0].Direction; got != direction {
				t.Fatalf("direction = %q, want %q", got, direction)
			}
		})
	}
}

func TestWriteCacheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "cache.yaml")
	data := []byte("cached data")
	if err := writeCacheFile(path, data); err != nil {
		t.Fatal(err)
	}
	read, _ := os.ReadFile(path)
	if string(read) != "cached data" {
		t.Errorf("read = %q", string(read))
	}
}

func TestClearCacheRemovesRegistryCacheButRetainsMutationLock(t *testing.T) {
	cacheDir, err := registryCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(cacheDir, "nested", "cached.yaml")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte("cached"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ClearCache(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("stat cleared cache directory error = %v, want not exist", err)
	}
	lockPath, err := registryUpdateLockPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("stat registry mutation lock after ClearCache: %v", err)
	}
}

func TestUnusedCacheEntries(t *testing.T) {
	lock := &LockFile{
		Registry: map[string]LockEntry{
			"ref1": {},
			"ref2": {},
			"ref3": {},
		},
	}
	active := map[string]bool{"ref1": true, "ref3": true}
	unused := UnusedCacheEntries(lock, active)
	if len(unused) != 1 {
		t.Fatalf("expected 1 unused, got %d", len(unused))
	}
	if unused[0] != "ref2" {
		t.Errorf("unused = %q", unused[0])
	}
}

func TestResolveRejectsDriftWithoutMutation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref := serveTestModule(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, replacementModuleYAML)
	})
	const missingRef = "example.invalid/missing.yaml"
	configPath := filepath.Join(t.TempDir(), "dotular.yaml")
	original := LockEntry{
		SHA256: testModuleChecksum(testModuleYAML),
		URL:    ParseRef(ref).FetchURL,
	}
	lock := &LockFile{Registry: map[string]LockEntry{ref: original}}
	if err := SaveLock(LockPath(configPath), lock); err != nil {
		t.Fatal(err)
	}
	if err := writeCacheFile(moduleCachePath(ref), []byte(testModuleYAML)); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Modules: []config.Module{
		{From: ref},
		{From: missingRef},
	}}

	_, err := Resolve(
		context.Background(),
		cfg,
		configPath,
		ResolveOptions{NoCache: true},
		ui.New(io.Discard, io.Discard),
	)
	if err == nil {
		t.Fatal("Resolve() succeeded, want checksum drift rejection")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Resolve() error = %q, want checksum mismatch", err)
	}

	persisted := loadTestLock(t, LockPath(configPath))
	if len(persisted.Registry) != 1 {
		t.Fatalf("persisted registry entries = %d, want 1", len(persisted.Registry))
	}
	requireLockEntryUnchanged(t, original, persisted.Registry[ref])
	if _, ok := persisted.Registry[missingRef]; ok {
		t.Fatalf("missing ref %q was pinned after drift failure", missingRef)
	}
	cached, readErr := os.ReadFile(moduleCachePath(ref))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(cached) != testModuleYAML {
		t.Fatalf("cache changed after drift failure: %q", cached)
	}
	if _, statErr := os.Stat(moduleCachePath(missingRef)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing ref cache error = %v, want not exist", statErr)
	}
}

func TestResolveKeepsAllOrdinaryCallFormsImmutable(t *testing.T) {
	type caller func(
		context.Context,
		string,
		config.Config,
		string,
		*LockFile,
		*ui.UI,
	) error
	tests := []struct {
		name         string
		call         caller
		wantMismatch bool
	}{
		{
			name: "Fetch cached",
			call: func(ctx context.Context, ref string, _ config.Config, _ string, lock *LockFile, u *ui.UI) error {
				_, _, err := Fetch(ctx, ref, lock, FetchOptions{}, u)
				return err
			},
		},
		{
			name: "Fetch no-cache",
			call: func(ctx context.Context, ref string, _ config.Config, _ string, lock *LockFile, u *ui.UI) error {
				_, _, err := Fetch(ctx, ref, lock, FetchOptions{NoCache: true}, u)
				return err
			},
			wantMismatch: true,
		},
		{
			name: "Resolve cached",
			call: func(ctx context.Context, _ string, cfg config.Config, configPath string, _ *LockFile, u *ui.UI) error {
				_, err := Resolve(ctx, cfg, configPath, ResolveOptions{}, u)
				return err
			},
		},
		{
			name: "Resolve no-cache",
			call: func(ctx context.Context, _ string, cfg config.Config, configPath string, _ *LockFile, u *ui.UI) error {
				_, err := Resolve(ctx, cfg, configPath, ResolveOptions{NoCache: true}, u)
				return err
			},
			wantMismatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			ref := serveTestModule(t, func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, replacementModuleYAML)
			})
			configPath := filepath.Join(t.TempDir(), "dotular.yaml")
			original := LockEntry{
				SHA256: testModuleChecksum(testModuleYAML),
				URL:    ParseRef(ref).FetchURL,
			}
			lock := &LockFile{Registry: map[string]LockEntry{ref: original}}
			if err := SaveLock(LockPath(configPath), lock); err != nil {
				t.Fatal(err)
			}
			if err := writeCacheFile(moduleCachePath(ref), []byte(testModuleYAML)); err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{Modules: []config.Module{{From: ref}}}

			err := tt.call(
				context.Background(),
				ref,
				cfg,
				configPath,
				lock,
				ui.New(io.Discard, io.Discard),
			)
			if tt.wantMismatch {
				if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
					t.Fatalf("%s error = %v, want checksum mismatch", tt.name, err)
				}
			} else if err != nil {
				t.Fatalf("%s error = %v", tt.name, err)
			}

			requireLockEntryUnchanged(t, original, lock.Registry[ref])
			persisted := loadTestLock(t, LockPath(configPath))
			requireLockEntryUnchanged(t, original, persisted.Registry[ref])
			cached, readErr := os.ReadFile(moduleCachePath(ref))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(cached) != testModuleYAML {
				t.Fatalf("%s changed cache: %q", tt.name, cached)
			}
		})
	}
}
