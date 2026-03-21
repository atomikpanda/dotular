# Dotular Module Skill Design

## Purpose

A Claude Code skill that assists users in creating dotular registry modules for tools and applications. When a user asks to "create a module for X", the skill guides Claude through researching the tool's configuration, validating against the local machine, and producing a complete registry module.

## Output Artifact

A registry module file at `modules/<tool>.yaml` plus any config files placed in `modules/<tool>/`. The flat YAML file is always the module definition; the subdirectory holds config files referenced by `file:` and `directory:` items.

**File placement rule**: Config files must be placed at `modules/<tool>/<filename>` where `<filename>` matches the `file:` value in the YAML. The runner prepends the module name as a `sourcePrefix` when resolving file paths at apply time.

### Registry module format (reference: `modules/wezterm.yaml`)

```yaml
name: <tool-name>
version: "1.0.0"
items:
  - package: <name>
    via: <manager>
    skip_if: command -v <name>
    verify: <command>

  - file: <filename>
    destination:
      macos: <path>
      linux: <path>
      windows: <path>

  - directory: <dirname>
    destination:
      macos: <path>
      linux: <path>
      windows: <path>

  - setting: <bundle-id>
    key: <key>
    value: <value>

  - binary: <name>
    version: <version>
    source:
      macos: <url>
      linux: <url>
    install_to: <path>
    verify: <command>

  - run: <command>
    after: <dependency>

  - script: <url>
    via: remote
    skip_if: <condition>
```

### Field notes

- `direction`: Defaults to `push`, omit unless `pull` or `sync` is needed.
- `link`: Set to `true` for config files that benefit from symlink management. Omit for copy-based (default).
- `skip_if`: Use for idempotency on package and script items (e.g., `skip_if: command -v brew`).
- `verify`: Use to confirm successful installation (e.g., `tool --version`).
- `permissions`: Use `"0600"` for files containing credentials or tokens.
- `version`: Always `"1.0.0"` for new modules.

## Workflow

### Step 1: Research

Use web search to find across **all three platforms** (macOS, Linux, Windows):
- **Package manager names** per platform (brew, brew-cask, apt, choco, winget, scoop, etc.)
- **Standard config file/directory locations** per OS
- **macOS defaults domains** or Windows registry keys (setting items) if applicable
- **Common post-install commands** (run items)
- **Verification commands** (e.g., `tool --version`)
- **Installation scripts** if the tool uses a custom installer (script items)

### Step 2: Local Scan

For each config file/directory discovered in research:
- Expand shell paths (`~`, `$HOME`) before checking existence
- Check if it exists on the user's machine
- Record existence status for the summary

### Step 3: Present Summary

Show a structured list with:

```
## Module: <tool-name>

### Packages
| Platform | Package | Via | Status |
|----------|---------|-----|--------|
| macOS    | tool    | brew | skip_if: command -v tool |

### Config Files
| File/Dir | Exists? | Destination (macOS) | Destination (Linux) | Destination (Windows) |
|----------|---------|---------------------|---------------------|-----------------------|
| config.toml | Yes | ~/.config/tool | ~/.config/tool | %APPDATA%\tool |

### Settings (if any)
| Domain | Key | Value |
|--------|-----|-------|

### Post-install Commands (if any)
| Command | After |
|---------|-------|

### Recommendation
Include: [list of items to include]
Exclude: [list of items to skip and why]
```

### Step 4: User Confirmation

Ask for a single yes/no to proceed. User may also request items be removed or modified before confirming.

### Step 5: Execute

1. Create `modules/<tool>/` directory if config files need to be stored
2. Copy existing config files from their current locations into `modules/<tool>/`
3. Write `modules/<tool>.yaml` with the complete module definition
4. If no config files to store (package-only module), no subdirectory is needed

### Step 6: Verify

Display the resulting module file contents for user review.

## Item Type Coverage

| Item Type | Source | Example |
|-----------|--------|---------|
| `package` | Web research | `package: neovim`, `via: brew`, `skip_if: command -v nvim` |
| `file` | Web research + local scan | `file: init.lua`, destination per OS |
| `directory` | Web research + local scan | `directory: nvim`, destination per OS |
| `setting` | Web research | `setting: com.apple.finder`, key/value |
| `run` | Web research | `run: nvim --headless "+Lazy sync" +qa` |
| `script` | Web research | `script: https://install-url.sh`, `via: remote`, `skip_if: command -v tool` |
| `binary` | Web research | `binary: tool`, `source` per OS, `install_to`, `version` |

## Out of Scope

- Editing the user's personal `dotular.yaml`
- Encryption (`encrypted` field) or hooks configuration
- Parameters/templating in registry modules
- Updating `modules/index.yaml` (note: user must manually add new modules to the index for `dotular init` discovery)

## Skill Location

Project-local: `.claude/skills/dotular-module/SKILL.md`

## Trigger

User asks to "create a module for X", "add X to dotular", or "make a dotular module for X".
