# dotular

A modular, cross-platform dotfile manager. Define your entire system setup — packages, files, binaries, and scripts — in a single `dotular.yaml`, then apply it on any machine.

## Why dotular?

Most dotfile managers only manage **files**. You still need a separate bootstrap script to install packages, download binaries, configure OS settings, and glue everything together. That script inevitably becomes a fragile, untested mess of `if` statements for each platform.

dotular replaces all of that with a single declarative YAML file.

### How it compares

| | **dotular** | **chezmoi** | **GNU Stow** | **yadm** | **mackup** |
|---|---|---|---|---|---|
| Manages files | Yes | Yes | Yes | Yes | Yes |
| Installs packages | Yes — brew, apt, winget, and 11 more | No | No | No | No |
| Downloads binaries | Yes — archives, extraction, versioning | No | No | No | No |
| Runs scripts | Yes — local and remote, with skip/verify | Templates only | No | Bootstrap only | No |
| OS settings | Yes — macOS `defaults`, Windows registry | No | No | No | No |
| Cross-platform config | One file, per-OS paths and packages | Separate templates | Symlinks only | Git + encryption | macOS only |
| Best-effort rollback | Per-module transactional rollback across supported side effects | No | No | No | No |
| No templating language | Plain YAML — no Go templates to learn | Go `text/template` | N/A | Jinja2 (alt) | N/A |
| Shareable modules | Registry with parameters and overrides | Community scripts | No | No | No |
| Audit log | Built-in, append-only JSON | No | No | No | No |

### The core idea

A "module" in dotular groups everything a tool needs — the package install, its config files, post-install scripts, binary downloads, and OS settings — into one unit. Apply a single module to fully set up one tool. Apply all modules to bootstrap an entire machine.

```yaml
- name: Neovim
  items:
    - binary: nvim                          # download the binary
      source:
        macos: https://...nvim-macos.tar.gz
        linux: https://...nvim-linux.tar.gz
      install_to: ~/.local/bin
    - directory: nvim                       # push config files
      destination: ~/.config
    - run: nvim --headless "+Lazy sync" +qa # install plugins
```

No bootstrap script. No platform `if`-statements. One file, any machine.

---

## Features

- **Modules** — group related items; apply one or all
- **Cross-platform** — macOS, Linux, and Windows; per-OS package managers and destinations
- **File direction** — `push` (repo→system), `pull` (system→repo), or `sync` (bidirectional with conflict prompt)
- **Symlinks** — `link: true` creates a symlink instead of copying
- **Idempotency** — skips already-applied packages and symlinks automatically
- **Hooks** — shell commands before/after module or file item
- **Verification** — health-check commands per item (`verify:`)
- **Encrypted secrets** — `age`-encrypted files, decrypted on apply
- **File permissions** — enforce `chmod`-style permissions on pushed files
- **Best-effort transactional rollback** — prepare filesystem snapshots and typed or explicit compensation before mutating a module; unwind on failure
- **Machine tagging** — `only_tags`/`exclude_tags` per module
- **Audit log** — append-only log of every action taken
- **Registry** — reusable remote modules with parameters and overrides
- **`skip_if`** — skip an item when a shell condition exits zero

---

## Installation

Requires Go 1.23+.

```sh
git clone https://github.com/atomikpanda/dotular
cd dotular
go build -o dotular ./cmd/dotular
```

Or install directly:

```sh
go install github.com/atomikpanda/dotular/cmd/dotular@latest
```

---

## Quick start

```sh
dotular init               # scan this machine, suggest registry modules to adopt
dotular add ~/.zshrc shell # manage a file under the "shell" module
dotular apply              # apply all modules
dotular apply homebrew     # apply a single module
dotular status             # dry-run with verbose output
dotular list               # list all modules
```

---

## Configuration

`dotular.yaml` (or pass `--config path/to/file.yaml`):

```yaml
# Optional: age encryption key
age:
  identity: ~/.config/dotular/identity.txt   # age identity file
  # passphrase: env:MY_AGE_PASSPHRASE        # or passphrase (supports env: prefix)

modules:
  - name: My Module
    only_tags: [darwin]          # optional: only run on matching machines
    exclude_tags: [work]         # optional: skip on matching machines
    hooks:
      before_apply: echo "starting"
      after_apply:  echo "done"
      rollback:
        before_apply: echo "undo starting"
        after_apply:  echo "undo done"
    items:
      - run: mkdir -p ~/.cache/my-module
        rollback: rm -rf ~/.cache/my-module
```

### Config file formats

Two on-disk formats are accepted:

| Format | Shape | Global keys (`age:`) |
|--------|-------|----------------------|
| Mapping (current) | `modules:` key holding the module list | Yes |
| Legacy | a bare top-level sequence of modules | No — there is nowhere to put them |

Both parse identically into modules, so the legacy format keeps working. Only the
mapping format can express `age:`, so an encrypted-secrets setup requires it.

`dotular add` and `dotular init` rewrite the config file, and they always write
the mapping format — a legacy file is silently migrated on the first write.
The rewrite re-marshals the config from scratch, so **comments, key order, and
formatting in the file are lost**. Commit before running either command.

### Item types

#### `package` — install via package manager

```yaml
- package: ripgrep
  via: brew           # see the table below for every accepted value
  skip_if: command -v rg
  verify: rg --version
  rollback: brew uninstall ripgrep  # fallback if exact package capture is unavailable
```

Supported package managers and the platform each one is bound to:

| `via`       | Platform      | Install command |
|-------------|---------------|-----------------|
| `brew`      | macOS         | `brew install` |
| `brew-cask` | macOS         | `brew install --cask` |
| `mas`       | macOS         | `mas install` |
| `apt`       | Linux         | `sudo apt-get install -y` |
| `apt-get`   | Linux         | `sudo apt-get install -y` |
| `dnf`       | Linux         | `sudo dnf install -y` |
| `yum`       | Linux         | `sudo yum install -y` |
| `pacman`    | Linux         | `sudo pacman -S --noconfirm` |
| `snap`      | Linux         | `sudo snap install` |
| `flatpak`   | any           | `flatpak install -y` |
| `nix`       | any           | `nix-env -iA` |
| `winget`    | Windows       | `winget install --id` |
| `choco`     | Windows       | `choco install -y` |
| `scoop`     | Windows       | `scoop install` |

A package item whose `via` is bound to one platform is **skipped** on the others,
so a single config can list the brew, apt, and winget spelling of the same tool.
`flatpak` and `nix` are treated as cross-platform and are never skipped.

Package items are **idempotent** — dotular checks whether the package is already installed before running the install command.

#### `script` — run a shell script

```yaml
- script: scripts/install-shell-tools.sh
  via: local
  skip_if: command -v shell-tool
  verify: shell-tool --version
  rollback: scripts/uninstall-shell-tools.sh
```

`via: remote` downloads the script to a temp file and runs it. `via: local` runs the path as a local script.

#### `file` — sync a config file

```yaml
- file: settings.json
  direction: sync        # push | pull | sync (default: push)
  link: false            # true to create a symlink instead of copying
  permissions: "0600"    # optional chmod
  encrypted: false       # true if the repo copy is .age-encrypted
  destination:
    macos: ~/Library/Application Support/Code/User
    windows: '%APPDATA%\Code\User'
    linux: ~/.config/Code/User
  hooks:
    before_sync: echo "about to sync"
    after_sync:  echo "sync complete"
  verify: test -f ~/Library/Application\ Support/Code/User/settings.json
```

`destination` accepts either a plain string (all platforms) or a per-OS mapping.

Per-platform YAML maps accept only `macos`, `linux`, and `windows`. The
`dotular platform` command prints Go runtime names, so it prints `darwin` on
macOS; use `macos:` in YAML, not `darwin:`.

#### `directory` — sync a whole directory tree

```yaml
- directory: nvim
  direction: push
  destination: ~/.config
  link: false
```

`sync` direction: pushes if only the repo copy exists, pulls if only the system copy exists, pushes if both exist. For per-file conflict resolution use individual `file` items.

#### `binary` — download and install a binary

```yaml
- binary: nvim
  version: "0.10.2"
  source:
    macos: https://github.com/neovim/neovim/releases/download/v0.10.2/nvim-macos-arm64.tar.gz
    linux: https://github.com/neovim/neovim/releases/download/v0.10.2/nvim-linux-x86_64.tar.gz
  install_to: ~/.local/bin
  skip_if: test -f ~/.local/bin/nvim
  verify: nvim --version
```

Downloads the archive (`.tar.gz`, `.tgz`, `.zip`, or plain binary), extracts the matching binary by name, and installs it with `chmod 755`.

`install_to` is optional and defaults to `~/.local/bin`. An item is skipped when `source` has no entry for the current platform.

#### `run` — inline shell command

```yaml
- run: mkdir -p ~/.cache/my-tool
  rollback: rm -rf ~/.cache/my-tool
  after: directory     # informational only — ordering follows declaration order
```

#### `setting` — write an OS preference

```yaml
- setting: com.apple.dock       # macOS bundle ID, or a Windows registry path
  key: autohide
  value: true                   # bool | int | float | string
  rollback: defaults delete com.apple.dock autohide  # fallback if exact capture is unavailable
```

The command used depends on the platform the apply runs on:

| Platform | Command | Value mapping |
|----------|---------|---------------|
| macOS    | `defaults write <setting> <key> …` | bool → `-bool`, int → `-int`, float → `-float`, string → `-string` |
| Windows  | `reg add <setting> /v <key> …` | bool/int → `REG_DWORD`, float/string → `REG_SZ` |

On Windows, `setting:` is the registry path (e.g. `HKCU\Control Panel\Desktop`)
and booleans are written as `1`/`0`. `setting` items are not supported on Linux.

---

## Common item fields

| Field       | Description |
|-------------|-------------|
| `skip_if`   | Shell command — skip this item if it exits zero |
| `verify`    | Shell command — run after apply and on `dotular verify`; fails the item if non-zero |
| `rollback`  | Shell compensation for `script`/`run`, or fallback for `package`/`setting`; rejected for `file`/`directory`/`binary` |
| `hooks`     | `before_apply`, `after_apply`, `before_sync`, `after_sync`; optional compensations use the same keys under `hooks.rollback` |

Every `hooks.rollback.<name>` requires the matching forward `hooks.<name>`.
Rollback commands cannot be blank. These rules apply to module and item hooks.

---

## CLI reference

### `init`

```sh
dotular init
```

Fetches the module registry, scans this machine for the packages and config files
those modules manage, and lets you pick which ones to adopt. Selected modules are
appended to the config as `from:` registry references. In a non-interactive shell
the picker is skipped and only fully-matching modules are added. `init` does not
infer `only_tags` or `exclude_tags`; add machine policy explicitly in the config.

### `add`

```sh
dotular add ~/.config/nvim nvim
dotular add ~/.zshrc shell --direction sync
dotular add ~/.config/nvim/init.lua nvim --link
dotular add ~/.zshrc
```

Copies a file or directory into the module's managed store (a directory named
after the module, next to the config file) and records it as a `file:` or
`directory:` item. The module is created if it does not exist. The item's
`destination` is set to the source's parent directory, for the current platform
only — add the other platforms' paths by hand.

If the module name is omitted, dotular infers it from the registry or prompts.

| Flag          | Description |
|---------------|-------------|
| `--link`      | Record `link: true`, so apply symlinks instead of copying (default `false`) |
| `--direction` | Accepts only `push`, `pull`, or `sync`, and records the selected `direction:` (default `push`) |

This command rewrites the config file — see [Config file formats](#config-file-formats).

### `apply`

```sh
dotular apply [module...]
dotular apply --dry-run
dotular apply --no-atomic
dotular apply --rollback-timeout 30s
dotular apply homebrew --ignore-tags
```

Apply all modules (or specified ones). Runs hooks, checks idempotency, and uses
best-effort transactional rollback by default. `--rollback-timeout` bounds
contextual compensation commands and defaults to two minutes; filesystem
snapshot restoration/cleanup may continue. Tag filters also apply when modules
are named explicitly; `--ignore-tags` is the deliberate override.

### `push` / `pull` / `sync`

```sh
dotular push [module...]
dotular pull [module...]
dotular sync [module...]
dotular sync --rollback-timeout 30s
```

Tag filters apply to all three commands, including named modules. Pass
`--ignore-tags` to override them for one invocation. Like `apply`, all three
commands accept `--rollback-timeout` with a two-minute default.

These commands override `direction` on all file and directory items for the
run. Link items (`link: true`) are never overridden.

`pull` and `sync` reconcile files only: `package`, `script`, `binary`, `run`,
and `setting` items are skipped for the run (listed with `--verbose`, recorded
in the audit log). `push` behaves like `apply` and runs them.

### `verify`

```sh
dotular verify [module...]
dotular verify "Work Tools" --ignore-tags
```

Run all `verify:` commands without modifying anything. Exits 1 if any check fails.
Tag filters apply to named modules unless `--ignore-tags` is present.

### `status`

```sh
dotular status
dotular status --ignore-tags
```

Dry-run with verbose output — shows what would be applied. Pass `--ignore-tags`
to include modules that are inactive on this machine.

### `list`

```sh
dotular list
```

Print all modules in config order. Active modules include item counts; inactive
modules show `skipped (tag mismatch)`. Inactive remote modules are not fetched,
so no item count is fabricated for them.

### `platform`

```sh
dotular platform
```

Print the detected OS (`darwin` / `linux` / `windows`).

### `version`

```sh
dotular version
```

Print the version, commit, and build date stamped into the binary.

### `encrypt` / `decrypt`

```sh
dotular encrypt secrets/file.txt      # writes secrets/file.txt.age
dotular decrypt secrets/file.txt.age  # writes secrets/file.txt
```

Requires `age.identity` or `age.passphrase` in config, or `DOTULAR_AGE_IDENTITY` / `DOTULAR_AGE_PASSPHRASE` env vars.

### `tag`

```sh
dotular tag list
dotular tag add work
```

Manage machine tags stored in `~/.config/dotular/machine.yaml`. Tags auto-detected on first run include OS, architecture, and hostname.

### `log`

```sh
dotular log
dotular log --module homebrew
dotular log --limit 20
```

Show the audit log at `~/.local/share/dotular/history.log`. `--limit` defaults to
the 50 most recent entries; `--module` filters by module name.

### `registry`

```sh
dotular registry list           # fetch and print the remote module index
dotular registry list --cached  # print the modules pinned in dotular.lock.yaml
dotular registry clear          # remove all cached modules
dotular registry update         # re-fetch modules and update their pins
```

`registry list` reaches the network by default and prints the name and version of
every module in the official index. `--cached` needs no network: it reads
`dotular.lock.yaml` and prints each pinned ref with its trust level and fetch time.

`registry update` stages every unique active registry ref before making changes,
then prints one tab-separated `REF OLD NEW` row per ref in lexical ref order. A
missing old checksum is printed as `none`. Once staging succeeds, all rows are
printed even if a later cache-path collision, cache preparation, lock save, or
cache publication fails; a staging failure prints no rows. Inactive pins and
their cache paths are preserved. A cache-path collision involving an active ref
fails without migrating either ref.

### Global flags

| Flag          | Description |
|---------------|-------------|
| `--config`    | Path to config file (default `dotular.yaml`) |
| `--dry-run`   | Print actions without executing |
| `--verbose`   | Show skipped items and extra output |
| `--no-atomic` | Bypass runtime snapshot capture, compensation, and rollback warnings/events |
| `--no-cache`  | Re-fetch registry modules without changing existing pins |

---

## Machine tagging

Add tags to a machine to control which modules run on it:

```sh
dotular tag add work
dotular tag add desktop
```

Then in your config:

```yaml
- name: Work Tools
  only_tags: [work]
  items:
    - package: slack
      via: brew-cask

- name: Gaming
  exclude_tags: [work]
  items:
    - package: steam
      via: brew-cask
```

Tag filters gate `apply`, `push`, `pull`, `sync`, `verify`, and `status` whether
all modules or explicit module names are requested. Use `--ignore-tags` when a
one-off override is intentional.

Filtering happens before registry resolution. An inactive remote module is not
downloaded, rendered, cached, or written to `dotular.lock.yaml`. `dotular list`
still shows it as `skipped (tag mismatch)`.

`dotular init` does not infer tag policy from the current OS, architecture, or
scan results. Registry modules do not carry that policy, and inferred filters
would make a cross-platform config machine-specific.

---

## Encrypted secrets

1. Configure an age key:
   ```yaml
   age:
     identity: ~/.config/dotular/identity.txt
   ```

2. Encrypt a file:
   ```sh
   dotular encrypt ~/.ssh/config
   # writes ~/.ssh/config.age — commit this file
   ```

3. Reference the encrypted file in your config:
   ```yaml
   - file: .ssh/config.age
     encrypted: true
     destination: ~/.ssh
     permissions: "0600"
   ```

On apply, dotular decrypts to a temp file and copies it to the destination.

---

## Registry modules

Reuse and share module definitions:

```yaml
modules:
  - from: neovim
    with:
      neovim_version: "0.10.2"
    override:
      - directory: nvim
        direction: push
        destination: ~/.config
```

### How it works

1. dotular fetches the remote YAML module definition.
2. Parameters from `with:` (merged with module defaults) are applied via Go templates.
3. `override:` items are merged by `(type, primary-value)` — unmatched overrides are appended.
4. On the first successful fetch, a lockfile (`dotular.lock.yaml`) records the module's
   SHA-256 checksum before resolution succeeds. Ordinary commands enforce that pin,
   including with `--no-cache`.

### Trust levels

| Source | Trust |
|--------|-------|
| `github.com/atomikpanda/dotular/...` or bare name | Official |
| Other `github.com/...` repos | GitHub |
| Other URLs | External |

Bare names (e.g. `neovim`) expand to `github.com/atomikpanda/dotular/modules/neovim@main`. GitHub refs are automatically rewritten to `raw.githubusercontent.com`.

### Cache

Remote modules are cached at `~/.cache/dotular/registry/`. Use `--no-cache` to
re-fetch while preserving and verifying existing pins. Ordinary resolution
continues to reject checksum drift; `dotular registry update` is the sole
command that explicitly authorizes replacing existing pins.

---

## Best-effort transactional rollback

By default, `apply`, `push`, `pull`, and `sync` prepare one in-process
transaction per module. This is **best-effort transactional rollback**, not
database-style atomicity. Before the module's first mutating hook or action,
dotular validates explicit rollback commands and captures every supported
pre-state:

- `file` and `directory` items snapshot whichever side their effective
  direction may write. `binary` items snapshot the exact install destination.
  Existing paths, contents, permissions, and links are restored. If an action
  creates missing parent directories, rollback removes the highest created
  ancestor below the nearest pre-existing parent without deleting that parent.
- A package installed by the transaction is automatically uninstalled only
  when the package manager query proves that exact package was previously
  absent. A package proved present is skipped. Unsupported, failed, malformed,
  or imprecise state queries never authorize uninstall; `nix` package
  attributes in particular cannot be mapped exactly to installed names.
- On macOS, setting rollback can restore prior string, boolean, integer, and
  float values. On Windows it can restore `REG_SZ`, `REG_EXPAND_SZ`,
  `REG_BINARY`, `REG_DWORD`, `REG_MULTI_SZ`, and `REG_QWORD` values. A key is
  deleted only when capture proves it was absent. Unsupported types and
  inconclusive or failed capture never guess at prior state.
- `script` and `run` items have no automatic compensation. Give them an
  explicit item `rollback`. Hooks likewise use their matching command under
  `hooks.rollback`.

Typed automatic package or setting compensation takes precedence over an
explicit item rollback. The explicit command is used only when typed capture is
unavailable; it is not retried if a prepared automatic compensation later
fails. Filesystem-backed `file`, `directory`, and `binary` items use snapshots
and reject item rollback commands.

Missing compensation for an applicable arbitrary command or hook emits a
warning **before mutation and continues**. If the module later unwinds, that
operation is reported as `uncompensated`.

An action, hook, or item `verify` error, command cancellation, or panic starts
rollback. Each attempted step is registered before it runs, so even the failing
step is considered. Contextual compensations run in strict LIFO order on a
fresh context and continue after ordinary failures. Once `--rollback-timeout`
expires, dotular stops starting contextual compensation commands; the default
timeout is two minutes. Context-free filesystem snapshot restore and discard
may continue afterward, and their failures remain contextual errors. Panic
rollback re-panics with the original value.

A failed transaction exits nonzero even when every compensation succeeds, and
the affected module reports `0 applied`. Detailed terminal and audit output
use `rolled_back`, `rollback_failed`, and `uncompensated`; hooks are detailed
without inflating item counts. Rollback failures do not prevent earlier
compensations from being attempted.

If final snapshot discard fails after all forward work succeeds, the command
exits nonzero but does not start rollback or relabel the committed forward
outcomes.

Pass `--no-atomic` to a mutating command to bypass runtime preflight, state
capture, compensation, warnings, and rollback events. Strict YAML decoding,
configuration validation, and template validation still apply.

The first supported termination signal (SIGINT, and SIGTERM on Unix) cancels
forward work and starts rollback. A second supported termination signal
terminates immediately, whether rollback has started or not.
SIGKILL, power loss, kernel failure, and process death cannot be recovered.
Package dependency removal, automatic reversal of undeclared command effects,
compensation retries, durable recovery, and restartable transactions are also
out of scope.

---

## Audit log

Every forward action is appended to
`~/.local/share/dotular/history.log` as JSON lines. Rollback attempts add
`phase: "rollback"`, a `scope` of `module` or `item`, the target and operation
in `item`, and one of the structured outcomes `rolled_back`,
`rollback_failed`, or `uncompensated`:

```json
{"time":"2026-08-15T12:00:00Z","command":"apply","module":"editor","item":"run \"install-plugin\"","outcome":"applied"}
{"time":"2026-08-15T12:00:01Z","command":"apply","module":"editor","item":"run \"install-plugin\" [action]","phase":"rollback","scope":"item","outcome":"rolled_back"}
```

The final summary reports committed `applied` items separately from rollback
operation counts. Each attempted item has one final item outcome; hook and
filesystem rollback details do not inflate the applied item count.

---

## Makefile

```sh
make build        # build the binary to ./build/dotular
make run ARGS=".."  # go run ./cmd/dotular with ARGS
make tidy         # go mod tidy
make index        # regenerate modules/index.yaml from modules/*.yaml
make test-list    # run dotular list
make test-status  # run dotular status
make test-apply-dry  # run dotular apply --dry-run
make clean        # remove binary
```

Run `make index` after adding or bumping a module under `modules/` — CI checks
that `modules/index.yaml` matches the module files.
