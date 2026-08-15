# Compensating and Interruption-Safe Rollback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Spec:** `compensating-rollback`

**Goal:** Replace file-only best-effort rollback with one truthful per-module transaction that compensates every supported side effect, unwinds on errors, cancellation, and panic, and reports what was restored, failed, or could not be compensated.

## Assumptions checked

- repo topology — covered: one Go repository (`dotular`), one Mothership WorkItem, one feature worktree, and one PR closing issue #7.
- credential locus — N/A: package/setting capture and rollback invoke the same local system tools as forward actions; tests inject command execution or use isolated temporary files.
- execution locus — covered: development and smoke tests run in the Mothership worktree; hosted CI runs the existing Go matrix and security/review checks.
- state durability — covered: this is an in-process transaction only. SIGKILL, power loss, kernel failure, and process death remain explicitly outside the recovery model because no durable restart journal is added.
- review surface — covered: one coherent PR updates config, actions, snapshotting, runner lifecycle, CLI cancellation, audit/UI output, tests, and README documentation.
- agent stream — covered: execute the dependency-ordered tasks below, recording Mothership test and journal evidence after each coherent commit.
- dispatched model — covered: Task 2 may proceed independently after Task 1; Tasks 3 and 4 share the compensation contract and therefore run in order; Tasks 5–8 consume all earlier interfaces.

**Architecture:** Add a narrow optional `actions.CompensationPreparer` capability rather than replacing the existing `Action` lifecycle. The runner owns a per-module `moduleTransaction`: preflight resolves applicable actions, validates explicit rollback commands, snapshots every declared filesystem write including the binary destination, and captures typed package/setting state before the first forward hook. Each hook/action activates its prepared compensation immediately before it is attempted. Failure, cancellation, or panic drains one LIFO journal using a fresh bounded context, continuing after compensation failures; filesystem restoration is the oldest entry and therefore runs last. `--no-atomic` bypasses preflight and the journal entirely.

**Tech Stack:** Go 1.23, Cobra, `context`, `os/signal`, existing `internal/config`, `internal/actions`, `internal/snapshot`, `internal/shell`, `internal/runner`, `internal/audit`, `internal/ui`, Go `testing`, Mothership.

## Global Constraints

- Preserve current forward order: module `before_apply`; module `before_sync` when applicable; per-item `before_apply`, `before_sync`, action, verify, `after_sync`, `after_apply`; module `after_sync`; module `after_apply`.
- Atomic preflight may move read-only `skip_if` and idempotency/state checks ahead of module hooks so no mutation starts before compensation is prepared. `--no-atomic` retains current direct-execution ordering and produces no rollback warning, state capture, snapshot, or compensation command.
- Register compensation before the corresponding forward attempt. A failed attempted step is compensated; compensation is not limited to successful steps.
- Automatic typed compensation takes precedence. `Item.Rollback` is a fallback for package/setting capture that is unavailable, and the primary compensation for script/run. Reject `Item.Rollback` on file, directory, and binary actions because snapshot ownership is unambiguous there.
- A forward hook may declare only its matching rollback hook. Missing rollback for a mutating hook/script/run action warns and continues in default atomic mode; it does not reject the module.
- Package uninstall is permitted only after a conclusive absent pre-state. A present package is skipped. Unsupported, imprecise, malformed, or failed state checks never authorize uninstall; use an explicit fallback when present, otherwise record an uncompensated operation.
- Setting compensation restores the prior presence, type, and value when capture is conclusive, or deletes only a key proven absent before the transaction. Unknown state never guesses.
- Keep one two-minute default rollback deadline. No retries. A second termination signal restores normal process termination immediately.
- Preserve the original forward error or panic. Join rollback, restore, discard, and deadline failures without hiding the original. Rollback proceeds through all remaining entries while the cleanup context permits.
- A failed transaction reports zero committed `applied` actions. Rollback counts are compensation-operation counts, not a claim that every forward side effect was reversible.
- No package dependency rollback, automatic reversal of undeclared arbitrary commands, durable recovery log, database transaction claim, compatibility alias, or rename of `--no-atomic`.
- RED/GREEN first for every observable contract; each coherent task ends with focused tests, formatting, a commit, and Mothership journal evidence.

---

<!-- mship:task id=1 acs=ac1,ac8 -->
### Task 1: Add Strict Rollback Configuration Contracts

**Files:**
- Modify: `internal/config/config.go:28-117,195-249`
- Modify: `internal/config/config_test.go` (strict decoding and validation tables)
- Modify: `internal/template/template_test.go` (item string rendering coverage)
- Modify: `internal/registry/fetch_test.go` (strict remote-module decoding)
- Modify: `internal/registry/resolve_integration_test.go` or the nearest existing rendered-item validation test

**Interfaces:**
- Produces: `config.RollbackHooks`, `config.ModuleHooks.Rollback`, `config.ItemHooks.Rollback`, and `config.Item.Rollback`.
- Preserves: generic `template.RenderItem` YAML rendering and `registry.renderItems` post-render `config.ValidateItems` boundary; do not add field-by-field template code.
- Validation contract: rollback hooks require their corresponding forward hook; whitespace-only commands fail; item rollback is allowed only for package, setting, script, and run items.

- [ ] **Step 1: Inspect symbol-aware impact before changing exported structs**

Use LSP references when available; otherwise use CodeGraph impact/exploration for `ModuleHooks`, `ItemHooks`, `Item`, `ValidateItems`, `Config.Validate`, and `RenderItem`. Confirm all struct literals and test fixtures that may need migration. Do not hand-search call sites after a symbol-aware result is available.

- [ ] **Step 2: Write failing strict-decoding and validation tests**

Add table-driven tests proving both local mapping/legacy YAML and remote registry YAML accept:

```yaml
hooks:
  before_apply: prepare-workspace
  after_apply: finalize-workspace
  rollback:
    before_apply: undo-prepare
    after_apply: undo-finalize
items:
  - run: install-custom-service
    rollback: uninstall-custom-service
    hooks:
      before_apply: prepare-item
      rollback:
        before_apply: undo-prepare-item
```

Add failures for:

- an unknown nested rollback key at module and item scope;
- a rollback hook with no corresponding forward hook;
- whitespace-only rollback commands;
- `rollback` on file, directory, and binary items;
- a rendered registry rollback command that becomes empty;
- a rendered rollback command containing missing template parameters.

Assert errors retain module/item indexes and the offending hook/action field.

- [ ] **Step 3: Confirm RED at every parsing boundary**

Run focused config, template, and registry tests. Expected: rollback keys are rejected by strict YAML decoding or omitted from rendered items because the schema does not exist.

- [ ] **Step 4: Add one reusable rollback-hook shape and structural validation**

In `internal/config/config.go`, add:

```go
type RollbackHooks struct {
    BeforeApply string `yaml:"before_apply,omitempty"`
    AfterApply  string `yaml:"after_apply,omitempty"`
    BeforeSync  string `yaml:"before_sync,omitempty"`
    AfterSync   string `yaml:"after_sync,omitempty"`
}
```

Embed it as a named `Rollback RollbackHooks` field in both existing hook structs, and add `Rollback string` to `Item` beside other shared execution fields. Keep `ModuleHooks` and `ItemHooks` because they are established exported types; reuse a private validator rather than adding a third public hooks abstraction.

Extend `ValidateItems` to reject unsupported item rollback placement and validate item hook pairs. Extend `Config.Validate` to validate module hook pairs. Structural validation must permit `{{ ... }}` text at the remote parse boundary but re-run after `RenderItem` through the existing `renderItems` validation call.

- [ ] **Step 5: Prove automatic template and registry propagation**

Do not modify `internal/template/template.go` or `internal/registry/resolver.go` if the generic YAML round-trip already passes the new tests. Verify item rollback and nested item rollback-hook strings render with parameters and survive override merging. Verify strict unknown-key rejection still occurs before registry publication.

- [ ] **Step 6: Format, run focused GREEN tests, commit, and journal**

Run `gofmt` only on touched Go files, rerun the focused packages, commit the config contract, and record exact tests plus the no-new-rendering-owner decision in the Mothership journal.

<!-- /mship:task -->

---

<!-- mship:task id=2 acs=ac2,ac4,ac9,ac15 -->
### Task 2: Make Filesystem Capture Complete and Deterministic

**Files:**
- Modify: `internal/snapshot/snapshot.go:14-119`
- Modify: `internal/snapshot/snapshot_test.go`
- Modify: `internal/actions/binary.go:22-98`
- Modify: `internal/actions/binary_test.go`

**Interfaces:**
- Preserves: `snapshot.New`, `Snapshot.Record`, `Snapshot.Restore`, `Snapshot.Discard`, and `actions.PathWriter`.
- Produces: `(*BinaryAction).WritePaths() []string` returning the exact expanded installed binary path.
- Snapshot invariant: records restore in deterministic reverse capture order; the first absent ancestor for a missing write path is removed on rollback; all restore/remove failures are joined.

- [ ] **Step 1: Inspect `PathWriter`, all implementations, and snapshot callers**

Use CodeGraph/LSP to inspect `actions.PathWriter`, `FileAction.WritePaths`, `DirectoryAction.WritePaths`, `BinaryAction`, `Snapshot.Record`, `Snapshot.Restore`, and every caller. Confirm file/directory direction handling remains owned by their current `WritePaths` methods.

- [ ] **Step 2: Write failing snapshot regressions**

Add tests for:

- a write target whose parent and grandparent do not exist; rollback removes the highest ancestor created beneath the nearest pre-existing parent;
- two targets beneath the same absent ancestor; removal is deduplicated and deterministic;
- nested saved paths restored in reverse capture order to the exact pre-module bytes, symlink targets, and modes;
- multiple restore/remove failures returned through `errors.Is` for every sentinel, not only the first;
- `Discard` failure remaining observable through its returned error.

Use package-local filesystem seams only where the OS cannot naturally produce a deterministic failure; do not add a broad filesystem abstraction.

- [ ] **Step 3: Write the failing binary `PathWriter` test**

Assert a binary named `tool` with `InstallTo: ~/bin` reports exactly `filepath.Join(platform.ExpandPath("~/bin"), "tool")`. Assert no download URL or temporary download path is included.

- [ ] **Step 4: Implement ordered records and created-ancestor ownership**

Replace unordered restoration over `saved map[string]string` with an ordered private record slice plus exact-path deduplication. When `Record(path)` sees a missing target, walk parents with `Lstat` until the nearest existing ancestor and remember only the highest missing descendant. Collapse later missing paths covered by an already-owned ancestor.

Restore saved records in reverse capture order, then remove created roots in reverse declaration order. Continue after each failure and return `errors.Join(...)`. Never follow a symlink when deciding ownership or restoration.

- [ ] **Step 5: Add binary destination capture**

Implement `BinaryAction.WritePaths` using the same `platform.ExpandPath` and `filepath.Join` calculation as `Run`. Keep the destination calculation single-source inside a private method if and only if both methods would otherwise duplicate it.

- [ ] **Step 6: Run focused GREEN tests, commit, and journal**

Format touched files, run snapshot and binary tests, commit, and record coverage for binary overwrite/create plus created-parent cleanup in Mothership.

<!-- /mship:task -->

---

<!-- mship:task id=3 acs=ac2,ac5,ac7,ac17 -->
### Task 3: Add Typed Package Compensation

**Files:**
- Modify: `internal/actions/action.go:1-30`
- Create: `internal/actions/compensation.go`
- Modify: `internal/actions/package.go:13-130`
- Modify: `internal/actions/package_test.go`

**Interfaces:**
- Produces:

```go
type Compensation interface {
    Describe() string
    Run(context.Context) error
}

type CompensationPreparation struct {
    AlreadyApplied     bool
    Compensation      Compensation
    UnavailableReason string
}

type CompensationPreparer interface {
    PrepareCompensation(context.Context) (CompensationPreparation, error)
}
```

- `Compensation == nil` plus a non-empty `UnavailableReason` means state was unsupported, imprecise, malformed, or failed and must not be guessed.
- `AlreadyApplied` is meaningful for package actions and instructs atomic execution to skip the forward install.
- Produces one narrow package-local command executor seam used by state capture and compensation tests; production defaults to `exec.CommandContext`.

- [ ] **Step 1: Inspect references before extending action capabilities**

Use LSP references/CodeGraph impact for `Action`, `Idempotent`, `PackageAction`, `CheckArgs`, and `installArgs`. Preserve non-atomic `Idempotent` behavior while making atomic state capture tri-state and safe.

- [ ] **Step 2: Write the package manager matrix as failing tests**

Table-test every currently supported manager: `brew`, `brew-cask`, `mas`, `winget`, `choco`, `scoop`, `apt`, `apt-get`, `dnf`, `yum`, `pacman`, `snap`, `flatpak`, and `nix`.

For each row assert:

- the exact state-query argv;
- the exact uninstall argv when exact identity mapping is supported;
- exact-ID parsing cannot confuse prefixes (`foo` versus `foobar`);
- present state yields `AlreadyApplied` and no uninstall;
- conclusively absent state yields a compensation using the same manager package identifier supplied to install;
- command start failure, unexpected non-zero status, malformed output, and canceled context produce no automatic uninstall;
- `mas` numeric IDs are parsed from the first list field;
- `nix` attribute-to-installed-name mapping is classified imprecise and therefore unavailable unless a future exact owner is added.

Use successful full-list queries where possible so “absent” is derived from valid output rather than treating every `ExitError` as absence. Expected query families:

| Manager | State source | Exact identity |
|---|---|---|
| brew / brew-cask | `brew list --formula` / `--cask` | exact output line |
| mas | `mas list` | first numeric field |
| winget | exact-ID list output | ID column, not name substring |
| choco | local exact limit output | field before `|` |
| scoop | `scoop export` JSON | app `Name` |
| apt / apt-get | `dpkg-query` installed-status rows | exact package field |
| dnf / yum | `rpm -qa --qf` | exact RPM name |
| pacman | `pacman -Qq` | exact output line |
| snap | `snap list` | first package-name column |
| flatpak | `flatpak list --app --columns=application` | exact application ID |
| nix | unsupported/imprecise | explicit fallback required |

- [ ] **Step 3: Confirm RED and add the focused capability contract**

Run package tests and confirm no compensation API or uninstall construction exists. Add only the optional interfaces above; do not add `Prepare/Apply/Commit/Rollback` methods to every action.

- [ ] **Step 4: Implement tri-state package capture and uninstall commands**

Add private state parsing that distinguishes `present`, `absent`, and `unknown`. Context cancellation is returned as an error so module preflight aborts; all other unsupported/imprecise/failed checks return an unavailable reason so the runner can select an explicit fallback or warn.

Construct uninstall commands for managers with exact ownership:

- brew: `brew uninstall <pkg>`; brew-cask: `brew uninstall --cask <pkg>`;
- mas: `mas uninstall <id>`;
- winget: `winget uninstall --id <pkg> -e --disable-interactivity`;
- choco: `choco uninstall <pkg> -y`;
- scoop: `scoop uninstall <pkg>`;
- apt/apt-get: `sudo apt-get remove -y <pkg>`;
- dnf/yum: matching `sudo <manager> remove -y <pkg>`;
- pacman: `sudo pacman -R --noconfirm <pkg>`;
- snap: `sudo snap remove <pkg>`;
- flatpak: `flatpak uninstall -y <pkg>`.

Do not add retries or package dependency cleanup. Preserve stdio for real uninstall execution and include argv context in errors.

- [ ] **Step 5: Make `IsApplied` reuse safe state parsing**

For non-atomic execution, return true only for conclusive presence. Preserve fail-open install behavior for unknown checks, but never convert unknown into an automatic compensation plan. Remove duplicate package-query logic created by the new capability.

- [ ] **Step 6: Run focused GREEN tests, commit, and journal**

Format, run the action package tests, commit, and record the per-manager state/uninstall matrix plus deliberate `nix` limitation in Mothership.

<!-- /mship:task -->

---

<!-- mship:task id=4 acs=ac2,ac6,ac7,ac18 -->
### Task 4: Add Exact macOS and Windows Setting Compensation

**Files:**
- Modify: `internal/actions/setting.go:14-97`
- Modify: `internal/actions/setting_test.go`
- Modify: `internal/actions/compensation.go` only if the command executor introduced in Task 3 needs a second concrete operation
- Modify: `internal/runner/runner.go:475-483` only to set the action OS if Task 4 cannot defer this one-line wiring to Task 6

**Interfaces:**
- `SettingAction` implements `CompensationPreparer`.
- Produces an explicit action OS field defaulting to `runtime.GOOS`, so Linux-hosted tests can cover macOS and Windows without executing host tools.
- Reuses the narrow command executor from Task 3; no global mutable test hook.

- [ ] **Step 1: Write failing state-capture and restore tables**

For macOS, cover prior missing, string, boolean, integer, and float values. For Windows, cover prior missing plus `REG_SZ`, `REG_DWORD`, `REG_QWORD`, `REG_BINARY`, and `REG_MULTI_SZ` output, preserving the value remainder exactly.

Assert:

- existing keys restore with the exact original type and value;
- keys conclusively absent before forward execution are deleted on compensation;
- permission errors, missing command binaries, unexpected status, malformed output, and unsupported macOS types yield `UnavailableReason`, not a delete/write guess;
- canceled capture returns `context.Canceled`/`DeadlineExceeded`;
- rollback commands receive the fresh context supplied to `Compensation.Run`;
- delete/write failures remain visible.

- [ ] **Step 2: Confirm RED and isolate platform selection**

Run setting tests. Add an `OS` field or equivalent explicit platform input on `SettingAction`; default it only at the production boundary. Avoid package-level mutation of `runtime.GOOS` behavior.

- [ ] **Step 3: Implement conclusive macOS capture**

Use `defaults read-type <domain> <key>` followed by `defaults read <domain> <key>`. Recognize the platform’s documented not-found diagnostic as absent; all other non-zero exits are unknown. Map only scalar types that `SettingAction.Run` can restore exactly to the matching `defaults write` type flags. Unsupported prior array/dictionary/data shapes require explicit fallback or remain uncompensated. A proven missing key compensates with `defaults delete <domain> <key>`.

- [ ] **Step 4: Implement conclusive Windows capture**

Use `reg query <path> /v <key>`. Parse the exact requested value row into name, `REG_*` type, and the untruncated value remainder. Recognize only the documented missing-key/value diagnostic as absent. Restore present values via `reg add ... /t <original-type> /d <original-value> /f`; delete proven-absent values via `reg delete ... /v <key> /f`.

- [ ] **Step 5: Run focused GREEN tests and cross-compilation, commit, and journal**

Run setting/action tests and compile the actions package for `GOOS=darwin` and `GOOS=windows`. Commit and record exact existing/absent/unknown coverage in Mothership.

<!-- /mship:task -->

---

<!-- mship:task id=5 acs=ac3,ac9,ac10,ac11,ac13,ac16,ac21,ac22 -->
### Task 5: Build the LIFO Module Transaction Primitive

**Files:**
- Create: `internal/runner/transaction.go`
- Create: `internal/runner/transaction_test.go`
- Modify: `internal/shell/shell.go:1-36`
- Modify: `internal/shell/shell_test.go`

**Interfaces:**
- Produces private runner types `moduleTransaction`, `journalEntry`, `operationIdentity`, `rollbackResult`, and `rollbackReport`.
- Produces `shell.Validate(context.Context, string) error` for syntax-only validation of an applicable rollback command through the current platform shell.
- Transaction methods: append/activate before forward attempt; deactivate a step proven skipped without side effects; rollback in strict reverse order; commit only after caller confirms module `after_apply` success.

- [ ] **Step 1: Define behavior in failing transaction tests**

Add deterministic tests that activate entries in this order:

1. filesystem snapshot;
2. module `before_apply` compensation;
3. item `before_apply` compensation;
4. typed/explicit item compensation;
5. item `after_apply` compensation;
6. module `after_apply` compensation.

Assert rollback executes `6,5,4,3,2,1`, including the currently failing entry. Add tests for:

- a middle compensation failure does not prevent later entries;
- all compensation errors are reachable through `errors.Is` on the joined result;
- nil compensation becomes one `uncompensated` result rather than a silent omission;
- a deactivated skipped action is not counted or executed;
- deadline expiry marks each unexecuted remaining entry `rollback_failed` with deadline context;
- snapshot `Discard` failure is separate from restoration and remains joined;
- no retry occurs.

Use channels/barriers for timeout tests; no sleeps.

- [ ] **Step 2: Write failing shell syntax-validation tests**

Assert valid shell fragments pass, malformed quoting/grammar fails without running the command body, canceled validation returns the context error, and blank input is rejected. On Unix use `sh -n -c`; on Windows use PowerShell script-block parsing. Keep shell selection in the existing `internal/shell` owner.

- [ ] **Step 3: Implement the minimal journal**

Store entries in activation order. Each entry owns structured identity (`scope`, target/name, forward operation), an optional compensation, and active state. Rollback iterates once in reverse, checks the cleanup context before each entry, continues after ordinary failures, and returns ordered per-entry results plus `errors.Join` of failures.

Do not add persistence, retries, goroutines, or a generic transaction package. The runner is the only owner.

- [ ] **Step 4: Make snapshot the oldest journal entry**

Provide a constructor/helper that captures the snapshot during preflight and immediately installs its restore operation as journal entry zero. Keep cleanup/discard ownership in the transaction so callers cannot discard before module `after_apply`.

- [ ] **Step 5: Run focused GREEN tests, commit, and journal**

Format, run runner transaction and shell tests, commit, and record LIFO/error/deadline ordering evidence in Mothership.

<!-- /mship:task -->

---

<!-- mship:task id=6 acs=ac1,ac2,ac3,ac4,ac5,ac6,ac7,ac8,ac9,ac10,ac11,ac13,ac15,ac16,ac19,ac22 -->
### Task 6: Integrate Preflight, Hooks, Rollback, Reporting, and Panic Unwind

**Files:**
- Modify: `internal/runner/runner.go:36-153,218-383,503-531`
- Modify: `internal/runner/rollback_test.go`
- Modify: `internal/runner/runner_test.go`
- Modify: `internal/audit/audit.go:13-44`
- Modify: `internal/audit/audit_test.go`
- Modify: `internal/ui/ui.go:82-156`
- Modify: `internal/ui/ui_test.go`

**Interfaces:**
- Produces private `preparedModule` / `preparedItem` state so action construction, applicability, `skip_if`, package presence, typed compensation, and explicit fallback selection each have one owner.
- Extends `ModuleResult` with rollback-operation counts: `RolledBack`, `RollbackFailed`, and `Uncompensated`.
- Extends audit entries with rollback phase/scope identity while preserving old JSON readability.
- Produces UI methods for individual rollback outcomes and module/final rollback summaries.
- Adds `Runner.RollbackTimeout`, defaulting to two minutes in `runner.New`.

- [ ] **Step 1: Inspect every exported-symbol caller before the refactor**

Run LSP references/CodeGraph impact for `ApplyModule`, `ApplyAll`, `ModuleResult`, `applyItems`, `applyItem`, `runHook`, `audit.Entry`, `UI.Summary`, and `UI.ModuleSummary`. Migrate every caller in one cutover; do not retain an old overload or compatibility wrapper.

- [ ] **Step 2: Write failing end-to-end runner tests first**

Add table/subtests proving:

- preflight failure or invalid applicable rollback syntax runs no module/item hook or action and leaves no snapshot directory;
- package/setting capture and every applicable filesystem path occur before module `before_apply`;
- module/item apply and sync hooks compensate in exact reverse execution order;
- a forward hook/action failure compensates that attempted step;
- verify failure and module `after_apply` failure unwind all active entries;
- file, directory, and binary overwrite/create restore exact bytes, modes, symlink state, and created parents;
- script/run without rollback and failed package/setting capture warn before execution, then appear as uncompensated if rollback is needed;
- explicit package/setting fallback runs only when automatic capture is unavailable, never when automatic compensation exists;
- one rollback-command failure does not stop later typed, explicit, hook, or snapshot compensation;
- failed transactions return `Applied == 0` and truthful rollback counts;
- successful modules discard all prepared compensation only after `after_apply` succeeds;
- `--no-atomic` equivalent runner mode performs no capture, validation, warning, snapshot, or compensation;
- a panic from an injected action/hook unwinds, removes snapshot data, and re-panics with the identical value.

Use runner/action seams or package-local fake actions, channels, and temp files. Do not use timing sleeps.

- [ ] **Step 3: Implement atomic preflight before the first hook**

For atomic non-dry-run execution:

1. build actions and platform/direction applicability in declaration order;
2. evaluate `skip_if` and conclusive already-applied state before mutations;
3. validate every applicable explicit item and hook rollback command with `shell.Validate`;
4. collect all applicable `PathWriter` paths, including binary, and create/capture the snapshot;
5. call typed `CompensationPreparer` capabilities;
6. choose automatic compensation first, explicit item fallback second, otherwise record and display an uncompensated warning;
7. retain prepared state for execution without repeating checks.

If any fatal preflight step fails or the context is canceled, discard captured state, join cleanup errors, and return before a hook/action runs. Dry-run and non-atomic paths must not invoke this preflight.

- [ ] **Step 4: Activate hook and action compensation at attempt time**

Replace direct hook calls in the atomic path with a helper that receives the forward command, matching rollback command, and structured identity. Activate its rollback/uncompensated journal entry immediately before `shell.Run`.

For each prepared action, activate typed/explicit/uncompensated compensation immediately before `Action.Run`. If `ErrSkipped` proves the action did not execute, deactivate only the action entry; retain any already-run before-hook entries. Preserve existing action and hook order exactly.

- [ ] **Step 5: Unwind on errors and cancellation with a fresh context**

On the first forward error or `ctx.Err()`, derive cleanup from `context.WithoutCancel(ctx)` and `context.WithTimeout(..., r.RollbackTimeout)`. Drain all active journal entries in reverse. Keep the original error first in `errors.Join`; add every compensation, snapshot restore, snapshot discard, and deadline failure.

Rollback commands and typed compensations receive only the fresh cleanup context. Forward commands continue to receive the caller context.

- [ ] **Step 6: Add panic recovery without swallowing programmer failure**

Install a narrowly scoped `defer` around the atomic module lifecycle. If a panic occurs after transaction creation, run the same bounded rollback/reporting path, then `panic(originalValue)`. If preflight has not created recoverable state, re-panic immediately. Never convert a panic into an ordinary `ModuleResult.Err`.

- [ ] **Step 7: Commit only after module `after_apply`**

Do not discard the snapshot after item execution. Run module `after_sync` and `after_apply` inside the transaction. Only after `after_apply` succeeds should commit discard captured state and clear compensation entries. A commit-time discard error is returned and reported as cleanup failure without falsely claiming rollback occurred.

- [ ] **Step 8: Add first-class audit and terminal rollback outcomes**

For every rollback entry emit one audit event and one terminal line with module plus item/hook/filesystem identity, outcome (`rolled_back`, `rollback_failed`, or `uncompensated`), and error/reason text. Add optional phase/scope JSON fields so old audit lines still decode.

Extend module/final summaries with rollback counts. On failed atomic transactions, committed applied count is zero. Preserve forward `success`, `skipped`, and `failure` audit semantics; add rather than overwrite rollback events.

- [ ] **Step 9: Run focused GREEN tests, commit, and journal**

Format touched files. Run runner rollback/runner, audit, UI, snapshot, and action tests. Commit the lifecycle integration and record LIFO traces, original-plus-rollback error evidence, panic identity, and no-atomic non-interference in Mothership.

<!-- /mship:task -->

---

<!-- mship:task id=7 acs=ac11,ac12,ac20,ac21 -->
### Task 7: Wire Signal Cancellation and Rollback Timeout Through the CLI

**Files:**
- Modify: `cmd/dotular/main.go:32-64,111-151,188-190,412-485`
- Modify: `cmd/dotular/main_test.go`
- Create: `cmd/dotular/main_signal_unix_test.go`

**Interfaces:**
- Produces `--rollback-timeout <duration>` on `apply`, `push`, `pull`, and `sync`, default `2m`; rejects zero/negative durations as usage errors.
- Root execution uses `signal.NotifyContext` plus Cobra `ExecuteContext`.
- First SIGINT/SIGTERM cancels forward work and starts bounded rollback. Signal notification is stopped immediately after first cancellation so a second signal gets the platform default termination behavior.

- [ ] **Step 1: Inspect current command context and flag ownership**

Use LSP references/CodeGraph for `main`, `buildRoot`, `newRunner`, `applyCmd`, `directionCmd`, `loadAndResolveConfig` (or its current tagged-config successor), and every `context.Background()` in command handlers. Adapt to any merged tag-filtering changes; do not reintroduce pre-resolution side effects.

- [ ] **Step 2: Write failing flag and context propagation tests**

Assert:

- all four mutating commands expose the duration flag with `2m` default;
- `0`, negative, and malformed durations return usage errors before config/network/action work;
- non-mutating commands do not expose a misleading rollback flag;
- `newRunner` receives the parsed duration;
- an already-canceled Cobra command context reaches config resolution and the runner rather than being replaced by `context.Background()`.

- [ ] **Step 3: Add a subprocess SIGINT rollback test**

In the Unix-only test file, launch the real test helper/binary against an isolated config. The first action creates a marker and blocks through a context-aware command; a following action/hook guarantees the module is mid-transaction. Send SIGINT, assert non-zero exit, marker restoration/removal, rollback audit output, and no snapshot temp directory. Bound synchronization with pipes/channels or process output markers, not sleeps.

Add a second-signal test that proves the process terminates promptly rather than extending the rollback timeout indefinitely. Keep Windows covered by compilation and transaction context tests because portable `os.Process.Signal(os.Interrupt)` behavior is not available there.

- [ ] **Step 4: Install one signal-aware root context**

Create a base context with `signal.NotifyContext` for `os.Interrupt` and SIGTERM, call `root.ExecuteContext(ctx)`, and restore default signal handling as soon as the first cancellation is observed. Keep normal `stop()` cleanup on ordinary exit.

Use `cmd.Context()` throughout apply/push/pull/sync config resolution and runner execution. Do not create a second independent signal context inside the runner.

- [ ] **Step 5: Bind and validate rollback timeout only on mutating commands**

Use a small command-construction helper to avoid repeating flag/validation wiring four times. Set `Runner.RollbackTimeout` at the CLI boundary. `runner.New` still owns the same two-minute safe default for direct callers.

- [ ] **Step 6: Run focused GREEN and subprocess tests, commit, and journal**

Format, run the CLI flag/context tests and Unix signal subprocess test, commit, and record first-signal rollback plus second-signal termination evidence in Mothership.

<!-- /mship:task -->

---

<!-- mship:task id=8 acs=ac12,ac13,ac14,ac18,ac20,ac21,ac23,ac24 -->
### Task 8: Document Guarantees and Complete Release Verification

**Files:**
- Modify: `README.md:52-60,310-332` and the config/reference sections nearest hooks, items, audit, and atomic behavior
- Verify: `specs/2026-08-15-compensating-rollback.md`
- Verify: `docs/plans/2026-08-15-compensating-rollback.md`
- Modify: any existing test named in Tasks 1–7 only when full verification exposes a real uncovered contract

**Interfaces:**
- Documentation names the feature “best-effort transactional rollback,” not database-style atomicity.
- Release evidence includes Mothership’s test iteration, `go vet`, Windows/Darwin cross-compilation, actual CLI failure rollback, actual SIGINT rollback, and hosted review/check state.

- [ ] **Step 1: Update README configuration and behavior**

Document:

- item `rollback` syntax for script/run and package/setting fallback;
- nested `hooks.rollback` syntax and counterpart requirement;
- automatic file/directory/binary snapshot coverage, including created ancestors;
- typed package and macOS/Windows setting compensation boundaries;
- warning-and-continue behavior for uncompensated arbitrary commands;
- automatic-first, explicit-fallback precedence;
- `--no-atomic` bypass and `--rollback-timeout` default/availability;
- rollback on action/hook/verify errors, cancellation, and panic;
- LIFO continuation after failures and first-class audit/summary outcomes;
- SIGKILL/power-loss/process-death, package dependencies, retries, and durable recovery as non-goals.

Remove or revise the current claim that snapshot/rollback covers only file and directory items. Do not claim full atomicity.

- [ ] **Step 2: Run formatting and all focused behavior packages**

Run `gofmt` on every changed Go file, then focused tests for:

```text
internal/config
internal/template
internal/registry
internal/actions
internal/snapshot
internal/shell
internal/runner
internal/audit
internal/ui
cmd/dotular
```

Confirm all RED tests introduced in Tasks 1–7 now pass and fail when their production change is locally reversed.

- [ ] **Step 3: Run Mothership’s full repository test iteration**

From the feature worktree run:

```bash
mship test --task compensating-rollback --repos dotular
```

If the workstation Go binary is not on Mothership’s task subprocess PATH, fix the environment and rerun through Mothership; do not substitute a bare successful `go test ./...` as final evidence. Record the iteration ID and diff in the journal.

- [ ] **Step 4: Run static and cross-platform gates**

Run:

```bash
go vet ./...
GOOS=windows GOARCH=amd64 go test -c ./internal/actions -o /tmp/dotular-actions-windows.test.exe
GOOS=windows GOARCH=amd64 go test -c ./internal/runner -o /tmp/dotular-runner-windows.test.exe
GOOS=windows GOARCH=amd64 go test -c ./cmd/dotular -o /tmp/dotular-cli-windows.test.exe
GOOS=darwin GOARCH=amd64 go test -c ./internal/actions -o /tmp/dotular-actions-darwin.test
GOOS=darwin GOARCH=amd64 go test -c ./internal/runner -o /tmp/dotular-runner-darwin.test
GOOS=darwin GOARCH=amd64 go test -c ./cmd/dotular -o /tmp/dotular-cli-darwin.test
```

Remove generated cross-compile binaries after recording results.

- [ ] **Step 5: Smoke the actual CLI rollback surface**

Build the real CLI into a temporary directory. With isolated `HOME`, `XDG_CACHE_HOME`, config, repo, and audit paths:

1. run a module whose first `run` action creates a marker with explicit rollback and whose second action fails; assert non-zero exit, marker absent, `applied == 0`, and audit/terminal `rolled_back` output;
2. run a module with an uncompensated command followed by failure; assert the warning precedes mutation and final output/audit says `uncompensated`;
3. rerun scenario one with `--no-atomic`; assert the marker remains and no rollback warning/event appears;
4. run the SIGINT scenario once against the built CLI, assert bounded non-zero exit, restored filesystem, and no snapshot litter.

- [ ] **Step 6: Request final code review and fix findings**

Use the requesting-code-review skill with the complete base-to-HEAD diff. Require evidence-backed findings with exact file/line, severity, failure scenario, and smallest correction. Apply technically valid findings RED/GREEN; push back on suggestions that weaken the approved warning-and-continue, no-retry, or in-process-only contract.

- [ ] **Step 7: Commit docs/final fixes and open the PR**

Commit README and any review fixes as coherent commits. Open a non-draft PR with `Closes #7`, spec/plan links, behavior summary, limitations, test iteration ID, vet/cross-compile/smoke evidence, and rollback outcome examples.

- [ ] **Step 8: Check hosted review and CI before handoff**

Use the check-pr workflow. Wait for required Go, security, and hosted review checks. Resolve every actionable thread, rerun affected evidence, and record the final PR URL/check state in Mothership. Do not finish the WorkItem until required checks pass and unresolved actionable comments are zero.

<!-- /mship:task -->
