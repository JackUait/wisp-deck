# Ghost Tab

A **`Ghostty`** + **`tmux`** wrapper that launches a three-pane dev session with an AI coding tool (**`Claude Code`** or **`OpenCode`**), **`lazygit`**, and a spare terminal. Automatically cleans up all processes when the window is closed — no zombie processes.

<p>
  <img src="docs/screenshot-selector.png" width="49%" />
  <img src="docs/screenshot-session.png" width="49%" />
</p>

---

## Quick Start

```sh
npx ghost-tab
```

That's it — only requirements are **`macOS`** and **`Node.js 16+`**. Everything (**`Ghostty`**, **`tmux`**, **`lazygit`**, and your chosen AI tool) is installed automatically.

---

## Usage

**Step 1.** Open a new **`Ghostty`** window (`Cmd+N`)

**Step 2.** Use the interactive project selector:

```
⬡  Ghost Tab
──────────────────────────────────────

 1❯ my-app
    ~/Projects/my-app
  2 another-project
    ~/Projects/another-project
──────────────────────────────────────
  A Add new project
  D Delete a project or a worktree
  O Open once
  P Plain terminal
──────────────────────────────────────
  ↑↓ navigate  ⏎ select
```

- **Arrow keys** or **mouse click** to navigate
- **Number keys** (1-9) to jump directly to a project
- **Letter keys** — **A** add, **D** delete, **O** open once, **P** plain terminal
- **Enter** to select
- **Path autocomplete** when adding projects (with Tab completion)
- **Plain terminal** opens a bare shell with no tmux overhead

**Step 3.** The three-pane **`tmux`** session launches automatically with **`Claude Code`** already focused — start typing your prompt right away.

> [!TIP]
> You can also open a specific project directly from the terminal:
> ```sh
> ~/.config/ghostty/claude-wrapper.sh /path/to/project
> ```

---

## Hotkeys

| Shortcut | Action |
|---|---|
| `Cmd+T` | New tab |
| `Cmd+Shift+Left` | Previous tab |
| `Cmd+Shift+Right` | Next tab |
| `Left Option` | Acts as `Alt` instead of typing special characters |

---

## What `ghost-tab` Does

1. Downloads **`tmux`**, **`lazygit`**, and **`jq`** natively (no package manager required)
2. Installs **`Claude Code`** via native installer (auto-updates)
3. Prompts to install **`Ghostty`** from ghostty.org if not already installed
4. Sets up the **`Ghostty`** config (with merge option if you have an existing one)
5. Walks you through adding your **project directories**
6. Installs **`Node.js`** LTS (if needed) and sets up **Claude Code status line** showing git info and context usage
7. Auto-updates automatically — just run `npx ghost-tab` again to get the latest version

---

## Status Line

The `ghost-tab` command configures a custom **Claude Code** status line based on [Matt Pocock's guide](https://www.aihero.dev/creating-the-perfect-claude-code-status-line):

```
my-project | main | S: 0 | U: 2 | A: 1 | 23.5%
```

- **Repository name** — current project
- **Branch** — current git branch
- **S** — staged files count
- **U** — unstaged files count
- **A** — untracked (added) files count
- **Context %** — how much of Claude's context window is used

> [!TIP]
> Monitor context usage to know when to start a new conversation. Lower is better.

---

## Process Cleanup

> [!CAUTION]
> When you close the **`Ghostty`** window, **all processes are force-terminated** — make sure your work is saved.

The wrapper automatically:

1. **Recursively kills** the full process tree of every **`tmux`** pane (including deeply nested subprocesses spawned by **`Claude Code`**, **`lazygit`**, etc.)
2. **Force-kills** (`SIGKILL`) any processes that ignored the initial `SIGTERM` after a brief grace period
3. **Destroys** the **`tmux`** session
4. **Self-destructs** the session via `destroy-unattached` if the **`tmux`** client disconnects without triggering cleanup

This prevents zombie **`Claude Code`** processes from accumulating.

---

## Architecture

Ghost Tab uses a **hybrid architecture**:

**Layer 1: Go TUI Binary (`ghost-tab-tui`)**
- Interactive terminal UI components built with Bubbletea
- Project selector, AI tool selector, settings menu, input forms
- Outputs structured JSON for bash consumption
- Binary: `~/.local/bin/ghost-tab-tui`

**Layer 2: Bash Orchestration (`ghost-tab`)**
- Entry point and session orchestration
- Process management, config file operations
- Calls ghost-tab-tui for interactive parts
- Parses JSON responses with jq
- Script: `~/.local/bin/ghost-tab`

**Dependencies:**
- Go 1.21+ (for building)
- jq (for JSON parsing)
- tmux (session management)
- Ghostty (terminal emulator)

**Communication:**
```bash
# Bash calls Go with subcommand
result=$(ghost-tab-tui select-project --projects-file ~/.config/ghost-tab/projects)

# Go returns JSON
{"name": "my-project", "path": "/home/user/code/my-project", "selected": true}

# Bash parses with jq
project_name=$(echo "$result" | jq -r '.name')
```
