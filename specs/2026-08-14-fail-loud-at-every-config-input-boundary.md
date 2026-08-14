---
id: fail-loud-at-every-config-input-boundary
title: Fail loud at every config input boundary
status: dispatched
created_at: '2026-08-14T21:02:04.090501Z'
updated_at: '2026-08-14T22:24:16.132701Z'
affected_repos:
- dotular
acceptance_criteria:
- id: ac1
  text: 'Issue #17 example 1 is closed: local fields such as packge, destinaton, and
    skipif are rejected during strict decoding with YAML decoder line information
    instead of loading successfully or producing a no-op item.'
  verdict: approved
  evidence: []
  comment: null
- id: ac2
  text: 'Issue #17 example 2 is closed: destination: {darwin: ~/.config} is rejected
    with the unknown key darwin and its YAML line in the error; it is never dropped
    as an inapplicable destination.'
  verdict: approved
  evidence: []
  comment: null
- id: ac3
  text: 'Issue #17 example 3 is closed: an item containing both package and file is
    rejected because it has multiple non-empty primary fields, rather than package
    precedence silently winning.'
  verdict: approved
  evidence: []
  comment: null
- id: ac4
  text: 'Issue #17 example 4 is closed: an item with none of package, script, setting,
    file, directory, binary, or run is rejected at load time, so apply cannot execute
    earlier items, verify cannot skip it, and list cannot count it while omitting
    its type.'
  verdict: approved
  evidence: []
  comment: null
- id: ac5
  text: 'Issue #17 example 5 is closed: a local module with a non-empty from and non-empty
    items is rejected instead of silently discarding the local items during registry
    resolution.'
  verdict: approved
  evidence: []
  comment: null
- id: ac6
  text: 'Issue #17 example 6 is closed: a loaded direction: pul is rejected instead
    of becoming push, and dotular add <path> <module> --direction pul returns the
    CLI usage exit status 2 rather than writing that value.'
  verdict: approved
  evidence: []
  comment: null
- id: ac7
  text: Strict local decoding rejects unknown keys at the mapping root, module, hooks,
    and item levels, while reporting decoder-provided line information for the first
    failure.
  verdict: approved
  evidence: []
  comment: null
- id: ac8
  text: Strict downloaded-module decoding rejects unknown keys on RemoteModule and
    on its nested item and hook data before the module can be resolved, cached as
    usable content, checked, or explicitly updated.
  verdict: approved
  evidence: []
  comment: null
- id: ac9
  text: PlatformMap mapping keys are exactly macos, linux, and windows; every other
    key, including darwin, is rejected with key and line context, with no aliasing
    or automatic rename.
  verdict: approved
  evidence: []
  comment: null
- id: ac10
  text: Valid PlatformMap scalar syntax, valid mappings, literal ~ scalar values,
    YAML null handling, and omission of platform values retain their current decoded
    meaning and serialized form.
  verdict: approved
  evidence: []
  comment: null
- id: ac11
  text: Local loading continues to accept both the current Config mapping root and
    the legacy []Module sequence root, including valid empty mapping, empty sequence,
    and empty-file configurations, while applying KnownFields(true) to the selected
    concrete type.
  verdict: approved
  evidence: []
  comment: null
- id: ac12
  text: Config.Validate uses the shared ValidateItems and ValidateDirection behavior
    so every local module item and every override item has exactly one non-empty primary
    field and every non-empty direction is push, pull, or sync.
  verdict: approved
  evidence: []
  comment: null
- id: ac13
  text: An empty direction in loaded item data remains valid and EffectiveDirection
    still treats it as the implicit push default; invalid non-empty values are not
    coerced.
  verdict: approved
  evidence: []
  comment: null
- id: ac14
  text: Semantic validation returns the first failure deterministically and includes
    module identity or index, whether the item came from items or override, and the
    item index; decode failures retain YAML decoder line information.
  verdict: approved
  evidence: []
  comment: null
- id: ac15
  text: For malformed local configuration, every config-consuming command, including
    apply, push, pull, sync, list, status, verify, registry resolution, registry update,
    and registry check, fails before running actions, hooks, verify commands, audit
    writes, snapshots, or other command work that mutates user state.
  verdict: approved
  evidence: []
  comment: null
- id: ac16
  text: Config.Save validates the complete Config before opening, truncating, replacing,
    or otherwise changing its destination, and a validation failure leaves an existing
    file byte-for-byte untouched and does not create a missing file.
  verdict: approved
  evidence: []
  comment: null
- id: ac17
  text: dotular add loads and validates an existing config before os.MkdirAll, file
    or directory copy, or Config.Save; an invalid existing config leaves both the
    config file and module store unchanged.
  verdict: approved
  evidence: []
  comment: null
- id: ac18
  text: dotular add validates --direction before mutation; any value other than push,
    pull, or sync is a usage error with process exit status 2 and leaves the config
    and module store untouched.
  verdict: approved
  evidence: []
  comment: null
- id: ac19
  text: A successful dotular add preserves its existing success and path output, stores
    a valid file or directory item, and omits direction from generated YAML when the
    requested direction is the default push while serializing pull or sync when explicitly
    selected.
  verdict: approved
  evidence: []
  comment: null
- id: ac20
  text: Downloaded RemoteModule items are validated immediately after strict decoding
    with the same primary-field cardinality rules used for local items; literal directions
    must be push, pull, or sync, while direction values containing Go-template actions
    are deferred to post-render validation, and errors identify the remote module
    and item index.
  verdict: approved
  evidence: []
  comment: null
- id: ac21
  text: Each downloaded item is strictly validated again after parameter template
    rendering; zero or multiple non-empty primary fields and every rendered direction
    other than push, pull, or sync fail before override merging or command use.
  verdict: approved
  evidence: []
  comment: null
- id: ac22
  text: Ordinary resolve, explicit registry update, and registry check mode all call
    the same render-and-post-render-validation path, so none can accept a rendered
    item that another mode rejects.
  verdict: approved
  evidence: []
  comment: null
- id: ac23
  text: Unknown fields, invalid item cardinality, and invalid literal remote directions
    fail before Fetch publishes cache or lock changes. A parameter-dependent post-render
    failure cannot run an action or hook, save local configuration, mutate the module
    store, append an audit result, or persist a new lockfile entry; ordinary resolve
    may retain the already verified raw source bytes in cache, while explicit update
    and check remain non-mutating until all staged refs validate.
  verdict: approved
  evidence: []
  comment: null
- id: ac24
  text: Valid local-only modules, valid downloaded modules including templated directions,
    parameter rendering, override merging, ordinary resolve, explicit update, and
    check flows retain their current successful behavior and output.
  verdict: approved
  evidence: []
  comment: null
- id: ac25
  text: For valid in-memory Item values, Item.Type precedence and return values and
    EffectiveDirection behavior remain unchanged; validation is enforced at input
    and save boundaries rather than by redefining these compatibility methods.
  verdict: approved
  evidence: []
  comment: null
- id: ac26
  text: README clearly distinguishes the runtime platform value darwin printed by
    dotular platform from the canonical YAML PlatformMap key macos, and documents
    macos, linux, and windows as the only accepted YAML keys.
  verdict: approved
  evidence: []
  comment: null
- id: ac27
  text: 'Focused automated tests cover all six issue #17 examples, unknown keys at
    local root/module/hooks/item and remote levels, every primary-field cardinality
    and direction rule for local items, overrides, decoded remote items and rendered
    remote items, deterministic error context, Save/add no-side-effect guarantees,
    and preservation of valid mapping and legacy formats.'
  verdict: approved
  evidence: []
  comment: null
- id: ac28
  text: Repository verification succeeds with the full Go test suite and go vet, cross-compilation
    for the supported macOS, Linux, and Windows targets, and actual dotular CLI smoke
    runs that demonstrate a valid config still works, malformed config fails before
    side effects, and invalid add direction exits exactly 2 without changing the config
    or module store.
  verdict: approved
  evidence: []
  comment: null
open_questions: []
non_goals:
- Add darwin or any other alias or automatic rename for the canonical macos YAML platform
  key.
- Create an unrelated field-compatibility matrix or relax strict decoding for legacy
  misspellings.
- Perform a broad runner or action refactor beyond moving validation to the configuration
  and remote-input boundaries.
risks:
- Strict decoding will intentionally reject existing files or downloaded modules that
  relied on ignored misspellings or undocumented keys.
- The two-pass decoder could regress legacy sequence roots, empty configs, null values,
  literal ~ values, or scalar PlatformMap syntax if the original bytes are not decoded
  consistently in the second pass.
- Template expansion can change which primary fields are non-empty, so validating
  only the downloaded representation would leave a semantic gap.
- Downloaded direction fields may contain supported Go-template actions, so raw validation
  must distinguish literal values from templated values without allowing a literal
  typo through; every rendered result remains subject to strict direction validation.
- If validation is ordered after cache, lockfile, module-store, hook, action, audit,
  or config writes, a correctly reported error could still leave observable state
  behind.
- Non-deterministic map traversal or incomplete error wrapping could make the first
  reported failure unstable or omit the module and item location needed to repair
  it.
task_slug: fail-loud-config-boundary
work_item_id: wi-20260814222416-eadb155e
clarification_reason: null
prose_verdicts: {}
---
## Problem

Dotular currently accepts malformed local dotular.yaml files and downloaded registry modules at multiple configuration boundaries. Unknown fields and platform keys are silently discarded, conflicting or missing item types are resolved late or by precedence, invalid directions become push, and local from-plus-items content is ignored. These failures make typos look successful, can execute a different action than the user declared, and can allow earlier actions or filesystem mutations before the error is discovered.

## User story

As a dotular user, I want every local and downloaded configuration input validated before it can cause side effects, so that malformed configuration fails deterministically with actionable context instead of being ignored or reinterpreted.

## Approach

Make internal/config the shared semantic-validation owner through Config.Validate, ValidateItems, and ValidateDirection. Decode local YAML in two passes: inspect a yaml.Node only to distinguish a mapping root from the supported legacy sequence root, then decode the original input with yaml.Decoder.KnownFields(true) into Config or []Module. Make PlatformMap.UnmarshalYAML accept only the canonical YAML keys macos, linux, and windows and report the offending key and line. Require every item to contain exactly one non-empty primary field from package, script, setting, file, directory, binary, and run; accept only push, pull, sync, or an empty loaded direction; reject modules combining from with non-empty items; and validate override items through the same item validator. Validate in Config.Save before any write. Strict-decode downloaded RemoteModule YAML and immediately validate item cardinality plus every literal direction. Preserve supported direction templates by deferring templated direction values to mandatory validation after rendering. Validate every rendered item through one strict path shared by ordinary resolution, explicit registry update, and check mode. In dotular add, validate --direction as a usage error and load and validate an existing config before creating directories, copying content, or saving. Preserve supported YAML shapes, valid CLI output, default-direction omission, and valid Item.Type and EffectiveDirection behavior.

## Validation boundaries

Validation occurs after strict decode and before data enters command execution. Raw downloaded items validate field cardinality and literal directions immediately; supported templated directions are deferred to the mandatory strict validation after rendering. Config.Save validates before writing. The core runner may continue to assume validated input; Item.Type and EffectiveDirection remain compatibility behavior for valid in-memory callers rather than serving as input validators.

## Error and side-effect contract

Decode errors preserve YAML line details. Semantic errors select the first failure in stable module/item order and add module or index, items-versus-override, and item index context. Rejected local input, raw remote structure, add direction, or save candidate leaves no mutation. A parameter-dependent post-render failure cannot execute or persist a lockfile change; ordinary resolve may retain verified raw source bytes in cache, while staged explicit update and check publish nothing until all refs validate.
