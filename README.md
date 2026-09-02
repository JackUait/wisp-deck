<p align="center">
  <img src="docs/wisp-deck-logo.png" alt="Wisp Deck ghost mascot in a terminal window" width="240" />
</p>

# Wisp Deck

Wisp Deck turns one command into a ready-to-use AI coding workspace on macOS: your chosen assistant, a live view of your git changes, and a spare shell, all arranged in tmux. Unlike a bare AI CLI launcher, it manages the whole workspace—from project selection and account routing to session restore and process cleanup—so you can start working quickly and close the window without leaving tools behind.

- **One command.** Run `npx wisp-deck`; Wisp Deck installs the supporting command-line tools it needs.
- **Three focused panes.** Work with your AI assistant, inspect live git changes, and keep a spare terminal ready.
- **Three AI tools.** Choose Claude Code, OpenCode, or Codex during setup or from the selector.
- **Live changes.** Review files, open full diffs, preview images, and discard selected changes from the ledger.
- **Session restore.** Reopen projects and supported AI conversations after a reboot.
- **Clean shutdown.** Closing the window stops the processes Wisp Deck started.

```sh
npx wisp-deck

# First run: choose Claude Code, OpenCode, or Codex
# Then: add a project and press Enter
# Result: AI assistant + live changes + spare terminal
```

## Contents

- [Getting Started](#getting-started)
- [Your Workspace](#your-workspace)
- [Project Selector](#project-selector)
- [Settings](#settings)
- [Claude Accounts and Subscriptions](#claude-accounts-and-subscriptions)
- [Stats](#stats)
- [Screenshots, Videos, and Files](#screenshots-videos-and-files)
- [Status Line](#status-line)
- [Hotkeys](#hotkeys)
- [Restore Your Sessions](#restore-your-sessions)
- [Update Wisp Deck](#update-wisp-deck)
- [Credits](#credits)

---

## Getting Started

> **Requirements:** macOS, [Ghostty](https://ghostty.org/), and Node.js 16+.

1. Run Wisp Deck from any terminal:

   ```sh
   npx wisp-deck
   ```

2. **Choose an AI assistant:** Claude Code, OpenCode, or Codex.
3. **Add a project:** press **A** in the selector and choose its folder.
4. **Open the workspace:** select the project and press **Enter**.

Wisp Deck installs your chosen AI tool and its supporting pieces—including tmux, jq, and the Wisp Deck TUI—during first-run setup. Ghostty is the only manual installation: if it is missing, Wisp Deck opens its download page and waits for you to install it.

After setup, open a new Ghostty window whenever you want the project selector. Starting a session takes only a project selection and **Enter**.

---

## Your Workspace

Every project opens with three focused areas:

- **AI assistant.** Claude Code, OpenCode, or Codex starts in your project and is ready for a prompt.
- **Live changes.** An auto-refreshing ledger groups changed and brand-new files, shows added and removed lines, and opens a full diff when you click a file. Image files show a pixel preview. Hover to reveal checkboxes and discard one or several files after confirmation.
- **Spare terminal.** A tabbed shell gives you room for commands without covering the assistant. Open as many shell tabs as you need.

> [!CAUTION]
> Closing the window force-stops everything in that Wisp Deck session. Save your work first.

---

## Project Selector

A new Wisp Deck window opens the selector (glyphs approximated):

```text
  AGENT ‹ Claude Code ›                 Wisp Deck
 ────────────────────────────────────────────────
   Projects    Settings    Stats
 ────────────────────────────────────────────────
  1❯ my-app
     ~/Projects/my-app
   2 another-project
     ~/Projects/another-project
 ────────────────────────────────────────────────
   ⏎ Open    W Worktrees    D Delete
 ────────────────────────────────────────────────
   ↑↓ move · ↵ open · O open once · P plain · L login
```

The header rows are switchers. Use **←/→** to cycle the AI tool and, when configured, the Claude login and plan. **Tab** cycles Projects, Settings, and Stats; **S** and **T** jump directly to the last two tabs.

### Project actions

- **Arrow keys or mouse** — move through the list.
- **Enter or click** — open the selected project.
- **1–9** — open the corresponding project immediately.
- **A** — choose a folder and add it as a project.
- **D** — remove a project or one of its worktrees.
- **W** — expand or collapse a project's worktrees.
- **O** — open a folder once without saving it.
- **P** — open a plain shell without the three-pane workspace.
- **L** — add or switch a Claude login.
- **Shift+↑/↓** — reorder projects.

### Git worktrees

Projects can expand to show their git worktrees. Open a worktree like any other project, create one from the branch picker, or delete one you no longer need. Type **/** in the branch picker to search.

---

## Settings

Press **S** in the selector to open Settings. Most changes apply immediately to open sessions; the default AI tool and a few launch options take effect on the next session or action.

- **Mascot** — show the animated ghost, a static ghost, or no mascot.
- **Tab title** — show the project and tool, the project only, or let the AI tool choose.
- **Theme** — follow the active AI tool automatically or use Orange, Purple, Green, Blue, Rose, or Cyan.
- **Usage bars** — show the Claude 5-hour window, 7-day window, both, or neither.
- **AI tools** — switch between Claude Code, OpenCode, and Codex.
- **Idle sound** — play one built-in macOS sound when the AI finishes and waits for you. Wisp Deck suppresses the agents' native hooks, notifications, and terminal bells so this setting remains the single automatic sound control. It is off by default.
- **Keep awake while working** — prevent the Mac from sleeping while the AI is busy.
- **Account rows** — manage the active Claude login, subscription, and automatic account switching.

---

## Claude Accounts and Subscriptions

### Multiple Claude logins

Keep work and personal Claude accounts separate without repeatedly logging in and out:

- The active account appears at the top of the selector.
- Press **Enter** on the **Account** row to add, rename, remove, or switch logins.
- Click the account pill at the bottom of a session's Changes pane to switch mid-conversation.
- Turn on **Auto-switch accounts** to rotate to the next login when the active account reaches its quota, then continue the conversation where it stopped.

### Subscription profiles

Press **Enter** on **Subscription** or the **PLAN** header to open the subscription manager. It shows Standard Claude and every configured profile in one overlay, including provider, authentication state, endpoint, and all four model routes.

From the manager you can:

- add a profile for a provider, then rename or delete it;
- edit masked API-key authentication when the provider requires it;
- save model mappings with **Save changes**;
- choose what new sessions use with **Use profile**.

On narrow terminals, the overlay switches to a list-and-details view.

### Use a ChatGPT subscription inside Claude Code

Wisp Deck can keep Claude Code as the interface and tool runner while routing models through the ChatGPT subscription managed by Codex:

1. Install or update Wisp Deck with `npx wisp-deck`. Setup can install Codex for you.
2. Open **Settings → Subscription**, press **Enter**, and choose **OpenAI GPT**.
3. Check the connection state: **Signed in**, **Signed out**, still checking, or unavailable.
4. Choose **Sign in / switch account** to open Codex-managed ChatGPT authentication in your browser.
5. Activate OpenAI GPT. If you are signed out, Wisp Deck opens ChatGPT sign-in in your browser automatically and waits; if the browser cannot open, open the fallback URL shown in the subscription modal.
6. Open a new session. After updating Wisp Deck, relaunch existing ledger panes or sessions so they load the new binary and provider environment.

> **No silent API billing:** this path accepts only Codex-managed ChatGPT authentication. It rejects OpenAI Platform API-key login, and the local adapter never reads or stores your ChatGPT token. Codex owns authentication and refresh; Claude Code continues to execute tools and enforce permissions.

Deleting an OpenAI profile does not log Codex out. Profiles contain Wisp Deck's local model-routing settings, while Codex owns the separate cached ChatGPT login. Use **Sign in / switch account** to change that login or run `codex logout` to clear it.

OpenAI GPT requires a compatible Codex version with app-server dynamic-tool support and is not mirrored into OpenCode. If launch reports that Codex is missing, update Wisp Deck and Codex, then relaunch. If Codex is authenticated with an API key, run `codex logout`; the next OpenAI GPT launch will offer ChatGPT sign-in without falling back to metered API usage.

---

## Stats

Press **T** in the selector to view Claude Code, OpenCode, and Codex usage together. The dashboard provides:

- **Monthly usage** — tokens and estimated cost in USD.
- **Model breakdown** — usage grouped by model.
- **Running total** — combined usage across the supported tools.
- **Full and Compact views** — switch detail levels without leaving the dashboard.

Observed usage is stored locally in mirrored append-only journals:

```text
~/.config/wisp-deck/usage-history.jsonl
~/.config/wisp-deck/usage-history.backup.jsonl
```

`usage-cache.json` is only a rebuildable speed-up. Journal history survives transcript pruning, cache resets, upgrades, interrupted writes, and loss or corruption of either journal copy.

> Local history cannot survive disk loss or deliberate deletion of both journal files.

---

## Screenshots, Videos, and Files

Drag a screenshot, video, or other file from Finder or the desktop onto the AI pane. Wisp Deck passes it to the assistant without making you copy the path.

If an image drag does not land where expected, press **`Ctrl+b`**, then **`i`**, to inject your most recent screenshot directly into the AI pane.

---

## Status Line

Claude Code sessions get a compact status line (glyphs approximated):

```text
my-project | 23.5% | 512M | 4% | Opus 4.8 [high] | 5h ▌▌▏ 7d ▌▏▏
```

- **Project** — the active project, with a marker for worktrees.
- **Context %** — how full Claude's context window is.
- **Memory and CPU** — resources used by the session's process tree.
- **Model and effort** — the active model and reasoning level.
- **5h / 7d bars** — quota used by the active login, colored per account and controlled by the Usage bars setting. Account colors appear when two or more Claude logins are configured.

> [!TIP]
> When the context percentage climbs high, start a fresh conversation before you run out of room.

---

## Hotkeys

### Ghostty window

| Shortcut | Action |
|---|---|
| `Cmd+N` | Open a new window and project selector |
| `Cmd+T` | Open a new Wisp Deck tab |
| `Cmd+Shift+Left` | Move to the previous tab |
| `Cmd+Shift+Right` | Move to the next tab |
| `Left Option` | Act as `Alt` instead of typing special characters |

### Inside a Wisp Deck session

Press `Ctrl+b`, then the second key:

| Shortcut | Action |
|---|---|
| `Ctrl+b` then `i` | Drop the latest screenshot into the AI pane |
| `Ctrl+b` then `t` | Open a new spare-terminal tab |
| `Ctrl+b` then `Tab` | Move to the next spare-terminal tab |
| `Ctrl+b` then `Shift+Tab` | Move to the previous spare-terminal tab |
| `Ctrl+b` then `w` | Close the current spare-terminal tab |

---

## Restore Your Sessions

After a reboot, the first Wisp Deck launch restores the projects that were open. Each project returns in its own tab, and supported Claude Code, OpenCode, and Codex conversations resume where they stopped.

Codex tabs restore by exact durable thread ID, so multiple tabs from the same project remain distinct. If an older snapshot has no recoverable Codex ID, Wisp Deck opens Codex's resume selector instead of silently starting an empty conversation.

---

## Update Wisp Deck

Wisp Deck checks quietly for new versions and tells you when one is available. Run the same command to update:

```sh
npx wisp-deck
```

---

## Credits

Made by **Evgeniy Pyatkov** ([@jackuait](https://github.com/JackUait)) — Telegram: [@that_ai_guy](https://t.me/that_ai_guy).

See [CREDITS.md](CREDITS.md).
