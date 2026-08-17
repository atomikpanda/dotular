---
id: compensating-rollback
title: Compensating and interruption-safe rollback
status: dispatched
created_at: '2026-08-15T16:57:23.944447Z'
updated_at: '2026-08-15T17:32:07.394427Z'
affected_repos:
- dotular
acceptance_criteria:
- id: ac1
  text: Default atomic apply, push, pull, and sync operations use a module transaction.
    `--no-atomic` retains its name and skips runtime rollback state capture and compensation,
    but does not bypass strict config decoding, validation, or template checks.
  verdict: approved
  evidence: []
  comment: null
- id: ac2
  text: Before any module hook or action executes, the runner builds applicable actions,
    collects all PathWriter paths, snapshots pre-module filesystem state, captures
    package and setting pre-state, validates explicit rollback commands, and places
    the snapshot as the oldest entry in one LIFO journal. For a missing write path,
    created-ancestor tracking records the highest missing component immediately beneath
    the closest existing parent.
  verdict: approved
  evidence: []
  comment: null
- id: ac3
  text: Prepared compensation is registered and activated immediately before each
    forward hook or action attempt; current forward ordering is preserved, a failed
    attempted step is compensated, and action errors, hook errors, cancellation, and
    panic all unwind the journal in strict reverse order with typed or explicit compensation
    before filesystem restoration.
  verdict: approved
  evidence: []
  comment: null
- id: ac4
  text: BinaryAction exposes its exact installed file through PathWriter, and rollback
    covers binary overwrite, binary creation, and parent scaffolding created by file,
    directory, or binary writes without deleting any pre-existing parent.
  verdict: approved
  evidence: []
  comment: null
- id: ac5
  text: Package capture distinguishes conclusively absent, present, and unknown states
    for every supported package manager. Only conclusively absent packages receive
    manager-specific named-package uninstall; present packages remain skipped; unknown,
    imprecise, or failed checks never auto-uninstall and instead use explicit fallback
    or produce an uncompensated warning.
  verdict: approved
  evidence: []
  comment: null
- id: ac6
  text: On macOS and Windows, setting rollback captures and restores the exact prior
    presence, type, and value, or deletes a key created by the failed transaction.
    Unavailable capture uses explicit fallback or produces an uncompensated warning.
  verdict: approved
  evidence: []
  comment: null
- id: ac7
  text: 'Typed automatic package or setting compensation takes precedence over an
    explicit item rollback: explicit fallback is selected only when typed capture
    is unavailable and is not run after a prepared typed rollback fails. Item-level
    `rollback` is accepted for script/run items and as package/setting fallback, and
    rejected for file/directory/binary items.'
  verdict: approved
  evidence: []
  comment: null
- id: ac8
  text: Existing hook forward fields remain scalar. `hooks.rollback` accepts only
    `before_apply`, `after_apply`, `before_sync`, and `after_sync`, and each rollback
    key is valid only when its matching forward hook exists. Strict decoding, validation,
    and template handling cover every new rollback field.
  verdict: approved
  evidence: []
  comment: null
- id: ac9
  text: The filesystem snapshot remains available through `after_apply`. After `after_apply`
    succeeds, the module commits before snapshot discard; a post-commit discard failure
    returns nonzero and reports committed work plus cleanup failure without attempting
    rollback. During rollback, command entries run in LIFO order until the fresh cleanup
    context expires; deadline-blocked entries are reported individually as rollback
    failures; context-free snapshot restore and discard are still attempted; all contextual
    failures are aggregated with the original forward error.
  verdict: approved
  evidence: []
  comment: null
- id: ac10
  text: A panic triggers rollback and then re-panics with the original panic value.
  verdict: approved
  evidence: []
  comment: null
- id: ac11
  text: Every command is executed with `cmd.Context`, not `context.Background`. The
    first signal cancels forward work and starts rollback on a fresh context bounded
    by the configured timeout; a second signal terminates immediately.
  verdict: approved
  evidence: []
  comment: null
- id: ac12
  text: Apply, push, pull, and sync expose `--rollback-timeout`; its documented default
    is two minutes.
  verdict: approved
  evidence: []
  comment: null
- id: ac13
  text: ModuleResult, UI, and audit distinguish `applied`, `rolled_back`, `rollback_failed`,
    and `uncompensated`. Each attempted item has one final outcome; hooks appear in
    detailed UI/audit without inflating item counts; snapshot cleanup failures remain
    contextual errors. Unavailable compensation warns rather than rejects in default
    atomic mode, and a failed transaction exits nonzero even when fully compensated.
  verdict: approved
  evidence: []
  comment: null
- id: ac14
  text: README and configuration examples document explicit item rollback, rollback
    hooks, the timeout flag and two-minute default, unsupported automatic compensation
    boundaries, and the term `best-effort transactional rollback` rather than database
    atomicity.
  verdict: approved
  evidence: []
  comment: null
- id: ac15
  text: RED/GREEN verification covers binary overwrite and creation, created ancestors,
    the pre-hook baseline, and `after_apply` failure.
  verdict: approved
  evidence: []
  comment: null
- id: ac16
  text: Verification proves one LIFO unwind spanning module hooks, item hooks, typed
    compensation, explicit compensation, and the filesystem snapshot, and proves that
    a failed attempted step is compensated.
  verdict: approved
  evidence: []
  comment: null
- id: ac17
  text: Package verification uses a table covering all supported managers and proves
    that unknown state never triggers automatic uninstall.
  verdict: approved
  evidence: []
  comment: null
- id: ac18
  text: macOS and Windows setting verification covers existing and absent settings
    through injected execution and includes Windows and Darwin cross-compilation.
  verdict: approved
  evidence: []
  comment: null
- id: ac19
  text: Panic verification proves filesystem/state restoration occurs before re-panicking
    with the original value.
  verdict: approved
  evidence: []
  comment: null
- id: ac20
  text: A subprocess SIGINT scenario proves rollback uses a fresh context, exits nonzero,
    and leaves no snapshot temporary data.
  verdict: approved
  evidence: []
  comment: null
- id: ac21
  text: Timeout and second-signal verification proves bounded synchronization without
    sleeps.
  verdict: approved
  evidence: []
  comment: null
- id: ac22
  text: Aggregation, audit, and UI verification covers full rollback, partial rollback,
    failed rollback, and uncompensated outcomes.
  verdict: approved
  evidence: []
  comment: null
- id: ac23
  text: Final verification includes the mship race suite, vet, Windows and Darwin
    cross-compilation, and an actual CLI smoke test.
  verdict: approved
  evidence: []
  comment: null
- id: ac24
  text: 'The delivered change is one coherent dotular repository change and closes
    issue #7.'
  verdict: approved
  evidence: []
  comment: null
open_questions: []
non_goals:
- Automatic reversal of undeclared arbitrary script or run-command effects
- Package dependency rollback
- Recovery from SIGKILL, power loss, kernel failure, or process death
- A durable or restartable transaction log
- Compensation retries
- A strict mode that rejects modules when compensation is unavailable
- Renaming `--no-atomic`
- Claiming database-style atomicity; the feature is best-effort transactional rollback
risks:
- Arbitrary script, run-command, or hook side effects remain uncompensated unless
  an explicit rollback command is declared.
- Unknown, imprecise, failed, or otherwise unavailable package/setting state capture
  can leave work uncompensated when no explicit fallback exists; default atomic mode
  warns and continues in that case.
- SIGKILL, power loss, kernel failure, and process death are outside the recovery
  model because there is no durable restart transaction log.
- Rollback commands, filesystem restoration, snapshot discard, or cleanup can fail;
  the command aggregates and reports partial or failed rollback rather than claiming
  full restoration.
- The bounded cleanup deadline and a second termination signal can deliberately leave
  journal entries unexecuted; each such entry must be reported rather than described
  as restored.
- Explicit rollback commands are trusted configuration code and execute with the same
  privileges as forward hooks or actions.
- Automatic package uninstall removes only the named package and cannot restore dependency
  or package-manager metadata changes.
task_slug: compensating-rollback
work_item_id: wi-20260815173207-06dd2e66
clarification_reason: null
prose_verdicts:
  problem:
    verdict: approved
    comment: null
  user_story:
    verdict: approved
    comment: null
  approach:
    verdict: approved
    comment: null
  non_goals:
    verdict: approved
    comment: null
  risks:
    verdict: approved
    comment: null
---
## Problem

Issue #7 remains open because rollback currently covers only FileAction and DirectoryAction paths. Binary, package, script, run-command, setting, module/item-hook side effects, interrupts, panics, discard failures, created parent scaffolding, and truthful reporting remain incomplete, while the README already limits current coverage to files and directories.

## User story

As a dotular operator using apply, push, pull, or sync, I want best-effort transactional rollback that compensates every supported attempted side effect in strict reverse order, remains bounded and interruption-safe, and reports committed, compensated, failed, and uncompensated work truthfully, so a failed or interrupted atomic operation does not silently leave avoidable partial state.

## Approach

Implement one coherent dotular repository change that closes issue #7. Add an unexported moduleTransaction owner in internal/runner backed by a single LIFO journal. Keep internal/snapshot as the filesystem-state owner, and let actions own typed state capture and restoration through focused optional capabilities; do not replace every Action with Prepare/Commit/Rollback and do not add runner type switches.

Before module hooks or actions run, build the applicable actions, collect every PathWriter path, snapshot the pre-module filesystem state including the first absent ancestors, capture package and setting pre-state, validate explicit rollback commands, and create the journal with the filesystem snapshot as its oldest entry. Immediately before each forward hook or action attempt, register and activate its prepared compensation. Preserve current forward ordering. On an action error, hook error, cancellation, or panic, unwind in strict reverse order so typed or explicit command compensation runs before filesystem restoration. Compensation applies to an attempted step even when that step itself fails.

Use typed automatic compensation for packages and settings. Package pre-state is tri-state: only a conclusive absent result permits manager-specific named-package uninstall; a present package remains skipped; an unknown, imprecise, or failed check must never be treated as absent and instead uses an explicit rollback fallback when available or warns and remains uncompensated. Settings capture exact prior presence, type, and value; rollback restores that state or deletes a newly created key. If setting capture is unavailable, use an explicit rollback fallback when available or warn and remain uncompensated. Typed automatic compensation takes precedence over explicit item rollback: the explicit command is selected only when typed pre-state capture is unavailable, and is not run as a second fallback after a prepared typed rollback itself fails.

Support explicit rollback commands for script and run items, package/setting fallback, and module/item hook side effects. Add an item-level sibling YAML field `rollback: command`. Existing hooks retain scalar forward fields and gain a `hooks.rollback` map keyed by `before_apply`, `after_apply`, `before_sync`, and `after_sync`. An item rollback is valid for script/run and as package/setting fallback, and is rejected for file/directory/binary items. A hook rollback key requires the corresponding forward hook. Apply strict decoding, validation, and templates to the new fields.

Make BinaryAction implement PathWriter for the exact installed file. Track the highest missing component immediately beneath the closest existing parent for file, directory, and binary writes, so rollback removes only directory scaffolding created by the transaction and never deletes a pre-existing parent. Keep the filesystem snapshot through `after_apply`. When `after_apply` succeeds, logically commit the module before discarding snapshot data. A post-commit discard failure is fatal and reports committed work plus a cleanup error; it does not initiate rollback or relabel durable state as unapplied. During rollback, invoke journal entries in reverse order until the fresh cleanup context expires and aggregate restore, discard, command-compensation, and cleanup errors rather than ignoring them. Entries prevented by the deadline are reported individually as rollback failures; context-free filesystem restoration and snapshot discard are still attempted despite command-compensation failures or deadline expiry. Retain the original forward error and join contextual rollback/cleanup failures. If the rollback deadline expires, identify remaining journal entries. On panic, unwind and then re-panic with the original value.

Use `cmd.Context` rather than `context.Background` for all commands. Default atomic apply, push, pull, and sync operations use the transaction; `--no-atomic` skips runtime state capture and compensation but never bypasses strict config decoding or validation. The first signal cancels forward work and starts bounded rollback on a fresh context. A second signal terminates immediately. Add `--rollback-timeout` to apply, push, pull, and sync, with a documented two-minute default.

Extend ModuleResult, UI, and audit reporting with first-class `applied`, `rolled_back`, `rollback_failed`, and `uncompensated` outcomes. Each attempted item has one final transaction outcome: committed items are applied; on failed transactions, compensated items are rolled back, failed compensation is rollback_failed, and attempted work without compensation is uncompensated. Hooks are identified in detailed UI and audit events but do not inflate item counts. Snapshot cleanup failures remain contextual errors, not pseudo-items. A failed transaction exits nonzero even when compensation succeeds completely. Default atomic mode warns and continues when compensation is unavailable; it does not reject the module.

## Operator-approved behavior

Use compensating actions rather than narrowing documentation. Add rollback hooks. Default atomic mode warns and continues when compensation is unavailable instead of rejecting the module. Continue rollback entries in LIFO order until the fresh cleanup context expires, aggregate their errors, and report deadline-blocked entries individually. Typed automatic compensation takes precedence, while explicit item rollback is the fallback when package or setting state capture is unavailable. Never infer package absence. Compensation runs for a step once that step is attempted, including when its forward execution fails.

## Transaction and cleanup invariants

The journal is LIFO and the filesystem snapshot is its oldest entry, ensuring filesystem restoration runs last. Pre-module filesystem state is established before hooks. A missing path records the highest missing component beneath the closest existing parent, so cleanup removes only transaction-created scaffolding. The module logically commits after after_apply succeeds; snapshot discard is post-commit cleanup. A discard failure is fatal but does not trigger rollback or hide durable applied state. During rollback, command entries run until the fresh cleanup context expires; deadline-blocked entries are reported, while context-free snapshot restore and discard are still attempted. Typed rollback failure never triggers the explicit fallback a second time. Panic recovery preserves the original panic value.

## Configuration surface

Item YAML adds sibling `rollback: command`. Existing hook forward fields remain scalar, and `hooks.rollback` is a map keyed by `before_apply`, `after_apply`, `before_sync`, and `after_sync`. Apply, push, pull, and sync add `--rollback-timeout` with a documented two-minute default. `--no-atomic` remains the opt-out name.

## Required verification matrix

RED/GREEN: binary overwrite/create, created ancestors, pre-hook baseline, and `after_apply` failure. Ordering: one LIFO sequence spanning module/item hooks, typed/explicit compensation, and snapshot. Attempt semantics: compensate a failed attempted step. Packages: table-test all managers and prove unknown never auto-uninstalls. Settings: macOS/Windows existing and absent cases through injected execution plus cross-compilation. Panic: restore then re-panic. Signals: subprocess SIGINT uses a fresh rollback context, exits nonzero, and leaves no snapshot temp; timeout and second signal demonstrate bounded synchronization without sleeps. Reporting: aggregation, audit, and UI cover full, partial, failed, and uncompensated results. Final gates: mship race suite, vet, Windows/Darwin cross-compilation, and an actual CLI smoke test.
