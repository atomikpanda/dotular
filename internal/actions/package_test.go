package actions

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"testing"
)

func TestPackageActionDescribe(t *testing.T) {
	a := &PackageAction{Package: "neovim", Manager: "brew"}
	got := a.Describe()
	want := `install package "neovim" via brew`
	if got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}
}

func TestInstallArgs(t *testing.T) {
	tests := []struct {
		manager string
		pkg     string
		first   string
		errMsg  string
	}{
		{"brew", "git", "brew", ""},
		{"brew-cask", "firefox", "brew", ""},
		{"mas", "123", "mas", ""},
		{"winget", "Git.Git", "winget", ""},
		{"choco", "git", "choco", ""},
		{"scoop", "git", "scoop", ""},
		{"apt", "git", "sudo", ""},
		{"apt-get", "git", "sudo", ""},
		{"dnf", "git", "sudo", ""},
		{"yum", "git", "sudo", ""},
		{"pacman", "git", "sudo", ""},
		{"snap", "code", "sudo", ""},
		{"flatpak", "org.app", "flatpak", ""},
		{"nix", "git", "nix-env", ""},
		{"unknown-mgr", "pkg", "", "unknown package manager"},
	}
	for _, tt := range tests {
		t.Run(tt.manager, func(t *testing.T) {
			args, err := installArgs(tt.manager, tt.pkg)
			if tt.errMsg != "" {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if args[0] != tt.first {
				t.Errorf("first arg = %q, want %q", args[0], tt.first)
			}
		})
	}
}

func TestCheckArgsPreservesPerPackageExitStatusContract(t *testing.T) {
	tests := []struct {
		manager string
		pkg     string
		want    []string
	}{
		{"brew", "git", []string{"brew", "list", "--formula", "git"}},
		{"brew-cask", "wezterm", []string{"brew", "list", "--cask", "wezterm"}},
		{"mas", "123", []string{"mas", "list"}},
		{"winget", "Vendor.App", []string{"winget", "list", "--id", "Vendor.App", "-e"}},
		{"choco", "git", []string{"choco", "list", "--local-only", "git"}},
		{"scoop", "git", []string{"scoop", "info", "git"}},
		{"apt", "curl", []string{"dpkg", "-s", "curl"}},
		{"apt-get", "curl", []string{"dpkg", "-s", "curl"}},
		{"dnf", "git", []string{"rpm", "-q", "git"}},
		{"yum", "git", []string{"rpm", "-q", "git"}},
		{"pacman", "git", []string{"pacman", "-Q", "git"}},
		{"snap", "code", []string{"snap", "list", "code"}},
		{"flatpak", "org.example.App", []string{"flatpak", "info", "org.example.App"}},
		{"nix", "nixpkgs.git", []string{"nix-env", "-q", "nixpkgs.git"}},
		{"unknown-mgr", "foo", nil},
	}
	for _, tt := range tests {
		t.Run(tt.manager+"/"+tt.pkg, func(t *testing.T) {
			if got := CheckArgs(tt.manager, tt.pkg); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("CheckArgs(%q, %q) = %#v, want %#v", tt.manager, tt.pkg, got, tt.want)
			}
		})
	}
}

func TestPackageActionRunDryRun(t *testing.T) {
	a := &PackageAction{Package: "git", Manager: "brew"}
	if err := a.Run(context.Background(), true); err != nil {
		t.Errorf("dry run error: %v", err)
	}
}

func TestPackageActionRunUnknownManager(t *testing.T) {
	a := &PackageAction{Package: "git", Manager: "nonexistent"}
	err := a.Run(context.Background(), false)
	if err == nil {
		t.Error("expected error for unknown manager")
	}
}

func TestPackageActionRunDryRunUnknownManager(t *testing.T) {
	// Dry run still calls installArgs, which fails for unknown managers.
	a := &PackageAction{Package: "git", Manager: "nonexistent"}
	err := a.Run(context.Background(), true)
	if err == nil {
		t.Error("expected error for unknown manager even in dry run")
	}
}

func TestPackageActionIsAppliedNoCheck(t *testing.T) {
	// unknown manager has no check command — should return false, nil.
	a := &PackageAction{Package: "git", Manager: "unknown"}
	applied, err := a.IsApplied(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Error("expected false for manager with no check")
	}
}

func TestPackageActionIsAppliedMissingBinary(t *testing.T) {
	// Use a manager whose check binary won't exist — should return false, nil.
	a := &PackageAction{Package: "test-pkg", Manager: "pacman"}
	applied, err := a.IsApplied(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// pacman likely doesn't exist on macOS/other, so the exec will fail.
	if applied {
		t.Error("expected false when check binary is missing")
	}
}

type packageCommandCall struct {
	args          []string
	captureOutput bool
}

type packageCommandResult struct {
	output []byte
	err    error
}

type recordingPackageExecutor struct {
	results []packageCommandResult
	calls   []packageCommandCall
}

func (e *recordingPackageExecutor) execute(ctx context.Context, args []string, captureOutput bool) ([]byte, error) {
	e.calls = append(e.calls, packageCommandCall{
		args:          append([]string(nil), args...),
		captureOutput: captureOutput,
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(e.results) == 0 {
		return nil, errors.New("unexpected package command")
	}
	result := e.results[0]
	e.results = e.results[1:]
	return result.output, result.err
}

func TestPackageCompensationManagerMatrix(t *testing.T) {
	tests := []struct {
		manager            string
		pkg                string
		query              []string
		uninstall          []string
		prefixOutput       string
		presentOutput      string
		malformedOutput    string
		absenceUnavailable bool
	}{
		{
			manager:            "brew",
			pkg:                "foo",
			query:              []string{"brew", "list", "--formula", "--full-name"},
			uninstall:          []string{"brew", "uninstall", "foo"},
			prefixOutput:       "foobar\n",
			presentOutput:      "foobar\nfoo\n",
			malformedOutput:    string([]byte{0xff}),
			absenceUnavailable: true,
		},
		{
			manager:            "brew-cask",
			pkg:                "foo",
			query:              []string{"brew", "list", "--cask", "--full-name"},
			uninstall:          []string{"brew", "uninstall", "--cask", "foo"},
			prefixOutput:       "foobar\n",
			presentOutput:      "foobar\nfoo\n",
			malformedOutput:    string([]byte{0xff}),
			absenceUnavailable: true,
		},
		{
			manager:         "mas",
			pkg:             "123",
			query:           []string{"mas", "list"},
			uninstall:       []string{"mas", "uninstall", "123"},
			prefixOutput:    "1234 Prefix App (1.0)\n",
			presentOutput:   "1234 Prefix App (1.0)\n123 Exact App (1.0)\n",
			malformedOutput: "not-a-number App (1.0)\n",
		},
		{
			manager:         "winget",
			pkg:             "Vendor.App",
			query:           []string{"winget", "list", "--disable-interactivity"},
			uninstall:       []string{"winget", "uninstall", "--id", "Vendor.App", "-e", "--disable-interactivity"},
			prefixOutput:    "Name          Id               Version\n--------------------------------------\nPrefix App    Vendor.App.Pro   1.0\n",
			presentOutput:   "Name          Id               Version\n--------------------------------------\nPrefix App    Vendor.App.Pro   1.0\nExact App     Vendor.App       1.0\n",
			malformedOutput: "winget output without a package table\n",
		},
		{
			manager:         "choco",
			pkg:             "foo",
			query:           []string{"choco", "list", "--local-only", "--limit-output"},
			uninstall:       []string{"choco", "uninstall", "foo", "-y"},
			prefixOutput:    "foobar|1.0\n",
			presentOutput:   "foobar|1.0\nfoo|1.0\n",
			malformedOutput: "missing-version-separator\n",
		},
		{
			manager:         "scoop",
			pkg:             "foo",
			query:           []string{"scoop", "export"},
			uninstall:       []string{"scoop", "uninstall", "foo"},
			prefixOutput:    `{"apps":[{"Name":"foobar"}]}`,
			presentOutput:   `{"apps":[{"Name":"foobar"},{"Name":"foo"}]}`,
			malformedOutput: `{"apps":`,
		},
		{
			manager:            "apt",
			pkg:                "foo",
			query:              []string{"dpkg-query", "-W", "-f=${binary:Package}\t${db:Status-Abbrev}\n"},
			uninstall:          []string{"sudo", "apt-get", "remove", "-y", "foo"},
			prefixOutput:       "foobar\tii \n",
			presentOutput:      "foobar\tii \nfoo\tii \n",
			malformedOutput:    "foo\tbroken\n",
			absenceUnavailable: true,
		},
		{
			manager:            "apt-get",
			pkg:                "foo",
			query:              []string{"dpkg-query", "-W", "-f=${binary:Package}\t${db:Status-Abbrev}\n"},
			uninstall:          []string{"sudo", "apt-get", "remove", "-y", "foo"},
			prefixOutput:       "foobar\tii \n",
			presentOutput:      "foobar\tii \nfoo\tii \n",
			malformedOutput:    "foo\tbroken\n",
			absenceUnavailable: true,
		},
		{
			manager:            "dnf",
			pkg:                "foo.x86_64",
			query:              []string{"rpm", "-qa", "--qf", "%{NAME}\t%{EPOCH}\t%{VERSION}\t%{RELEASE}\t%{ARCH}\t[%{PROVIDENAME},]\n"},
			uninstall:          []string{"sudo", "dnf", "remove", "-y", "foo.x86_64"},
			prefixOutput:       "foobar\t(none)\t1.0\t1\tx86_64\tfoobar,\n",
			presentOutput:      "foobar\t(none)\t1.0\t1\tx86_64\tfoobar,\nfoo\t(none)\t1.0\t1\tx86_64\tfoo,\n",
			malformedOutput:    string([]byte{0xff}),
			absenceUnavailable: true,
		},
		{
			manager:            "yum",
			pkg:                "foo.x86_64",
			query:              []string{"rpm", "-qa", "--qf", "%{NAME}\t%{EPOCH}\t%{VERSION}\t%{RELEASE}\t%{ARCH}\t[%{PROVIDENAME},]\n"},
			uninstall:          []string{"sudo", "yum", "remove", "-y", "foo.x86_64"},
			prefixOutput:       "foobar\t(none)\t1.0\t1\tx86_64\tfoobar,\n",
			presentOutput:      "foobar\t(none)\t1.0\t1\tx86_64\tfoobar,\nfoo\t(none)\t1.0\t1\tx86_64\tfoo,\n",
			malformedOutput:    string([]byte{0xff}),
			absenceUnavailable: true,
		},
		{
			manager:            "pacman",
			pkg:                "foo",
			query:              []string{"pacman", "-Qq"},
			uninstall:          []string{"sudo", "pacman", "-R", "--noconfirm", "foo"},
			prefixOutput:       "foobar\n",
			presentOutput:      "foobar\nfoo\n",
			malformedOutput:    string([]byte{0xff}),
			absenceUnavailable: true,
		},
		{
			manager:         "snap",
			pkg:             "foo",
			query:           []string{"snap", "list"},
			uninstall:       []string{"sudo", "snap", "remove", "foo"},
			prefixOutput:    "Name    Version  Rev  Tracking  Publisher  Notes\nfoobar  1.0      1    latest    vendor     -\n",
			presentOutput:   "Name    Version  Rev  Tracking  Publisher  Notes\nfoobar  1.0      1    latest    vendor     -\nfoo     1.0      1    latest    vendor     -\n",
			malformedOutput: "output without the snap list header\n",
		},
		{
			manager:         "flatpak",
			pkg:             "org.example.App/x86_64",
			query:           []string{"flatpak", "list", "--columns=application,arch,branch"},
			uninstall:       []string{"flatpak", "uninstall", "-y", "org.example.App/x86_64"},
			prefixOutput:    "org.example.App.Pro\tx86_64\tstable\n",
			presentOutput:   "org.example.App.Pro\tx86_64\tstable\norg.example.App\tx86_64\tstable\n",
			malformedOutput: "org.example.App\n",
		},
		{
			manager:   "nix",
			pkg:       "nixpkgs.foo",
			query:     nil,
			uninstall: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.manager, func(t *testing.T) {
			if got := packageStateArgs(tt.manager, tt.pkg); !reflect.DeepEqual(got, tt.query) {
				t.Fatalf("packageStateArgs() = %#v, want %#v", got, tt.query)
			}

			if tt.uninstall == nil {
				executor := &recordingPackageExecutor{}
				action := &PackageAction{Package: tt.pkg, Manager: tt.manager, executor: executor.execute}
				preparation, err := action.PrepareCompensation(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if preparation.AlreadyApplied || preparation.Compensation != nil || preparation.UnavailableReason == "" {
					t.Fatalf("imprecise preparation = %#v, want unavailable only", preparation)
				}
				if len(executor.calls) != 0 {
					t.Fatalf("imprecise manager executed state query: %#v", executor.calls)
				}
				return
			}

			t.Run("prefix_does_not_match_exact_identity", func(t *testing.T) {
				executor := &recordingPackageExecutor{results: []packageCommandResult{
					{output: []byte(tt.prefixOutput)},
					{},
				}}
				action := &PackageAction{Package: tt.pkg, Manager: tt.manager, executor: executor.execute}
				preparation, err := action.PrepareCompensation(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if tt.absenceUnavailable {
					if preparation.AlreadyApplied || preparation.Compensation != nil || preparation.UnavailableReason == "" {
						t.Fatalf("ambiguous absence preparation = %#v, want unavailable", preparation)
					}
					return
				}
				if preparation.AlreadyApplied || preparation.Compensation == nil || preparation.UnavailableReason != "" {
					t.Fatalf("absent preparation = %#v, want exact compensation", preparation)
				}
				if got := preparation.Compensation.Describe(); got == "" {
					t.Fatal("compensation description is empty")
				}
				if err := preparation.Compensation.Run(context.Background()); err != nil {
					t.Fatal(err)
				}
				wantCalls := []packageCommandCall{
					{args: tt.query, captureOutput: true},
					{args: tt.uninstall, captureOutput: false},
				}
				if !reflect.DeepEqual(executor.calls, wantCalls) {
					t.Fatalf("calls = %#v, want %#v", executor.calls, wantCalls)
				}
			})

			t.Run("present_is_already_applied", func(t *testing.T) {
				executor := &recordingPackageExecutor{results: []packageCommandResult{{output: []byte(tt.presentOutput)}}}
				action := &PackageAction{Package: tt.pkg, Manager: tt.manager, executor: executor.execute}
				preparation, err := action.PrepareCompensation(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if !preparation.AlreadyApplied || preparation.Compensation != nil || preparation.UnavailableReason != "" {
					t.Fatalf("present preparation = %#v, want AlreadyApplied only", preparation)
				}
			})

			unknownCases := []struct {
				name   string
				output []byte
				err    error
			}{
				{name: "start_failure", err: errors.New("start failed")},
				{name: "nonzero_status", err: &exec.ExitError{}},
				{name: "malformed_output", output: []byte(tt.malformedOutput)},
			}
			for _, unknown := range unknownCases {
				t.Run(unknown.name+"_never_uninstalls", func(t *testing.T) {
					executor := &recordingPackageExecutor{results: []packageCommandResult{{
						output: unknown.output,
						err:    unknown.err,
					}}}
					action := &PackageAction{Package: tt.pkg, Manager: tt.manager, executor: executor.execute}
					preparation, err := action.PrepareCompensation(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					if preparation.AlreadyApplied || preparation.Compensation != nil || preparation.UnavailableReason == "" {
						t.Fatalf("unknown preparation = %#v, want unavailable only", preparation)
					}
					if len(executor.calls) != 1 {
						t.Fatalf("unknown state made %d calls, want state query only", len(executor.calls))
					}
				})
			}

			t.Run("canceled_context_never_uninstalls", func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				executor := &recordingPackageExecutor{}
				action := &PackageAction{Package: tt.pkg, Manager: tt.manager, executor: executor.execute}
				preparation, err := action.PrepareCompensation(ctx)
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("PrepareCompensation() error = %v, want context.Canceled", err)
				}
				if preparation.Compensation != nil {
					t.Fatalf("canceled preparation has compensation: %#v", preparation)
				}
				if len(executor.calls) != 0 {
					t.Fatalf("canceled state made %d calls, want fail-fast without a query", len(executor.calls))
				}
			})
		})
	}
}

func TestPackageCompensationNoMatchInventoryDoesNotAuthorizeProviderUninstall(t *testing.T) {
	tests := []struct {
		name    string
		manager string
		output  string
	}{
		{
			name:    "dpkg_empty_inventory",
			manager: "apt",
		},
		{
			name:    "dpkg_unrelated_inventory",
			manager: "apt-get",
			output:  "postfix\tii \n",
		},
		{
			name:    "rpm_empty_inventory",
			manager: "dnf",
		},
		{
			name:    "rpm_unrelated_inventory",
			manager: "yum",
			output:  "postfix\t(none)\t3.9\t1\tx86_64\tpostfix,\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &recordingPackageExecutor{results: []packageCommandResult{{
				output: []byte(tt.output),
			}}}
			action := &PackageAction{
				Package:  "mail-transport-agent",
				Manager:  tt.manager,
				executor: executor.execute,
			}

			preparation, err := action.PrepareCompensation(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if preparation.AlreadyApplied || preparation.Compensation != nil || preparation.UnavailableReason == "" {
				t.Fatalf("preparation = %#v, want unknown state with explicit fallback available", preparation)
			}
			if len(executor.calls) != 1 {
				t.Fatalf("state capture made %d calls, want inventory query only", len(executor.calls))
			}
		})
	}
}

func TestPackageActionIsAppliedUsesPerPackageCheckAfterUnknownCompensationCapture(t *testing.T) {
	for _, manager := range []string{"apt", "dnf"} {
		t.Run(manager, func(t *testing.T) {
			executor := &recordingPackageExecutor{results: []packageCommandResult{
				{err: errors.New("inventory unavailable")},
				{},
			}}
			action := &PackageAction{
				Package:  "mail-transport-agent",
				Manager:  manager,
				executor: executor.execute,
			}

			preparation, err := action.PrepareCompensation(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if preparation.AlreadyApplied || preparation.Compensation != nil || preparation.UnavailableReason == "" {
				t.Fatalf("preparation = %#v, want unknown state with explicit fallback available", preparation)
			}

			applied, err := action.IsApplied(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !applied {
				t.Fatal("IsApplied() = false, want installed package skipped")
			}
			if len(executor.calls) != 2 {
				t.Fatalf("package checks made %d calls, want inventory capture then per-package check", len(executor.calls))
			}
			if got, want := executor.calls[1].args, CheckArgs(manager, action.Package); !reflect.DeepEqual(got, want) {
				t.Fatalf("IsApplied() args = %#v, want original per-package check %#v", got, want)
			}
		})
	}
}

func TestPackageActionIsAppliedUsesPerPackageExitStatus(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		err     error
		applied bool
	}{
		{name: "successful_empty_output", applied: true},
		{name: "successful_arbitrary_output", output: string([]byte{0xff}), applied: true},
		{name: "nonzero_status", err: &exec.ExitError{}},
		{name: "start_failure", err: errors.New("start failed")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &recordingPackageExecutor{results: []packageCommandResult{{
				output: []byte(tt.output),
				err:    tt.err,
			}}}
			action := &PackageAction{Package: "foo", Manager: "brew", executor: executor.execute}
			applied, err := action.IsApplied(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if applied != tt.applied {
				t.Fatalf("IsApplied() = %t, want %t", applied, tt.applied)
			}
			if got, want := executor.calls[0].args, CheckArgs(action.Manager, action.Package); !reflect.DeepEqual(got, want) {
				t.Fatalf("IsApplied() args = %#v, want %#v", got, want)
			}
		})
	}
}

func TestPackageActionCanceledStateCheckReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	executor := &recordingPackageExecutor{}
	action := &PackageAction{Package: "foo", Manager: "brew", executor: executor.execute}
	if _, err := action.IsApplied(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("IsApplied() error = %v, want context.Canceled", err)
	}
}

func TestPackageCompensationAmbiguousIdentitySafety(t *testing.T) {
	tests := []struct {
		name        string
		manager     string
		pkg         string
		output      string
		wantPresent bool
		wantUnknown bool
		wantAbsent  bool
	}{
		{
			name:        "dpkg_architecture_qualified_held_package_is_unknown_for_bare_target",
			manager:     "apt",
			pkg:         "libc6",
			output:      "libc6:amd64\thi \n",
			wantUnknown: true,
		},
		{
			name:        "dpkg_current_installed_status_ignores_desired_state",
			manager:     "apt-get",
			pkg:         "libc6",
			output:      "libc6\tri \n",
			wantPresent: true,
		},
		{
			name:        "dpkg_related_transitional_package_is_unknown",
			manager:     "apt",
			pkg:         "libc6",
			output:      "libc6-amd64\tii \n",
			wantUnknown: true,
		},
		{
			name:        "winget_unicode_name_preserves_exact_id",
			manager:     "winget",
			pkg:         "Vendor.App",
			output:      "Name            Id               Version\n----------------------------------------\n工具工具工具工  Vendor.App       1.0\n",
			wantPresent: true,
		},
		{
			name:        "winget_unstructured_long_row_is_unknown",
			manager:     "winget",
			pkg:         "Vendor.App",
			output:      "Name            Id               Version\n----------------------------------------\nthis row has no reliable column separators at all\n",
			wantUnknown: true,
		},
		{
			name:        "flatpak_branch_only_ref_is_unknown_without_default_architecture",
			manager:     "flatpak",
			pkg:         "org.example.App//beta",
			output:      "org.example.App\tx86_64\tbeta\n",
			wantUnknown: true,
		},
		{
			name:        "flatpak_application_only_row_cannot_disprove_branch_ref",
			manager:     "flatpak",
			pkg:         "org.example.App//beta",
			output:      "org.example.App\n",
			wantUnknown: true,
		},
		{
			name:        "rpm_architecture_qualified_name_is_present",
			manager:     "dnf",
			pkg:         "foo.x86_64",
			output:      "foo\t(none)\t1.2\t1\tx86_64\tfoo,\n",
			wantPresent: true,
		},
		{
			name:        "rpm_related_nevra_cannot_be_mapped_exactly",
			manager:     "yum",
			pkg:         "foo-1.2-1.x86_64",
			output:      "foo\t(none)\t1.2\t1\tx86_64\tfoo,\n",
			wantUnknown: true,
		},
		{
			name:        "brew_qualified_core_name_with_short_inventory_name_is_unknown",
			manager:     "brew",
			pkg:         "homebrew/core/git",
			output:      "git\n",
			wantUnknown: true,
		},
		{
			name:        "dpkg_half_installed_is_unknown",
			manager:     "apt",
			pkg:         "foo",
			output:      "foo\tiH \n",
			wantUnknown: true,
		},
		{
			name:        "dpkg_unpacked_is_unknown",
			manager:     "apt",
			pkg:         "foo",
			output:      "foo\tiU \n",
			wantUnknown: true,
		},
		{
			name:        "dpkg_half_configured_is_unknown",
			manager:     "apt",
			pkg:         "foo",
			output:      "foo\tiF \n",
			wantUnknown: true,
		},
		{
			name:        "dpkg_triggers_awaiting_is_unknown",
			manager:     "apt",
			pkg:         "foo",
			output:      "foo\tiW \n",
			wantUnknown: true,
		},
		{
			name:        "dpkg_triggers_pending_is_unknown",
			manager:     "apt",
			pkg:         "foo",
			output:      "foo\tit \n",
			wantUnknown: true,
		},
		{
			name:        "dpkg_invalid_desired_status_is_unknown",
			manager:     "apt",
			pkg:         "foo",
			output:      "foo\txi \n",
			wantUnknown: true,
		},
		{
			name:        "dpkg_invalid_error_status_is_unknown",
			manager:     "apt",
			pkg:         "foo",
			output:      "foo\tiiX\n",
			wantUnknown: true,
		},
		{
			name:        "apt_glob_selector_is_unknown",
			manager:     "apt",
			pkg:         "foo*",
			output:      "foo\tii \n",
			wantUnknown: true,
		},
		{
			name:        "apt_any_architecture_selector_is_unknown",
			manager:     "apt-get",
			pkg:         "foo:any",
			output:      "foo:amd64\tii \n",
			wantUnknown: true,
		},
		{
			name:        "apt_hyphenated_prefix_package_is_unknown",
			manager:     "apt",
			pkg:         "foo",
			output:      "foo-dev\tii \n",
			wantUnknown: true,
		},
		{
			name:        "rpm_glob_selector_is_unknown",
			manager:     "dnf",
			pkg:         "foo*",
			output:      "foo\t(none)\t1.2\t1\tx86_64\tfoo,\n",
			wantUnknown: true,
		},
		{
			name:        "rpm_provide_selector_is_unknown",
			manager:     "yum",
			pkg:         "/usr/bin/foo",
			output:      "foo\t(none)\t1.2\t1\tx86_64\tfoo,\n",
			wantUnknown: true,
		},
		{
			name:        "rpm_hyphenated_prefix_package_is_unknown",
			manager:     "dnf",
			pkg:         "foo",
			output:      "foo-devel\t(none)\t1.2\t1\tx86_64\tfoo-devel,\n",
			wantUnknown: true,
		},
		{
			name:        "flatpak_application_architecture_ref_is_present",
			manager:     "flatpak",
			pkg:         "org.example.App/x86_64",
			output:      "org.example.App\tx86_64\tstable\n",
			wantPresent: true,
		},
		{
			name:       "snap_successful_empty_inventory_is_absent",
			manager:    "snap",
			pkg:        "foo",
			output:     "No snaps are installed yet. Try 'snap install hello-world'.\n",
			wantAbsent: true,
		},
		{
			name:        "brew_alias_target_is_unknown_without_canonical_resolution",
			manager:     "brew",
			pkg:         "openssl",
			output:      "openssl@3\n",
			wantUnknown: true,
		},
		{
			name:        "dpkg_native_architecture_omission_is_unknown_for_qualified_target",
			manager:     "apt",
			pkg:         "foo:amd64",
			output:      "foo\tii \n",
			wantUnknown: true,
		},
		{
			name:        "dpkg_foreign_architecture_is_unknown_for_bare_target",
			manager:     "apt-get",
			pkg:         "foo",
			output:      "foo:arm64\tii \n",
			wantUnknown: true,
		},
		{
			name:        "apt_action_suffix_is_unknown",
			manager:     "apt",
			pkg:         "foo+",
			output:      "foo\tii \n",
			wantUnknown: true,
		},
		{
			name:        "dpkg_reinstall_required_is_unknown",
			manager:     "apt",
			pkg:         "foo",
			output:      "foo\tiiR\n",
			wantUnknown: true,
		},
		{
			name:        "rpm_alphabetic_nevra_is_unknown_even_when_version_differs",
			manager:     "dnf",
			pkg:         "foo-next-1.x86_64",
			output:      "foo\t(none)\tbeta\t1\tx86_64\tfoo,\n",
			wantUnknown: true,
		},
		{
			name:        "rpm_group_selector_is_unknown",
			manager:     "dnf",
			pkg:         "@development-tools",
			output:      "gcc\t(none)\t14.1\t1\tx86_64\tgcc,\n",
			wantUnknown: true,
		},
		{
			name:        "rpm_virtual_provide_is_unknown",
			manager:     "yum",
			pkg:         "mail-transport-agent",
			output:      "postfix\t(none)\t3.9\t1\tx86_64\tmail-transport-agent,postfix,\n",
			wantUnknown: true,
		},
		{
			name:        "rpm_foreign_architecture_is_unknown_for_bare_target",
			manager:     "dnf",
			pkg:         "foo",
			output:      "foo\t(none)\t1.2\t1\tarm64\tfoo,\n",
			wantUnknown: true,
		},
		{
			name:        "winget_truncated_id_is_unknown",
			manager:     "winget",
			pkg:         "Vendor.Application",
			output:      "Name          Id               Version\n--------------------------------------\nApplication   Vendor.App…      1.0\n",
			wantUnknown: true,
		},
		{
			name:        "winget_id_matching_is_case_insensitive",
			manager:     "winget",
			pkg:         "vendor.app",
			output:      "Name          Id               Version\n--------------------------------------\nApplication   Vendor.App       1.0\n",
			wantPresent: true,
		},
		{
			name:        "mas_numeric_ids_are_canonicalized",
			manager:     "mas",
			pkg:         "00123",
			output:      "123 Exact App (1.0)\n",
			wantPresent: true,
		},
		{
			name:        "choco_ids_are_case_insensitive",
			manager:     "choco",
			pkg:         "foo",
			output:      "Foo|1.0\n",
			wantPresent: true,
		},
		{
			name:        "scoop_ids_are_case_insensitive",
			manager:     "scoop",
			pkg:         "foo",
			output:      `{"apps":[{"Name":"Foo","Source":"main"}]}`,
			wantPresent: true,
		},
		{
			name:        "scoop_source_qualified_identity_is_present",
			manager:     "scoop",
			pkg:         "extras/foo",
			output:      `{"apps":[{"Name":"foo","Source":"extras"}]}`,
			wantPresent: true,
		},
		{
			name:        "pacman_group_target_is_unknown",
			manager:     "pacman",
			pkg:         "base-devel",
			output:      "autoconf\nautomake\n",
			wantUnknown: true,
		},
		{
			name:        "pacman_repository_qualified_target_is_unknown",
			manager:     "pacman",
			pkg:         "core/foo",
			output:      "foo\n",
			wantUnknown: true,
		},
		{
			name:        "flatpak_partial_ref_with_nondefault_branch_is_unknown",
			manager:     "flatpak",
			pkg:         "org.example.App/x86_64",
			output:      "org.example.App\tx86_64\tbeta\n",
			wantUnknown: true,
		},
		{
			name:        "brew_non_version_alias_is_unknown_without_canonical_resolution",
			manager:     "brew",
			pkg:         "gpg",
			output:      "gnupg\n",
			wantUnknown: true,
		},
		{
			name:        "rpm_unresolved_version_like_target_is_unknown",
			manager:     "dnf",
			pkg:         "foo-2-1",
			output:      "foo\t(none)\t1\t1\tx86_64\tfoo,\n",
			wantUnknown: true,
		},
		{
			name:        "flatpak_fuzzy_application_name_is_unknown",
			manager:     "flatpak",
			pkg:         "gedit",
			output:      "org.gnome.gedit\tx86_64\tstable\n",
			wantUnknown: true,
		},
		{
			name:        "scoop_qualified_identity_with_empty_source_is_unknown",
			manager:     "scoop",
			pkg:         "extras/foo",
			output:      `{"apps":[{"Name":"foo","Source":""}]}`,
			wantUnknown: true,
		},
		{
			name:        "scoop_qualified_identity_with_null_source_is_unknown",
			manager:     "scoop",
			pkg:         "extras/foo",
			output:      `{"apps":[{"Name":"foo","Source":null}]}`,
			wantUnknown: true,
		},
		{
			name:        "flatpak_unqualified_ref_with_secondary_architecture_is_unknown",
			manager:     "flatpak",
			pkg:         "org.example.App",
			output:      "org.example.App\taarch64\tstable\n",
			wantUnknown: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &recordingPackageExecutor{results: []packageCommandResult{{
				output: []byte(tt.output),
			}}}
			action := &PackageAction{Package: tt.pkg, Manager: tt.manager, executor: executor.execute}
			preparation, err := action.PrepareCompensation(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantPresent {
				if !preparation.AlreadyApplied || preparation.Compensation != nil || preparation.UnavailableReason != "" {
					t.Fatalf("preparation = %#v, want conclusive presence", preparation)
				}
				return
			}
			if tt.wantUnknown {
				if preparation.AlreadyApplied || preparation.Compensation != nil || preparation.UnavailableReason == "" {
					t.Fatalf("preparation = %#v, want unknown state with no automatic uninstall", preparation)
				}
				return
			}
			if tt.wantAbsent {
				if preparation.AlreadyApplied || preparation.Compensation == nil || preparation.UnavailableReason != "" {
					t.Fatalf("preparation = %#v, want conclusive absence", preparation)
				}
				return
			}
			t.Fatal("test case has no expected state")
		})
	}
}

func TestPackageActionCanceledUnsupportedStateReturnsError(t *testing.T) {
	for _, manager := range []string{"nix", "unknown"} {
		t.Run(manager, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			action := &PackageAction{Package: "foo", Manager: manager}

			if preparation, err := action.PrepareCompensation(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("PrepareCompensation() = %#v, %v; want context.Canceled", preparation, err)
			}
			if applied, err := action.IsApplied(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("IsApplied() = %t, %v; want context.Canceled", applied, err)
			}
		})
	}
}

var _ CompensationPreparer = (*PackageAction)(nil)
