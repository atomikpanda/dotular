# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test

```bash
make build                    # Build binary to ./build/dotular
go test ./...                 # Run all tests
go test -race ./...           # Run tests with race detector (CI default)
go test ./internal/config/    # Run tests for a single package
go test -run TestLoad ./internal/config/  # Run a single test
go vet ./...                  # Lint (only linter used)
```

CI enforces 80% code coverage minimum (`go test -race -coverprofile=coverage.out -covermode=atomic ./...`).

## Architecture

Go CLI dotfile manager using Cobra. Module path: `github.com/atomikpanda/dotular`, requires Go 1.23+ (`go.mod` declares `go 1.23.0`).

**Config-driven**: A `dotular.yaml` file defines modules, each containing items (package installs, file syncs, scripts, settings, binaries, directory trees, inline commands). The config supports both a mapping format (with `modules:` key) and a legacy bare-sequence format. Only the mapping format can hold the top-level `age:` key. `config.Save` (used by `add` and `init`) always writes the mapping format by re-marshalling the struct, so it migrates legacy files and drops all comments and formatting.

**Key flow**: `cmd/dotular/main.go` parses CLI flags and loads config → `internal/registry/` resolves any remote module references → `internal/runner/runner.go` orchestrates applying modules with hooks/snapshots/audit → `internal/actions/` executes each item type.

**File/directory items**: The repo acts as the managed store. Each module's files live in a directory named after the module (e.g., `nvim/init.lua`). The runner's `buildAction` prepends the module name to the item's filename via `sourcePrefix`. `PlatformMap` handles per-OS destination paths.

**Action types** (in `internal/actions/`): `package`, `script`, `file`, `directory`, `binary`, `run`, `setting` — each implements the `Action` interface (`Describe()`, `Run()`). Some also implement `Idempotent` (`IsApplied()`).

**Cross-cutting concerns**: `internal/snapshot/` provides rollback per module, but the runner only records the destinations of `file` and `directory` items (`runner.go`, "snapshot destination before modification") — `package`, `script`, `binary`, `run`, and `setting` effects are not rolled back. `internal/audit/` logs all actions. `internal/tags/` filters modules by machine tags. `internal/ageutil/` handles age encryption for sensitive files.

## YAML Config Schema

Items are polymorphic — the type is determined by which primary field is set (`package`, `script`, `file`, `directory`, `binary`, `run`, `setting`). Fields honoured on every type: `skip_if`, `verify`, `hooks`. `via` is parsed on every type but only read for `package` (the package manager) and `script` (`remote`/`local`); on the other five it is silently ignored.

`PlatformMap` accepts either a scalar (all platforms) or a `macos`/`windows`/`linux` mapping. It has custom YAML marshal/unmarshal methods.

## CLI Commands

All 16 top-level commands are registered in `newRootCmd` (`cmd/dotular/main.go`):

- `dotular version` — print version, commit, and build date
- `dotular init` — scan machine against registry and suggest modules to adopt
- `dotular add <path> [module]` — add a file or directory to a module (creates module if needed); `--link`, `--direction`
- `dotular apply [module...]` — apply all or named modules
- `dotular push [module...]` — apply with `direction` forced to push on file/directory items
- `dotular pull [module...]` — same, forced to pull
- `dotular sync [module...]` — same, forced to sync
- `dotular list` — list modules and item counts
- `dotular status` — verbose dry-run showing all actions
- `dotular platform` — print detected OS
- `dotular verify [module...]` — run `verify:` commands only; exits non-zero on failure
- `dotular encrypt <file>` — write `<file>.age`
- `dotular decrypt <file.age>` — write `<file>`
- `dotular tag list|add <tag>` — machine tags in `~/.config/dotular/machine.yaml`
- `dotular log` — print the audit log; `--module`, `--limit` (default 50)
- `dotular registry list|clear|update` — remote index by default, `list --cached` reads `dotular.lock.yaml`

Persistent flags: `--config`/`-c` (default `dotular.yaml`), `--dry-run`, `--verbose`/`-v`, `--no-atomic`, `--no-cache`.

## Dependencies

- `github.com/spf13/cobra` — CLI framework
- `gopkg.in/yaml.v3` — YAML parsing
- `filippo.io/age` — age encryption
