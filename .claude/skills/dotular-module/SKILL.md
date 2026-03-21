---
name: dotular-module
description: Use when user asks to create a dotular module, add a tool to dotular, or manage a tool's config with dotular. Triggers on phrases like "create a module for X", "add X to dotular", "make a dotular module for X".
---

# Create Dotular Registry Module

Create a complete dotular registry module for a tool by researching its config files, scanning the local machine, and writing the module YAML.

## Process

```dot
digraph module_creation {
    "User requests module for tool X" [shape=doublecircle];
    "Web search for tool's config" [shape=box];
    "Scan local machine for found paths" [shape=box];
    "Present summary table" [shape=box];
    "User confirms?" [shape=diamond];
    "User wants changes?" [shape=diamond];
    "Adjust items" [shape=box];
    "Create module files" [shape=box];
    "Display result" [shape=doublecircle];

    "User requests module for tool X" -> "Web search for tool's config";
    "Web search for tool's config" -> "Scan local machine for found paths";
    "Scan local machine for found paths" -> "Present summary table";
    "Present summary table" -> "User confirms?";
    "User confirms?" -> "Create module files" [label="yes"];
    "User confirms?" -> "User wants changes?" [label="no"];
    "User wants changes?" -> "Adjust items" [label="yes"];
    "Adjust items" -> "Present summary table";
    "User wants changes?" -> "Display result" [label="cancel"];
    "Create module files" -> "Display result";
}
```

## Step 1: Research (MANDATORY — do not skip)

You MUST use WebSearch to find the following for the tool across **all three platforms** (macOS, Linux, Windows). Do NOT rely on your training data alone — tools change their config locations across versions.

Search for:
- **Package manager names**: brew/brew-cask, apt, dnf, pacman, choco, winget, scoop, snap, flatpak
- **Config file and directory locations** per OS (check the tool's official docs)
- **macOS defaults domains** (for `setting` items) if the tool writes to `defaults`
- **Post-install setup commands** (for `run` items)
- **Verification command** (usually `tool --version`)
- **Install scripts** (for `script` items, e.g., curl-pipe-bash installers)

## Step 2: Local Scan

For each config path found in research, check if it exists on the user's machine:

```bash
# Expand ~ and check existence
ls -la ~/.config/toolname/ 2>/dev/null
ls -la ~/Library/Application\ Support/ToolName/ 2>/dev/null
```

Record which files/directories exist. This validates your research and identifies files available to copy into the module store.

## Step 3: Present Summary

Show a structured summary using this format:

```
## Proposed module: <tool-name>

### Packages
| Platform | Package | Via | skip_if |
|----------|---------|-----|---------|
| macOS | tool | brew | command -v tool |
| Linux | tool | apt | command -v tool |
| Windows | tool | winget | |

### Config Files & Directories
| Type | Name | Exists? | macOS | Linux | Windows |
|------|------|---------|-------|-------|---------|
| file | config.yml | Yes | ~/.config/tool | ~/.config/tool | %APPDATA%\tool |
| directory | themes | No | ~/.config/tool/themes | ~/.config/tool/themes | %APPDATA%\tool\themes |

### Settings (if any)
| Domain | Key | Value |
|--------|-----|-------|
| com.example.tool | ShowSidebar | true |

### Post-install Commands (if any)
| Command | After |
|---------|-------|
| tool setup | package |

### Recommendation
Include: [items to include and why]
Skip: [items to skip and why]
```

Include a clear recommendation. Wait for user confirmation before proceeding.

## Step 4: Execute

After user confirms:

1. **Create module directory** (if config files exist to store):
   ```bash
   mkdir -p modules/<tool>/
   ```

2. **Copy existing config files** into the module store:
   ```bash
   cp ~/.config/tool/config.yml modules/<tool>/config.yml
   ```
   File placement rule: files go at `modules/<tool>/<filename>` where `<filename>` matches the `file:` value in the YAML. The runner prepends the module name as `sourcePrefix`.

3. **Write module YAML** at `modules/<tool>.yaml`:

   ```yaml
   name: <tool-name>
   version: "1.0.0"
   items:
     - package: <name>
       via: brew
       skip_if: command -v <name>
       verify: <name> --version

     - package: <name>
       via: apt
       skip_if: command -v <name>

     - package: <name-or-id>
       via: winget

     - file: <filename>
       destination:
         macos: <path>
         linux: <path>
         windows: <path>
   ```

   **Multiple package items**: Create a separate `package` item for each platform's package manager (see `dotular.yaml` VS Code module for this pattern). Use `skip_if: command -v <tool>` so only the available manager runs. The runner executes all items — an unavailable package manager simply fails, but `skip_if` prevents redundant installs after the first succeeds.

4. If **no config files** to store (package-only module), skip creating the subdirectory.

## Step 5: Verify

Display the complete contents of the generated `modules/<tool>.yaml` for user review.

## YAML Field Reference

| Field | When to use | Notes |
|-------|-------------|-------|
| `skip_if` | Package and script items | `command -v <tool>` for idempotency |
| `verify` | Package and binary items | Usually `tool --version` |
| `direction` | Omit unless pull/sync needed | Defaults to `push` |
| `link` | When symlink management preferred | Set `true`; omit for copy (default) |
| `permissions` | Sensitive files (credentials, tokens) | Use `"0600"` |
| `after` | Run items that depend on package install | Set to `package` |
| `via` | Required for package/script items | See package managers list below |

### Package managers by platform

| Platform | Managers |
|----------|----------|
| macOS | `brew`, `brew-cask`, `mas` |
| Linux | `apt`, `dnf`, `pacman`, `snap`, `flatpak`, `nix` |
| Windows | `winget`, `choco`, `scoop` |
| Cross-platform | `flatpak`, `nix` |

## Common Mistakes

| Mistake | Fix |
|---------|-----|
| Relying on training data for config paths | ALWAYS web search — paths change between versions |
| Missing `skip_if` on package items | Add `skip_if: command -v <tool>` for idempotency |
| Using trailing `/` in destination | Match existing convention: `~/.config/tool` not `~/.config/tool/` |
| Forgetting Windows paths | Always research all 3 platforms for PlatformMap |
| Editing user's `dotular.yaml` | This skill creates REGISTRY modules only — never touch personal config |
| Updating `modules/index.yaml` | Out of scope — tell user to add the entry manually |

## Out of Scope

- Editing the user's personal `dotular.yaml`
- Updating `modules/index.yaml` (remind user to do this manually)
- Encryption (`encrypted` field) or hooks
- Parameters/templating
