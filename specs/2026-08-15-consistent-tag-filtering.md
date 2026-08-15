---
id: consistent-tag-filtering
title: 'Fix #26: enforce tag filtering before resolution and on named modules'
status: dispatched
created_at: '2026-08-15T08:45:41.711387Z'
updated_at: '2026-08-15T08:48:47.861946Z'
affected_repos:
- dotular
acceptance_criteria:
- id: ac1
  text: apply, push, pull, sync, verify, and status exclude modules whose only_tags
    or exclude_tags do not match the loaded machine tags.
  verdict: approved
  evidence: []
  comment: null
- id: ac2
  text: Naming an inactive module in apply, push, pull, sync, or verify returns a
    non-zero usage error before hooks, actions, verify commands, network requests,
    cache writes, lockfile writes, or audit entries.
  verdict: approved
  evidence: []
  comment: null
- id: ac3
  text: The named-module error identifies the inactive module, states that its tag
    filters do not match, and points to --ignore-tags.
  verdict: approved
  evidence: []
  comment: null
- id: ac4
  text: --ignore-tags on apply, push, pull, sync, verify, and status deliberately
    restores execution or verification of tag-inactive modules.
  verdict: approved
  evidence: []
  comment: null
- id: ac5
  text: Without --ignore-tags, remote modules excluded by tag policy are removed before
    registry.Resolve and therefore produce no module fetch, cache publication, lockfile
    creation or mutation, or template rendering.
  verdict: approved
  evidence: []
  comment: null
- id: ac6
  text: dotular list preserves config order, shows active modules with their current
    item counts, and shows inactive modules as skipped for tag mismatch without fetching
    inactive remote definitions.
  verdict: approved
  evidence: []
  comment: null
- id: ac7
  text: Failure to load or parse machine tags fails before registry resolution instead
    of silently treating the machine as tagless.
  verdict: approved
  evidence: []
  comment: null
- id: ac8
  text: Direct runner ApplyAll and VerifyAll callers continue to enforce tags using
    tags.Matches.
  verdict: approved
  evidence: []
  comment: null
- id: ac9
  text: init does not add inferred only_tags or exclude_tags to adopted modules, and
    the documented tag semantics do not claim that it does.
  verdict: approved
  evidence: []
  comment: null
- id: ac10
  text: README command and tagging documentation describes named-module enforcement,
    --ignore-tags, pre-resolution filtering, and inactive list output.
  verdict: approved
  evidence: []
  comment: null
open_questions: []
non_goals:
- Changing the only_tags/exclude_tags matching rules.
- Adding tag policy to registry module or index schemas.
- Inferring permanent module tags from the current OS, architecture, hostname, or
  init scan results.
- Changing registry pin, cache, trust, or update semantics for active modules.
- Changing runner behavior for platform-inapplicable items or skip_if.
risks:
- Users who relied on named modules silently bypassing tags must now pass --ignore-tags;
  the explicit flag and error text make that compatibility break visible.
- Filtering too late would retain the bug by allowing network, cache, or lock side
  effects; tests must observe all three boundaries.
- Filtering a copied Config must preserve module order, age configuration, aliases,
  and named-module diagnostics.
- list cannot show remote item counts for inactive modules without violating the no-fetch
  contract, so its inactive row must prefer truthful status over a count.
task_slug: consistent-tag-filtering
work_item_id: wi-20260815084847-60182c92
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

Machine only_tags and exclude_tags filters currently protect only all-module runner loops. Explicitly named apply, push, pull, sync, and verify calls bypass them, while registry resolution runs before any filtering and can fetch excluded remote modules and publish cache and lock state. list also hides whether a module is inactive. This makes documented tag policy inconsistent and lets a nominally skipped module cause network and persistent state changes.

## User story

As a dotular user, I want module tag policy to apply consistently before remote resolution, so that inactive modules neither execute nor mutate registry state unless I explicitly override the policy.

## Approach

Keep internal/tags.Matches as the single owner of matching semantics. At CLI command boundaries, strict-load the raw config and machine tags, classify modules before registry.Resolve, and resolve only active or explicitly overridden modules. Named inactive modules fail before resolution with a diagnostic that identifies the module and points to --ignore-tags. Add --ignore-tags to apply, push, pull, sync, verify, and status. Preserve runner-level matching as defense for direct package callers. list resolves active modules for item counts and reports inactive modules as skipped for tag mismatch without fetching them. init does not synthesize only_tags because registry module metadata carries no tag policy and current-machine inference would incorrectly restrict cross-platform configs.

## Ordering invariant

For execution and verification commands, the observable order is strict config decode and validation, machine-tag load, name validation and tag classification, registry resolution of the retained config, then runner work. An inactive named module is diagnosed from the raw config so it is not misreported as absent after filtering.

## Compatibility decision

Explicit module names no longer imply an undocumented tag bypass. The supported bypass is the visible --ignore-tags flag, which is intentional at the call site and can be represented in shell history and automation.

## Verification

Use RED/GREEN CLI tests with an in-process HTTP transport or test server to prove excluded remote refs cause no request and no cache/lock publication; command tests for every flag-bearing command and named-module failure; list ordering/status tests; focused package tests; full Go suite; go vet; Windows and Darwin amd64 cross-compilation; and actual CLI smoke scenarios with isolated HOME and cache directories.
