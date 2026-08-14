# Fail Loud at Every Config Input Boundary Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reject malformed local and downloaded Dotular configuration deterministically before it can be ignored, reinterpreted, or cause user-state mutations.

**Approved spec:** `fail-loud-at-every-config-input-boundary` (GitHub issue #17; WorkItem `wi-20260814210145-95250681`)

## Assumptions checked

- repo topology — covered: one Go repository, `dotular`; config, registry, CLI, tests, and README change together.
- credential locus — N/A: no credentials, token handling, or authenticated service behavior changes.
- execution locus — covered: validation runs in the local CLI process at local YAML load/save, registry decode, and post-template render boundaries.
- state durability — covered: config files, module-store paths, cache bytes, lockfiles, snapshots, hooks, actions, and audit records are explicitly checked for pre-validation mutation.
- review surface — covered: one GitHub PR with focused Go tests, full-suite/vet evidence, cross-compilation, README changes, and actual CLI smoke output.
- agent stream — covered: each implementation task records its commit and focused test result in the task-resolved Mothership journal; final checks attach Mothership evidence.
- dispatched model — covered: Mothership context selects `inherit` for implementers and `sonnet` for reviewers; each task receives only its anchored plan section.

**Architecture:** `internal/config` owns semantic item and direction validation. Local YAML uses a root-kind probe followed by strict typed decoding; registry YAML uses strict decoding plus template-aware raw validation, then the shared strict validator after rendering. CLI and save paths invoke these boundaries before mutation rather than compensating in runners or actions.

**Tech Stack:** Go 1.23, `gopkg.in/yaml.v3`, Cobra, standard `testing`, existing registry staging/cache primitives, Mothership.

## Global Constraints

- Canonical YAML platform keys are exactly `macos`, `linux`, and `windows`; reject `darwin` and every other key without aliases or automatic renames.
- Preserve mapping-root and legacy sequence-root local configs, valid scalar/mapping `PlatformMap` forms, literal `~`, YAML null handling, empty configs, default-direction omission, and valid `Item.Type` / `EffectiveDirection` behavior.
- Local and fully rendered directions are empty or exactly `push`, `pull`, or `sync`; raw downloaded direction values containing Go-template actions are deferred to mandatory post-render validation.
- Every item has exactly one non-empty primary field from `package`, `script`, `setting`, `file`, `directory`, `binary`, and `run`.
- Reject local modules combining non-empty `from` and non-empty `items`; validate `override` items with the same semantic owner.
- Validation errors are deterministic and retain YAML line context plus module, collection (`items` or `override`), and item index context where available.
- Do not add a field-compatibility matrix, platform alias, broad runner/action refactor, command, dependency, or migration shim.
- The CodeGraph concept search found `Item.Type`, `Item.PrimaryValue`, `Item.EffectiveDirection`, `PlatformMap.UnmarshalYAML`, `Load`, and `Save`, but no validation owner; extend `internal/config` rather than creating a second convention.

---

<!-- mship:task id=1 acs=ac1,ac2,ac3,ac4,ac5,ac6,ac7,ac9,ac10,ac11,ac12,ac13,ac14,ac16,ac25,ac27 -->
### Task 1: Strict Local Decode and Semantic Validation

**Files:**
- Modify: `internal/config/config.go:1-327`
- Test: `internal/config/config_test.go:1-436`

**Interfaces:**
- Consumes: existing `Config`, `Module`, `Item`, `PlatformMap`, `DefaultDirection`, and YAML tags.
- Produces: `type ItemValidationOptions struct { AllowDirectionTemplates bool }`, `func ValidateDirection(direction string) error`, `func ValidateItems(items []Item, opts ItemValidationOptions) error`, `func (c Config) Validate() error`; strict `Load` and validation-before-write `Save`.

- [ ] **Step 1: Add table-driven failing tests for strict local decoding**

Add `TestLoadRejectsUnknownFields` to `internal/config/config_test.go`. Write each YAML case to `t.TempDir()/dotular.yaml`, call `Load`, and require both the misspelled key and `line ` in the error:

```go
tests := []struct {
    name string
    yaml string
    key  string
}{
    {"root", "moduels: []\n", "moduels"},
    {"module", "modules:\n  - name: test\n    itmes: []\n", "itmes"},
    {"module hooks", "modules:\n  - name: test\n    hooks:\n      before_aply: echo bad\n    items: []\n", "before_aply"},
    {"item", "modules:\n  - name: test\n    items:\n      - packge: git\n", "packge"},
    {"item hooks", "modules:\n  - name: test\n    items:\n      - package: git\n        hooks:\n          after_aply: echo bad\n", "after_aply"},
}
```

Also add a legacy-root case (`- name: test`) with an unknown module field to prove `KnownFields(true)` applies to both supported roots.

- [ ] **Step 2: Add failing tests for platform-map keys and compatibility**

Extend the existing `PlatformMap` tests with:

```go
func TestPlatformMapUnmarshalRejectsUnknownAndDuplicateKeys(t *testing.T) {
    tests := []struct {
        name string
        yaml string
        want string
    }{
        {"runtime darwin spelling", "darwin: ~/.config\n", `unknown platform key "darwin"`},
        {"arbitrary key", "freebsd: ~/.config\n", `unknown platform key "freebsd"`},
        {"duplicate canonical key", "macos: one\nmacos: two\n", `duplicate platform key "macos"`},
    }
    // yaml.Unmarshal each case and require want plus the offending key's line.
}
```

Keep and expand the existing scalar, mapping, literal-tilde, null-spelling, and invalid-node tests. Add `TestLoadSupportedRootShapes` covering `""`, `"{}\n"`, `"[]\n"`, a valid mapping config, and a valid legacy sequence config.

- [ ] **Step 3: Add failing tests for item and module semantics**

Add direct tests for `ValidateDirection`, `ValidateItems`, and `Config.Validate`. Cover all seven single-primary success cases; zero primaries; each representative conflicting pair (`package+file`, `script+run`); empty/default/pull/sync direction success; `pul` failure; `from+items` failure; valid `from+override`; invalid override item; and stable first-failure ordering. Require errors such as:

```text
module 2 ("shell"): items: item 1: expected exactly one primary field; found package, file
module 1 ("remote"): override: item 2: direction "pul" must be push, pull, or sync
```

The exact punctuation may follow existing Go error style, but tests must assert module number/name, collection, one-based item index, offending field/value, and deterministic first-failure selection.

- [ ] **Step 4: Add failing `Load` tests for all six issue examples**

Use one table to load `packge`, `destination: {darwin: ~/.config}`, `package+file`, an item with only `verify`, `from+items`, and `direction: pul`. Assert every case fails in `Load`, not `Item.Type`, runner construction, or command execution. This is the regression test that must fail against `main` before implementation.

- [ ] **Step 5: Add failing `Save` no-mutation tests**

Add `TestSaveRejectsInvalidConfigBeforeWrite` with two subtests:

```go
existing := []byte("sentinel: unchanged\n")
invalid := Config{Modules: []Module{{Name: "bad", Items: []Item{{Verify: "true"}}}}}
```

For an existing destination, require `Save` error and byte-for-byte `existing` content. For a missing destination, require `Save` error and `errors.Is(os.Stat(path), fs.ErrNotExist)`. Do not use an unwritable-directory proxy; the contract is validation ordering.

- [ ] **Step 6: Run the focused tests and confirm the intended failures**

Run:

```bash
go test ./internal/config -run 'Test(LoadRejects|LoadSupported|PlatformMapUnmarshalRejects|Validate|ConfigValidate|SaveRejects)' -count=1
```

Expected before implementation: failures showing permissive decode, ignored platform keys, missing validators, and `Save` writing invalid state.

- [ ] **Step 7: Implement the shared validators in `internal/config/config.go`**

Add `strings` for field-name joins and template detection. Use a fixed stack array for primary fields; do not add reflection or a second schema:

```go
type ItemValidationOptions struct {
    AllowDirectionTemplates bool
}

func ValidateDirection(direction string) error {
    switch direction {
    case "push", "pull", "sync":
        return nil
    default:
        return fmt.Errorf("direction %q must be push, pull, or sync", direction)
    }
}

func ValidateItems(items []Item, opts ItemValidationOptions) error {
    for itemIndex, item := range items {
        fields := [...]struct{ name, value string }{
            {"package", item.Package},
            {"script", item.Script},
            {"setting", item.Setting},
            {"file", item.File},
            {"directory", item.Directory},
            {"binary", item.Binary},
            {"run", item.Run},
        }
        names := make([]string, 0, 2)
        for _, field := range fields {
            if field.value != "" {
                names = append(names, field.name)
            }
        }
        if len(names) != 1 {
            found := "none"
            if len(names) != 0 {
                found = strings.Join(names, ", ")
            }
            return fmt.Errorf("item %d: expected exactly one primary field; found %s", itemIndex+1, found)
        }
        if item.Direction != "" &&
            !(opts.AllowDirectionTemplates && strings.Contains(item.Direction, "{{")) {
            if err := ValidateDirection(item.Direction); err != nil {
                return fmt.Errorf("item %d: %w", itemIndex+1, err)
            }
        }
    }
    return nil
}
```

Implement `Config.Validate` as an index-ordered loop. Build `module N` or `module N ("name")` once; reject `mod.From != "" && len(mod.Items) != 0`; wrap `ValidateItems(mod.Items, ItemValidationOptions{})` with `items`, then wrap overrides with `override`. Empty item slices remain valid.

- [ ] **Step 8: Make `PlatformMap.UnmarshalYAML` reject unknown and duplicate keys**

Keep scalar/null behavior byte-for-byte. In the mapping arm, use three booleans (`seenMacOS`, `seenWindows`, `seenLinux`) rather than allocating a map. For each key, reject a duplicate at `value.Content[i].Line`; in the `default` arm return:

```go
return fmt.Errorf("line %d: unknown platform key %q (want macos, linux, or windows)", keyNode.Line, key)
```

Do not accept `darwin`; runtime `GOOS=darwin` remains mapped only by `PlatformMap.ForOS`.

- [ ] **Step 9: Replace permissive local decoding and validate before return/write**

Add `bytes`. Preserve the initial `yaml.Node` root probe. After selecting mapping or sequence, construct one decoder over the original bytes and enable strict fields:

```go
decoder := yaml.NewDecoder(bytes.NewReader(data))
decoder.KnownFields(true)
switch doc.Kind {
case yaml.MappingNode:
    if err := decoder.Decode(&cfg); err != nil { /* wrap parse config */ }
case yaml.SequenceNode:
    if err := decoder.Decode(&cfg.Modules); err != nil { /* wrap legacy context */ }
default:
    // preserve nodeKindName error
}
if err := cfg.Validate(); err != nil {
    return Config{}, fmt.Errorf("validate config: %w", err)
}
```

At the first line of `Save`, call `cfg.Validate()` and wrap `validate config`; only then marshal and call `os.WriteFile`. Do not alter `Item.Type`, `PrimaryValue`, or `EffectiveDirection`.

- [ ] **Step 10: Run focused config tests, format, and commit**

Run:

```bash
gofmt -w internal/config/config.go internal/config/config_test.go
go test ./internal/config -count=1
git add internal/config/config.go internal/config/config_test.go
mship commit "fix(config): reject malformed input at load and save"
mship journal "Task 1: strict local decoding, platform keys, semantic item validation, and validation-before-save implemented; internal/config tests pass" --action committed --test-state pass
```

Expected: all `internal/config` tests pass; existing scalar, null, legacy, `Type`, and `EffectiveDirection` tests remain green.
<!-- /mship:task -->

<!-- mship:task id=2 acs=ac8,ac20,ac23,ac24,ac27 -->
### Task 2: Strict Registry Decode Before Fetch Publication

**Files:**
- Modify: `internal/registry/fetch.go:1-181`
- Test: `internal/registry/fetch_test.go`
- Test: `internal/registry/fetch_network_test.go:29-520`

**Interfaces:**
- Consumes: Task 1 `config.ValidateItems(items, config.ItemValidationOptions{AllowDirectionTemplates: true})` and existing `fetchNoWrite` ordering.
- Produces: strict `parseModule([]byte) (*RemoteModule, error)` that rejects unknown fields, invalid primary cardinality, and invalid literal directions before network data can update an in-memory pin or cache file.

- [ ] **Step 1: Add failing unit tests for strict `RemoteModule` decoding**

In `fetch_test.go`, add `TestParseModuleRejectsUnknownFields` with root (`nmae`), parameter (`descrption`), item (`packge`), and item-hook (`after_aply`) cases. Require `parse registry module`, the key, and decoder line context. Add `TestParseModuleValidatesRawItems` for zero/multiple primary fields and literal `direction: pul`; require module name and one-based item context.

- [ ] **Step 2: Protect supported direction templates in a failing compatibility test**

Add:

```go
func TestParseModuleDefersTemplatedDirection(t *testing.T) {
    mod, err := parseModule([]byte(
        "name: templated\nparams:\n  dir:\n    default: push\nitems:\n  - file: config\n    direction: '{{ .dir }}'\n",
    ))
    if err != nil {
        t.Fatalf("parseModule() error = %v", err)
    }
    if got := mod.Items[0].Direction; got != "{{ .dir }}" {
        t.Fatalf("direction = %q", got)
    }
}
```

Also verify `push-{{ .suffix }}` is deferred; final validation belongs to Task 3.

- [ ] **Step 3: Add network-path no-publication tests**

In `fetch_network_test.go`, create an unpinned TLS test module for each parse-time failure class (unknown item key, zero primary, multiple primary, invalid literal direction). Set `HOME` to `t.TempDir()`, call `Fetch`, and assert:

```go
if len(lock.Registry) != 0 { t.Fatalf("rejected module pinned: %#v", lock.Registry) }
if _, err := os.Stat(moduleCachePath(ref)); !errors.Is(err, fs.ErrNotExist) {
    t.Fatalf("cache state after rejected module = %v", err)
}
```

Add a pinned matching-cache case containing invalid module bytes. Require checksum verification to succeed first, parse to fail second, and the existing lock entry to remain byte-for-byte unchanged. Existing bad cache bytes need not be deleted; Fetch did not publish them.

- [ ] **Step 4: Run registry decode tests and confirm failures**

Run:

```bash
go test ./internal/registry -run 'Test(ParseModule|FetchRejectsMalformedConfig|FetchPinnedInvalidCache)' -count=1
```

Expected before implementation: unknown fields decode successfully, semantic cases return modules, and at least the no-publication assertions fail.

- [ ] **Step 5: Implement strict, template-aware `parseModule`**

Add `bytes` to `fetch.go`; retain the existing `config` import. Replace `yaml.Unmarshal` with:

```go
decoder := yaml.NewDecoder(bytes.NewReader(data))
decoder.KnownFields(true)
if err := decoder.Decode(&mod); err != nil {
    return nil, fmt.Errorf("parse registry module: %w", err)
}
if err := config.ValidateItems(mod.Items, config.ItemValidationOptions{
    AllowDirectionTemplates: true,
}); err != nil {
    name := mod.Name
    if name == "" {
        name = "<unnamed>"
    }
    return nil, fmt.Errorf("validate registry module %q: items: %w", name, err)
}
```

Do not move checksum verification. `fetchNoWrite` already parses before returning; `Fetch` mutates `lock.Registry` and writes cache only after `fetchNoWrite` succeeds. Preserve cache-hit checksum-before-parse ordering and warning behavior for non-fatal cache write failures.

- [ ] **Step 6: Run focused and package tests, format, and commit**

Run:

```bash
gofmt -w internal/registry/fetch.go internal/registry/fetch_test.go internal/registry/fetch_network_test.go
go test ./internal/registry -run 'Test(ParseModule|Fetch)' -count=1
go test ./internal/registry -count=1
git add internal/registry/fetch.go internal/registry/fetch_test.go internal/registry/fetch_network_test.go
mship commit "fix(registry): validate downloaded module definitions"
mship journal "Task 2: strict registry decode and raw item validation now precede Fetch publication; templated directions remain supported; registry tests pass" --action committed --test-state pass
```
<!-- /mship:task -->

<!-- mship:task id=3 acs=ac21,ac22,ac23,ac24,ac27 -->
### Task 3: Mandatory Post-Template Validation

**Files:**
- Modify: `internal/registry/resolver.go:139-150`
- Test: `internal/registry/resolver_test.go:81-94`
- Test: `internal/registry/update_test.go:691-735`

**Interfaces:**
- Consumes: Task 1 strict `config.ValidateItems(items, config.ItemValidationOptions{})`; existing `renderItems` callers in ordinary `Resolve` and staged `CheckPins` / `UpdatePins`.
- Produces: `renderItems` returns only fully rendered, semantically valid item slices; no caller can merge or use invalid rendered values.

- [ ] **Step 1: Add failing focused tests for rendered semantic failures**

Extend `resolver_test.go` with `TestRenderItemsValidatesRenderedValues`. Table cases:

```go
{
    name:   "primary renders empty",
    items:  []config.Item{{Package: "{{ .package }}"}},
    params: map[string]any{"package": ""},
    want:   "expected exactly one primary field",
},
{
    name:   "direction renders invalid",
    items:  []config.Item{{File: "config", Direction: "{{ .direction }}"}},
    params: map[string]any{"direction": "pul"},
    want:   `direction "pul"`,
},
{
    name:   "multiple primaries rejected",
    items:  []config.Item{{Package: "git", File: "config"}},
    params: nil,
    want:   "package, file",
},
```

Require a nil result on error. Add a success case where templated direction renders to `sync` and the returned item contains `sync`.

- [ ] **Step 2: Add a staged update/check no-mutation regression**

Adapt `TestStageActiveRefsValidatesEverySharedRefUsageAfterUniqueFetchesWithoutWrites` so one shared module has a templated `direction`, one usage renders `sync`, and another renders `pul`. Require the error to identify the ref, local module name, rendered item index, and invalid direction; require `stageActiveRefs` to return nil and `fixture.requireDurableStateUnchanged()`.

Keep the existing invalid-quote render test as a separate subtest or sibling; it defends template unmarshalling behavior, while this case defends semantic validation. One fetch per unique ref must remain true.

- [ ] **Step 3: Run focused tests and confirm the semantic gap**

Run:

```bash
go test ./internal/registry -run 'Test(RenderItemsValidatesRenderedValues|StageActiveRefsValidatesEverySharedRefUsage)' -count=1
```

Expected before implementation: invalid rendered direction and empty primary return successfully from `renderItems`.

- [ ] **Step 4: Validate once at the shared renderer return point**

After rendering every item and before returning the slice, add:

```go
if err := config.ValidateItems(rendered, config.ItemValidationOptions{}); err != nil {
    return nil, fmt.Errorf("validate rendered items: %w", err)
}
return rendered, nil
```

Do not add validation separately in `Resolve`, `stageActiveRefs`, `CheckPins`, or `UpdatePins`: all three behaviors already traverse `renderItems`, and a second call would create drift. Keep overrides validated by local `Config.Validate` before registry resolution.

- [ ] **Step 5: Run resolver/update/package tests, format, and commit**

Run:

```bash
gofmt -w internal/registry/resolver.go internal/registry/resolver_test.go internal/registry/update_test.go
go test ./internal/registry -run 'Test(RenderItems|StageActiveRefs|CheckPins|UpdatePins)' -count=1
go test ./internal/registry -count=1
git add internal/registry/resolver.go internal/registry/resolver_test.go internal/registry/update_test.go
mship commit "fix(registry): validate items after template rendering"
mship journal "Task 3: the shared registry renderer now rejects empty/conflicting primaries and invalid rendered directions for resolve, update, and check; registry tests pass" --action committed --test-state pass
```
<!-- /mship:task -->

<!-- mship:task id=4 acs=ac6,ac15,ac17,ac18,ac19,ac24,ac27 -->
### Task 4: CLI Preflight and No-Side-Effect Command Behavior

**Files:**
- Modify: `cmd/dotular/main.go:194-337`
- Test: `cmd/dotular/main_test.go:1554-1770,1857-1926`

**Interfaces:**
- Consumes: Task 1 `config.ValidateDirection`, strict `loadConfig`, and validation-before-write `config.Save`.
- Produces: `dotular add` rejects invalid direction with `usageError` and validates existing config before inference/network or filesystem mutation; all config-consuming commands surface load failures before their action path.

- [ ] **Step 1: Add a failing invalid-direction process contract test**

Add `TestAddCmdRejectsInvalidDirectionBeforeMutation`. Create a valid config, a real source file, and an absent module directory; snapshot config bytes. Execute:

```go
root := buildRoot()
root.SetArgs([]string{"add", sourcePath, "shell", "--config", cfgPath, "--direction", "pul"})
err := root.Execute()
```

Require `exitCode(err) == exitUsage`, error text containing `--direction` and `pul`, identical config bytes, and `errors.Is(os.Stat(filepath.Join(filepath.Dir(cfgPath), "shell")), fs.ErrNotExist)`.

- [ ] **Step 2: Add a failing invalid-existing-config ordering test**

Add `TestAddCmdValidatesConfigBeforeCopy`. Put `packge: git` in the existing config, create a source file, execute valid `add`, and require `exitFailure`, unchanged config bytes, no module directory, and no copied destination. This test must fail against the current copy-before-load ordering.

- [ ] **Step 3: Add a malformed-config command routing table**

Add `TestConfigCommandsRejectMalformedInputBeforeWork`. Use a config with an unknown key plus a `run` item that would create a marker if reached. Execute fresh roots for:

```go
[][]string{
    {"apply"}, {"push"}, {"pull"}, {"sync"},
    {"list"}, {"status"}, {"verify"},
    {"registry", "update"},
    {"registry", "update", "--check"},
}
```

Append `--config` and `cfgPath` using the ordering accepted by existing command tests. Require a non-nil load error containing the unknown key and that the marker, lockfile, and module store remain absent. For the registry cases, replace `httputil.Client.Transport` with a `commandRoundTripFunc` that fails the test on any request, then restore it with `t.Cleanup`; observe zero requests. This table proves command routing; `loadAndResolveConfig` covers registry resolution used by apply/push/pull/sync/status/verify.

- [ ] **Step 4: Preserve successful add output and serialization**

Extend `TestAddCmdWithDirection` into subtests for default `push`, explicit `pull`, and explicit `sync`. Assert:

- default `push` is omitted from saved YAML;
- `pull` and `sync` are serialized exactly;
- existing success/store/config output strings remain present;
- the stored `Item` passes `config.ValidateItems`.

Do not weaken existing file, directory, link, existing-module, missing-path, or symlink-preservation tests.

- [ ] **Step 5: Run focused CLI tests and confirm current failures**

Run:

```bash
go test ./cmd/dotular -run 'Test(AddCmd|ConfigCommandsRejectMalformedInput)' -count=1
```

Expected before implementation: `pul` is serialized, invalid existing config leaves copied content, and malformed cases show inconsistent late behavior.

- [ ] **Step 6: Preflight direction and config at the top of `addCmd.RunE`**

Immediately after context initialization:

```go
if err := config.ValidateDirection(direction); err != nil {
    return usageErrorf("invalid --direction: %v", err)
}
cfg, err := loadConfig()
if err != nil {
    if !errors.Is(err, fs.ErrNotExist) {
        return err
    }
    cfg = config.Config{}
}
```

Do this before module inference because inference may fetch and cache registry data. Delete the old load block after the copy. Keep path resolution/stat before mutation, but use the preloaded `cfg` when appending the item. Preserve default-direction omission and existing output.

- [ ] **Step 7: Run CLI and integration-focused tests, format, and commit**

Run:

```bash
gofmt -w cmd/dotular/main.go cmd/dotular/main_test.go
go test ./cmd/dotular -run 'Test(AddCmd|ConfigCommandsRejectMalformedInput|ExitCode)' -count=1
go test ./cmd/dotular ./internal/config ./internal/registry -count=1
git add cmd/dotular/main.go cmd/dotular/main_test.go
mship commit "fix(cli): validate add inputs before mutation"
mship journal "Task 4: add direction/config preflight and malformed-config command routing are enforced before filesystem, network, action, lock, or audit work; focused CLI and registry tests pass" --action committed --test-state pass
```
<!-- /mship:task -->

<!-- mship:task id=5 acs=ac10,ac11,ac15,ac19,ac22,ac24,ac25,ac26,ac27,ac28 -->
### Task 5: Documentation and End-to-End Acceptance

**Files:**
- Create from the approved Mothership workspace artifacts: `specs/2026-08-14-fail-loud-at-every-config-input-boundary.md`
- Create from the approved Mothership workspace artifacts: `docs/plans/2026-08-14-fail-loud-at-every-config-input-boundary.md`
- Modify: `README.md:97-137,259-304,350-360`
- Verify: all changed Go packages and the built `dotular` CLI

**Interfaces:**
- Consumes: Tasks 1-4 completed behavior and all 28 approved acceptance criteria.
- Produces: documented canonical YAML vocabulary, repository-wide verification evidence, cross-platform compile evidence, and actual CLI smoke evidence ready for PR review.

- [ ] **Step 1: Document the platform vocabulary without an alias**
Before editing README, materialize the approved planning artifacts on the feature branch by copying these exact workspace files without changing their contents:

```text
/home/bailey/development/repos/dotular/specs/2026-08-14-fail-loud-at-every-config-input-boundary.md
/home/bailey/development/repos/dotular/docs/plans/2026-08-14-fail-loud-at-every-config-input-boundary.md
```

Write them to the corresponding `specs/` and `docs/plans/` paths in the task worktree so the PR carries the approved spec and implementation plan.


In README configuration documentation, add a compact note adjacent to per-platform maps:

```markdown
Per-platform YAML maps accept only `macos`, `linux`, and `windows`. The
`dotular platform` command prints Go runtime names, so it prints `darwin` on
macOS; use `macos:` in YAML, not `darwin:`.
```

In the `add` flag table, state that `--direction` accepts only `push`, `pull`, or `sync`. Do not add migration or alias language.

- [ ] **Step 2: Run formatting and focused regression suites**

Run:

```bash
gofmt -w internal/config/config.go internal/config/config_test.go \
  internal/registry/fetch.go internal/registry/fetch_test.go \
  internal/registry/fetch_network_test.go internal/registry/resolver.go \
  internal/registry/resolver_test.go internal/registry/update_test.go \
  cmd/dotular/main.go cmd/dotular/main_test.go
go test ./internal/config ./internal/registry ./cmd/dotular -count=1
```

Expected: all three packages pass with uncached results.

- [ ] **Step 3: Run the complete repository checks**

Run:

```bash
go test ./... -count=1
go vet ./...
```

Then run Mothership's configured validation from the task worktree:

```bash
mship test | tee /tmp/fail-loud-mship-test.json
```

Expected: every package passes, vet emits no diagnostics, and Mothership records a passing test iteration.

- [ ] **Step 4: Cross-compile affected packages for supported targets**

Use throwaway outputs outside the repository:

```bash
GOOS=windows GOARCH=amd64 go test -c -o /tmp/dotular-config-windows.test.exe ./internal/config
GOOS=windows GOARCH=amd64 go test -c -o /tmp/dotular-registry-windows.test.exe ./internal/registry
GOOS=windows GOARCH=amd64 go test -c -o /tmp/dotular-cli-windows.test.exe ./cmd/dotular
GOOS=darwin GOARCH=amd64 go test -c -o /tmp/dotular-config-darwin.test ./internal/config
GOOS=darwin GOARCH=amd64 go test -c -o /tmp/dotular-registry-darwin.test ./internal/registry
GOOS=darwin GOARCH=amd64 go test -c -o /tmp/dotular-cli-darwin.test ./cmd/dotular
```

Linux is exercised by the native full suite. Remove the six named `/tmp/dotular-*-windows.test.exe` and `/tmp/dotular-*-darwin.test` binaries after recording success.

- [ ] **Step 5: Smoke the actual CLI with valid and malformed config**

Build `./cmd/dotular` as `$smoke_dir/dotular`. Create `$smoke_dir/source` and run in isolated `HOME=$smoke_dir/home` and `XDG_CACHE_HOME=$smoke_dir/cache` directories:

1. Run `$smoke_dir/dotular list --config $smoke_dir/valid.yaml` with a valid local package item and require exit 0 plus the module/type count.
2. Run `$smoke_dir/dotular list --config $smoke_dir/typo.yaml` containing `packge: git`; require exit 1, the `packge` key and line in stderr, and no cache/lock/audit/module-store files.
3. Run `$smoke_dir/dotular add $smoke_dir/source shell --config $smoke_dir/valid.yaml --direction pul`; require exit 2, byte-identical config, and absent `shell/` store.
4. Run `$smoke_dir/dotular add $smoke_dir/source shell --config $smoke_dir/valid.yaml --direction sync`; require exit 0, copied store content, and `direction: sync` in YAML.

Record exact commands and exit statuses in `mship journal`; do not commit smoke fixtures. Registry post-render and staged no-mutation behavior is exercised by Task 3's deterministic in-process HTTP tests rather than an external-network CLI smoke.

- [ ] **Step 6: Commit documentation and record acceptance evidence**

Run:

```bash
git add README.md specs/2026-08-14-fail-loud-at-every-config-input-boundary.md docs/plans/2026-08-14-fail-loud-at-every-config-input-boundary.md
mship commit "docs: document strict config vocabulary"
mship journal "Task 5: README documents canonical platform keys/directions; full suite, vet, Windows/Darwin cross-compilation, Mothership validation, and actual CLI valid/malformed/add smoke pass" --action committed --test-state pass
```

Read the exact iteration from `/tmp/fail-loud-mship-test.json`, then attach that test run to every criterion:

```bash
iteration=$(jq -r '.iteration' /tmp/fail-loud-mship-test.json)
for criterion in $(seq 1 28); do
  mship spec evidence fail-loud-at-every-config-input-boundary "ac${criterion}" "test-runs/${iteration}.dotular" --kind test
done
```

Add focused evidence notes for CLI smoke and cross-compilation where the full suite alone does not exercise the observable contract.

- [ ] **Step 7: Run independent review and finish through Mothership**

Dispatch a read-only final reviewer against the complete branch and approved spec. Fix every actionable correctness or missing-test finding with a focused regression, rerun Steps 2-5, commit, and journal the correction. When review is approved and hosted checks are ready:

```bash
mship finish
```

Use the generated PR body; verify the PR description names issue #17, the templated-direction correction, test plan, no-side-effect guarantees, and `Fixes #17`.
<!-- /mship:task -->
