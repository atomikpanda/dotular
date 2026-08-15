# Consistent Tag Filtering Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Spec:** `consistent-tag-filtering`

**Goal:** Enforce module tag filters consistently before registry resolution, expose an explicit override, and make inactive modules visible without allowing resolver side effects.

## Assumptions checked

- repo topology — covered: one Go repository (`dotular`) and one feature worktree/PR.
- credential locus — N/A: implementation and tests use local files and in-process HTTP servers; no new credential is introduced.
- execution locus — covered: development and smoke tests run in the Mothership worktree; hosted CI runs the existing GitHub Actions matrix.
- state durability — covered: cache, lockfile, machine tags, config, and audit paths are isolated in tests; excluded modules must publish none of them.
- review surface — covered: one PR closes #26 with production code, focused regressions, README updates, and the approved spec/plan.
- agent stream — covered: inline execution in this session, with Mothership journal/test evidence and hosted review before handoff.
- dispatched model — N/A: no implementation subagent is dispatched.

**Architecture:** Keep `internal/tags.Matches` as the only matching-rule implementation. Add a CLI-owned tagged-config boundary that strict-loads config and machine tags, retains original ordering, builds a filtered config before `registry.Resolve`, and keeps enough raw metadata to diagnose named inactive modules and render inactive `list` rows. Add an opt-in `Runner.IgnoreTags` switch so the CLI can deliberately bypass the runner's defense-in-depth filter without weakening default direct callers.

**Tech Stack:** Go 1.23, Cobra, existing `internal/config`, `internal/tags`, `internal/registry`, `internal/runner`, `internal/ui`, Go `testing`, `httptest`, Mothership.

## Global Constraints

- `internal/tags.Matches` remains the single source of truth for `only_tags` / `exclude_tags` matching.
- Strict config validation and machine-tag loading happen before registry network/cache/lock work.
- No new registry schema, tag inference, dependencies, compatibility shim, or implicit override.
- Active-module order and named active-module behavior remain unchanged.
- Excluded remote modules cause zero requests and zero cache/lock publication.
- Every behavior change is developed RED/GREEN and committed as a coherent unit.

---

<!-- mship:task id=1 acs=ac1,ac2,ac3,ac4,ac5,ac7,ac8 -->
### Task 1: Enforce Tags Before Resolution and Named Execution

**Files:**
- Modify: `cmd/dotular/main.go:177-190,412-486,535-613`
- Modify: `cmd/dotular/main_test.go:530-729,812-856`
- Modify: `internal/runner/runner.go:44-56,505-507`
- Modify: `internal/runner/runner_test.go` (tag-filter tests near existing `matchesTags` coverage)

**Interfaces:**
- Consumes: `tags.Load() (*tags.MachineConfig, error)`, `tags.Matches(machineTags, onlyTags, excludeTags []string) bool`, `registry.Resolve(...)`, `config.Config`, and existing Cobra command constructors.
- Produces: CLI-local `taggedConfig` containing `raw config.Config`, `active config.Config`, `activeMask []bool`, and `machineTags []string`; `loadTaggedConfig(ignoreTags bool) (taggedConfig, error)`; `resolveTaggedConfig(context.Context, taggedConfig) (config.Config, error)`; inactive-name diagnostics consumed by apply/direction/verify selection and Task 2 list rendering; `runner.Runner.IgnoreTags bool`.

- [ ] **Step 1: Inspect symbol-aware call sites before changing interfaces**

Use the LSP for references to `loadAndResolveConfig`, `selectModules`, `applyNamedModules`, `Runner`, and `matchesTags`. Confirm every command caller and runner test that must migrate; do not use text replacement for interface changes.

- [ ] **Step 2: Write failing command-boundary tests**

Add table-driven end-to-end command tests in `cmd/dotular/main_test.go` for:

```go
func TestNamedCommandsRejectTagInactiveModuleBeforeSideEffects(t *testing.T) {
    commands := [][]string{
        {"apply"}, {"push"}, {"pull"}, {"sync"}, {"verify"},
    }
    // Config contains an explicitly named module with only_tags: [work],
    // a before_apply hook / verify command that would create a marker, and no
    // matching machine tag. Each invocation names the module.
    // Assert non-zero usage error contains module name, "tag filters do not match",
    // and "--ignore-tags"; marker, audit log, cache, and lockfile remain absent.
}

func TestCommandsIgnoreTagsOnlyWhenExplicitlyRequested(t *testing.T) {
    // Table: apply/push/pull/sync use --dry-run --ignore-tags; verify uses a
    // deterministic true verify command; status uses --ignore-tags.
    // Assert each command reaches the inactive module and returns nil.
}
```

Add the resolver-order regression:

```go
func TestTagFilteringPrecedesRegistryResolution(t *testing.T) {
    // Isolate HOME and XDG_CACHE_HOME. Serve a valid remote module with an
    // httptest TLS server and request counter. Configure the aliased remote
    // module with only_tags: [work], but leave machine tags empty.
    // Execute apply, status, and verify in subtests without --ignore-tags.
    // Assert requests == 0 and registry.LockPath(configPath), module cache,
    // audit log, and marker files do not exist.
}
```

Add a malformed machine-tag boundary test by writing invalid YAML to `tags.ConfigPath()`, pointing config at the same counting server, and asserting the parse error appears before request/cache/lock publication.

- [ ] **Step 3: Run focused tests and confirm RED**

Run:

```bash
go test ./cmd/dotular -run 'Test(NamedCommandsRejectTagInactiveModuleBeforeSideEffects|CommandsIgnoreTagsOnlyWhenExplicitlyRequested|TagFilteringPrecedesRegistryResolution|MalformedMachineTagsFailBeforeRegistryResolution)$' -count=1
```

Expected: FAIL because named commands bypass tag checks, `--ignore-tags` is unknown, excluded remotes are fetched, and machine-tag load errors are swallowed by `runner.New`.

- [ ] **Step 4: Add the tagged-config boundary with no duplicate matching logic**

In `cmd/dotular/main.go`, replace the load-then-resolve helper with this shape (names may be adjusted only if existing local conventions require it):

```go
type taggedConfig struct {
    raw         config.Config
    active      config.Config
    activeMask  []bool
    machineTags []string
}

func loadTaggedConfig(ignoreTags bool) (taggedConfig, error) {
    cfg, err := loadConfig()
    if err != nil {
        return taggedConfig{}, err
    }
    machine, err := tags.Load()
    if err != nil {
        return taggedConfig{}, err
    }

    selected := taggedConfig{
        raw:         cfg,
        active:      config.Config{Age: cfg.Age},
        activeMask:  make([]bool, len(cfg.Modules)),
        machineTags: append([]string(nil), machine.Tags...),
    }
    for i, mod := range cfg.Modules {
        active := ignoreTags || tags.Matches(machine.Tags, mod.OnlyTags, mod.ExcludeTags)
        selected.activeMask[i] = active
        if active {
            selected.active.Modules = append(selected.active.Modules, mod)
        }
    }
    return selected, nil
}

func resolveTaggedConfig(ctx context.Context, selected taggedConfig) (config.Config, error) {
    u := ui.New(os.Stdout, os.Stderr)
    return registry.Resolve(ctx, selected.active, configFile, registry.ResolveOptions{NoCache: noCache}, u)
}
```

Provide a small raw-identifier helper for inactive diagnostics: prefer `Module.Name`; otherwise use `Module.From`. Match both explicit alias and `From` ref when checking whether a failed resolved-name lookup refers to a known inactive raw module. Do not fetch an inactive module merely to discover its remote internal name; an unmatched name remains the existing `module not found` usage error.

- [ ] **Step 5: Wire default enforcement and the explicit override**

Add one flag-binding owner used by `apply`, `directionCmd`, `verify`, and `status`:

```go
func addIgnoreTagsFlag(cmd *cobra.Command, target *bool) {
    cmd.Flags().BoolVar(target, "ignore-tags", false, "run modules even when their tag filters do not match")
}
```

Each constructor owns its local `ignoreTags bool`. It loads `taggedConfig`, resolves only `active`, assigns the already-loaded tags to the runner, and sets the explicit bypass:

```go
r.MachineTags = selected.machineTags
r.IgnoreTags = ignoreTags
```

Extend named selection to receive the raw tagged metadata. When a requested alias or `from` ref is in an inactive raw slot, return:

```go
usageErrorf("module %q is inactive: tag filters do not match; use --ignore-tags to override", name)
```

Unknown names keep the current `module %q not found in config` error. Active remote modules continue to resolve before ordinary name lookup, preserving their existing remote-defined names.

- [ ] **Step 6: Preserve runner defense in depth and add its RED/GREEN test**

Add the opt-in field and keep default behavior unchanged:

```go
type Runner struct {
    // existing fields...
    IgnoreTags bool
}

func (r *Runner) matchesTags(mod config.Module) bool {
    return r.IgnoreTags || tags.Matches(r.MachineTags, mod.OnlyTags, mod.ExcludeTags)
}
```

Extend the existing runner tag test to prove both branches: default `ApplyAll` / `VerifyAll` skip an inactive module, while `IgnoreTags: true` reaches it. This is the permanent regression for direct package callers and the explicit CLI override.

- [ ] **Step 7: Run focused tests and confirm GREEN**

Run:

```bash
go test ./internal/runner -run 'Test.*Tags' -count=1
go test ./cmd/dotular -run 'Test(NamedCommandsRejectTagInactiveModuleBeforeSideEffects|CommandsIgnoreTagsOnlyWhenExplicitlyRequested|TagFilteringPrecedesRegistryResolution|MalformedMachineTagsFailBeforeRegistryResolution|ApplyCmd|DirectionCmd|StatusCmd|VerifyCmd)' -count=1
```

Expected: PASS. Confirm the no-side-effect test observed zero requests and absent lock/cache/audit/marker paths rather than only checking a returned error.

- [ ] **Step 8: Commit and journal Task 1**

```bash
git add cmd/dotular/main.go cmd/dotular/main_test.go internal/runner/runner.go internal/runner/runner_test.go
git commit -m "fix(tags): enforce filters before module resolution"
mship journal "Task 1: tag classification now precedes registry resolution; named inactive modules fail with an explicit override; focused CLI and runner tests pass" --action committed --test-state pass
```
<!-- /mship:task -->

<!-- mship:task id=2 acs=ac5,ac6 -->
### Task 2: Show Inactive Modules Without Fetching Them

**Files:**
- Modify: `cmd/dotular/main.go:488-530`
- Modify: `cmd/dotular/main_test.go:605-621`

**Interfaces:**
- Consumes: Task 1 `taggedConfig`, `loadTaggedConfig(false)`, `resolveTaggedConfig`, `activeMask`, raw module identifiers, and existing `formatTypeCounts`.
- Produces: ordered `dotular list` output where active rows retain item counts and inactive rows render `skipped (tag mismatch)` without resolving inactive remotes.

- [ ] **Step 1: Write the failing observable list test**

Add:

```go
func TestListShowsInactiveModulesInOrderWithoutFetchingThem(t *testing.T) {
    // Isolate HOME/XDG_CACHE_HOME and capture cmd.OutOrStdout/ErrOrStderr.
    // Config order: active local "first", inactive aliased remote "second",
    // active local "third". The remote points at a counting httptest server.
    // Execute list, assert request count 0, no lock/cache files, and output names
    // occur first < second < third. Assert active rows retain item counts and
    // second contains "skipped (tag mismatch)" with no fabricated item count.
}
```

Also cover an inactive unaliased module so the row uses its `from` ref instead of an empty name.

- [ ] **Step 2: Run the list test and confirm RED**

Run:

```bash
go test ./cmd/dotular -run 'TestListShowsInactiveModulesInOrderWithoutFetchingThem' -count=1
```

Expected: FAIL because current `list` resolves every remote module and has no inactive annotation.

- [ ] **Step 3: Render active and inactive rows from one ordered walk**

Change `listCmd` to:

1. `loadTaggedConfig(false)` and fail immediately on config/machine-tag errors.
2. `resolveTaggedConfig` using only the active config.
3. Use `ui.New(cmd.OutOrStdout(), cmd.ErrOrStderr())` so output is observable through Cobra streams.
4. Walk `selected.raw.Modules` and `selected.activeMask` in config order, advancing a separate resolved-active index only for active slots.
5. Render active modules with the current count/breakdown text.
6. Render inactive modules with their alias or `from` ref and exactly `skipped (tag mismatch)`; never invent a count for an unresolved remote.

Keep this ordering check loud: if the resolved-active count does not match active slots, return an internal error instead of indexing incorrectly or silently dropping rows.

- [ ] **Step 4: Run focused list and ordinary registry tests**

Run:

```bash
go test ./cmd/dotular -run 'Test(List|OrdinaryCommandNoCacheRejectsDrift)' -count=1
```

Expected: PASS. The existing active `list --no-cache` checksum-verification contract must remain unchanged.

- [ ] **Step 5: Commit and journal Task 2**

```bash
git add cmd/dotular/main.go cmd/dotular/main_test.go
git commit -m "fix(tags): report inactive modules without fetching"
mship journal "Task 2: list preserves config order, counts active modules, annotates inactive modules, and proves zero remote/cache/lock work for inactive refs" --action committed --test-state pass
```
<!-- /mship:task -->

<!-- mship:task id=3 acs=ac1,ac2,ac3,ac4,ac5,ac6,ac7,ac8,ac9,ac10 -->
### Task 3: Document Semantics and Produce Merge Evidence

**Files:**
- Modify: `README.md` command reference and tagging section
- Add to branch unchanged from approved workspace artifact: `specs/2026-08-15-consistent-tag-filtering.md`
- Add to branch unchanged from approved workspace artifact: `docs/plans/2026-08-15-consistent-tag-filtering.md`

**Interfaces:**
- Consumes: Tasks 1-2 command behavior and approved Mothership spec `consistent-tag-filtering`.
- Produces: user-facing semantics, durable spec/plan, complete verification evidence, and a review-ready PR closing #26.

- [ ] **Step 1: Update the README with exact behavior**

Document all of the following in the existing command/tag sections, without creating a second conceptual section:

- `only_tags` / `exclude_tags` apply to all-module and explicitly named apply/push/pull/sync/verify invocations.
- Filtering occurs before remote module resolution, so inactive remote modules are not fetched and do not update cache or lock state.
- `--ignore-tags` is the explicit override on apply, push, pull, sync, verify, and status.
- `list` shows inactive modules as `skipped (tag mismatch)` and cannot show remote item counts without fetching them.
- `init` does not infer or add tag filters; users set policy explicitly with configuration and `dotular tag`.

- [ ] **Step 2: Run focused formatting and package tests**

Run:

```bash
gofmt -w cmd/dotular/main.go cmd/dotular/main_test.go internal/runner/runner.go internal/runner/runner_test.go
go test ./internal/tags ./internal/runner ./cmd/dotular -count=1
```

Expected: all packages PASS and `gofmt` produces no subsequent diff.

- [ ] **Step 3: Run full static and cross-platform verification**

Run:

```bash
go test ./... -count=1
go vet ./...
GOOS=windows GOARCH=amd64 go test -c -o /tmp/dotular-cli-tag-windows.test.exe ./cmd/dotular
GOOS=windows GOARCH=amd64 go test -c -o /tmp/dotular-runner-tag-windows.test.exe ./internal/runner
GOOS=darwin GOARCH=amd64 go test -c -o /tmp/dotular-cli-tag-darwin.test ./cmd/dotular
GOOS=darwin GOARCH=amd64 go test -c -o /tmp/dotular-runner-tag-darwin.test ./internal/runner
rm /tmp/dotular-cli-tag-windows.test.exe /tmp/dotular-runner-tag-windows.test.exe /tmp/dotular-cli-tag-darwin.test /tmp/dotular-runner-tag-darwin.test
```

Expected: full suite and vet exit 0; all four cross-compiles exit 0.

- [ ] **Step 4: Smoke the actual CLI with isolated state**

Build `cmd/dotular`, create an isolated `HOME` / `XDG_CACHE_HOME`, and exercise:

1. An inactive local module named explicitly: exits 2, identifies tag mismatch, and points to `--ignore-tags`; its marker remains absent.
2. The same command with `--ignore-tags --dry-run`: exits 0 and prints the module/action without executing it.
3. `list` over active/inactive/active modules: preserves order and annotates the inactive row.
4. A malformed `machine.yaml` with an unreachable registry ref: fails on machine-tag parsing before any lock/cache path appears.

Record exact commands, exit statuses, stdout/stderr, path-absence checks, and hashes of untouched config files as Mothership acceptance evidence.

- [ ] **Step 5: Copy approved artifacts and commit documentation**

Copy the exact approved workspace spec and plan into the feature worktree without editing their contents, then:

```bash
git add README.md specs/2026-08-15-consistent-tag-filtering.md docs/plans/2026-08-15-consistent-tag-filtering.md
git commit -m "docs: define consistent tag filtering semantics"
mship journal "Task 3: README documents named enforcement, pre-resolution filtering, explicit override, list annotation, and non-inference by init; full verification evidence recorded" --action committed --test-state pass
```

- [ ] **Step 6: Run Mothership verification and final review**

```bash
mship test
mship export --task consistent-tag-filtering
mship dispatch --mode reviewer --task consistent-tag-filtering
```

Address every material reviewer finding with focused RED/GREEN evidence, rerun affected/full checks, and journal the correction. Run hosted PR checks/review rounds until all checks pass and unresolved actionable threads are zero.

- [ ] **Step 7: Finish and open the PR**

Use `mship finish` with a body that summarizes tag semantics, side-effect ordering, list behavior, tests, and `Closes #26`. Confirm the PR is non-draft, merge state is clean, all checks pass, the description is complete, and review threads are resolved. Do not merge.
<!-- /mship:task -->
