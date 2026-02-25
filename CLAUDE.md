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

Go CLI dotfile manager using Cobra. Module path: `github.com/atomikpanda/dotular`, requires Go 1.22+.

**Config-driven**: A `dotular.yaml` file defines modules, each containing items (package installs, file syncs, scripts, settings, binaries, directory trees, inline commands). The config supports both a mapping format (with `modules:` key) and a legacy bare-sequence format.

**Key flow**: `cmd/dotular/main.go` parses CLI flags and loads config → `internal/registry/` resolves any remote module references → `internal/runner/runner.go` orchestrates applying modules with hooks/snapshots/audit → `internal/actions/` executes each item type.

**File/directory items**: The repo acts as the managed store. Each module's files live in a directory named after the module (e.g., `nvim/init.lua`). The runner's `buildAction` prepends the module name to the item's filename via `sourcePrefix`. `PlatformMap` handles per-OS destination paths.

**Action types** (in `internal/actions/`): `package`, `script`, `file`, `directory`, `binary`, `run`, `setting` — each implements the `Action` interface (`Describe()`, `Run()`). Some also implement `Idempotent` (`IsApplied()`).

**Cross-cutting concerns**: `internal/snapshot/` provides atomic rollback per module. `internal/audit/` logs all actions. `internal/tags/` filters modules by machine tags. `internal/ageutil/` handles age encryption for sensitive files.

## YAML Config Schema

Items are polymorphic — the type is determined by which primary field is set (`package`, `script`, `file`, `directory`, `binary`, `run`, `setting`). Shared fields: `via`, `skip_if`, `verify`, `hooks`.

`PlatformMap` accepts either a scalar (all platforms) or a `macos`/`windows`/`linux` mapping. It has custom YAML marshal/unmarshal methods.

## Dependencies

- `github.com/spf13/cobra` — CLI framework
- `gopkg.in/yaml.v3` — YAML parsing
- `filippo.io/age` — age encryption
