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
| Atomicity | Snapshot + rollback of file and directory items, per module | No | No | No | No |
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
- **Atomic applies** — snapshot the destination of each `file` and `directory` item before its module runs; roll back on failure
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
      before_sync:  echo "syncing"
      after_sync:   echo "synced"
    items:
      - ...
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
- script: https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh
  via: remote          # remote | local (default: local)
  skip_if: command -v brew
  verify: brew --version
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
- run: nvim --headless "+Lazy sync" +qa
  after: directory     # informational only — ordering follows declaration order
```

#### `setting` — write an OS preference

```yaml
- setting: com.apple.dock       # macOS bundle ID, or a Windows registry path
  key: autohide
  value: true                   # bool | int | float | string
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
| `hooks`     | `before_apply`, `after_apply`, `before_sync`, `after_sync` |

---

## CLI reference

### `init`

```sh
dotular init
```

Fetches the module registry, scans this machine for the packages and config files
those modules manage, and lets you pick which ones to adopt. Selected modules are
appended to the config as `from:` registry references. In a non-interactive shell
the picker is skipped and only fully-matching modules are added.

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
| `--direction` | Record `direction:` — `push`, `pull`, or `sync` (default `push`) |

This command rewrites the config file — see [Config file formats](#config-file-formats).

### `apply`

```sh
dotular apply [module...]
dotular apply --dry-run
dotular apply --no-atomic
```

Apply all modules (or specified ones). Runs hooks, checks idempotency, handles rollback on failure.

### `push` / `pull` / `sync`

```sh
dotular push [module...]
dotular pull [module...]
dotular sync [module...]
```

Override the `direction` on all file and directory items for the run. Link items (`link: true`) are never overridden.

`pull` and `sync` reconcile files only: `package`, `script`, `binary`, `run`, and `setting` items are skipped for the run (listed with `--verbose`, recorded in the audit log). `push` behaves like `apply` and runs them.

### `verify`

```sh
dotular verify [module...]
```

Run all `verify:` commands without modifying anything. Exits 1 if any check fails.

### `status`

```sh
dotular status
```

Dry-run with verbose output — shows what would be applied.

### `list`

```sh
dotular list
```

Print all modules and their item counts.

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

### Global flags

| Flag          | Description |
|---------------|-------------|
| `--config`    | Path to config file (default `dotular.yaml`) |
| `--dry-run`   | Print actions without executing |
| `--verbose`   | Show skipped items and extra output |
| `--no-atomic` | Disable snapshot/rollback per module |
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
re-fetch while preserving and verifying existing pins. `dotular registry update`
is the sole command that explicitly authorizes replacing them.

---

## Atomic applies

By default, dotular snapshots the destination of every `file` and `directory` item before running each module. If any item in the module fails, the snapshot is restored. Disable with `--no-atomic`.

Only `file` and `directory` items are snapshotted. `package`, `script`, `binary`, `run`, and `setting` items are not covered — an installed package, an executed script, or a written OS setting is not undone by a rollback.

---

## Audit log

Every action is appended to `~/.local/share/dotular/history.log` as JSON lines:

```
TIME                  COMMAND   MODULE               OUTCOME   ITEM
2024-01-15 12:00:00   apply     homebrew             skipped   script "https://..."
2024-01-15 12:00:01   apply     Visual Studio Code   success   push settings.json -> ...
```

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
