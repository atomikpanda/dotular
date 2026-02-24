# dotular

A modular, cross-platform dotfile manager. Define your entire system setup — packages, files, binaries, and scripts — in a single `dotular.yaml`, then apply it on any machine.

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
- **Atomic applies** — snapshot files before each module; roll back on failure
- **Machine tagging** — `only_tags`/`exclude_tags` per module
- **Audit log** — append-only log of every action taken
- **Registry** — reusable remote modules with parameters and overrides
- **`skip_if`** — skip an item when a shell condition exits zero

---

## Installation

Requires Go 1.22+.

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

### Item types

#### `package` — install via package manager

```yaml
- package: ripgrep
  via: brew           # brew | brew-cask | apt | dnf | pacman | snap | winget | choco | scoop
  skip_if: command -v rg
  verify: rg --version
```

Supported package managers and their platforms:

| `via`      | Platform |
|------------|----------|
| `brew`     | macOS    |
| `brew-cask`| macOS    |
| `apt`      | Linux    |
| `dnf`      | Linux    |
| `pacman`   | Linux    |
| `snap`     | Linux    |
| `winget`   | Windows  |
| `choco`    | Windows  |
| `scoop`    | Windows  |

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

#### `run` — inline shell command

```yaml
- run: nvim --headless "+Lazy sync" +qa
  after: directory     # informational only — ordering follows declaration order
```

#### `setting` — macOS `defaults write`

```yaml
- setting: com.apple.dock
  key: autohide
  value: true           # bool | int | float | string
```

---

## Common item fields

| Field       | Description |
|-------------|-------------|
| `skip_if`   | Shell command — skip this item if it exits zero |
| `verify`    | Shell command — run after apply and on `dotular verify`; fails the item if non-zero |
| `hooks`     | `before_apply`, `after_apply`, `before_sync`, `after_sync` |

---

## CLI reference

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

Show the audit log at `~/.local/share/dotular/history.log`.

### `registry`

```sh
dotular registry list    # show cached registry modules
dotular registry clear   # remove all cached modules
dotular registry update  # re-fetch all modules from the network
```

### Global flags

| Flag          | Description |
|---------------|-------------|
| `--config`    | Path to config file (default `dotular.yaml`) |
| `--dry-run`   | Print actions without executing |
| `--verbose`   | Show skipped items and extra output |
| `--no-atomic` | Disable snapshot/rollback per module |
| `--no-cache`  | Re-fetch registry modules from the network |

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
  - from: dotular.dev/modules/neovim@1.0.0
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
4. A lockfile (`dotular.lock.yaml`) records SHA-256 checksums for reproducible fetches.

### Trust levels

| Source | Trust |
|--------|-------|
| `dotular.dev/modules/` | Official |
| `dotular.dev/community/` | Community |
| GitHub / arbitrary URL | Private |

GitHub refs (`github.com/user/repo@ref`) are automatically rewritten to `raw.githubusercontent.com`.

### Cache

Remote modules are cached at `~/.cache/dotular/registry/`. Use `--no-cache` or `dotular registry update` to re-fetch.

---

## Atomic applies

By default, dotular snapshots any files it will modify before running each module. If any item fails, the snapshot is restored. Disable with `--no-atomic`.

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
make build        # build the binary
make tidy         # go mod tidy
make test-list    # run dotular list
make test-status  # run dotular status
make test-apply-dry  # run dotular apply --dry-run
make clean        # remove binary
```
