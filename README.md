# Wisp Deck

Launch a ready-to-go AI coding session in one command. Wisp Deck opens a three-pane terminal workspace — your AI assistant, a live view of your git changes, and a spare terminal — and cleans everything up the moment you close the window.

<p>
  <img src="docs/screenshot-selector.png" width="49%" />
  <img src="docs/screenshot-session.png" width="49%" />
</p>

---

## Quick Start

```sh
npx wisp-deck
```

That's it. The only requirements are **macOS** and **Node.js 16+**. Your AI tool and the supporting pieces (tmux, jq, the Wisp Deck TUI) are installed for you automatically the first time you run it. The one manual step is the terminal itself: if Ghostty isn't installed yet, Wisp Deck opens its download page and waits while you install it.

During setup you'll pick your AI assistant; you add projects afterwards, from the selector (press **A**). After that, opening a session is a couple of keystrokes.

---

## What You Get

Pick a project and Wisp Deck drops you into a three-pane workspace:

- **AI assistant** — Claude Code, OpenCode or Codex, focused and ready. Just start typing your prompt.
- **Changes view** — a live, auto-refreshing ledger of your working-tree changes: added/removed lines per file, with brand-new files in their own group. Click any file to open its full diff in a popup — image files show an actual pixel preview. Hover reveals checkboxes so you can discard one file or several at once (with a yes/no confirmation).
- **Spare terminal** — a tabbed shell for running commands, with its own tab bar so you can open as many as you need.

Close the window and Wisp Deck shuts down every process it started — no leftover AI processes quietly running in the background.

> [!CAUTION]
> Closing the window force-stops everything in the session. Save your work first.

---

## Using the Project Selector

Open a new window and you're greeted by the selector (glyphs approximated):

```
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

The header rows are switchers — use **←/→** to cycle the AI tool (and, if you have them, the Claude login and plan). Below that, **Projects / Settings / Stats** are tabs (**Tab** cycles them; **S** and **T** jump straight there), and the action bar shows what applies to the selected row.

- **Arrow keys or mouse** to move, **Enter** or **click** to open
- **Number keys (1–9)** open that project immediately
- **A** — add a project (with path autocomplete as you type)
- **D** — remove a project or one of its worktrees
- **W** — expand or collapse a project's worktrees
- **O** — open a folder once without saving it to your list
- **P** — open a plain shell with no panes, just a terminal
- **L** — add or switch a Claude login
- **Shift+↑/↓** — reorder projects in the list

### Git worktrees

Projects can expand to show their git worktrees. From the selector you can open a worktree like any project, create a new one from a branch picker (type `/` to search branches), or delete worktrees you're done with.

---

## Settings

Press **S** in the selector to open Settings. Most changes apply immediately to open sessions; a few (like the default AI tool) take effect on the next session or action.

- **Mascot** — show the animated ghost, a static one, or none.
- **Tab title** — what the window tab shows: project and tool, project only, or let the AI tool set it.
- **Theme** — Auto (matches your AI tool's colors) or a preset accent: Orange, Purple, Green, Blue, Rose, Cyan.
- **Usage bars** — which Claude quota bars the status line shows: the 5-hour window, the 7-day window, both, or none.
- **AI tools** — switch between Claude Code, OpenCode and Codex.
- **Idle sound** — play a chime when the AI finishes and is waiting on you. Off by default; choose from the built-in macOS sounds.
- **Keep awake while working** — stop the Mac from sleeping while the AI is busy.
- **Account** rows — the active Claude login, subscription, and auto-switch (see below).

---

## Claude Accounts & Plans

If you use Claude Code, Settings also lets you manage **multiple logins** — keep separate work and personal accounts and switch between them without logging in and out each time. The active account is shown at the top of the menu; from the **Account** row you can add, rename, remove, and switch logins. Inside a session, click the account pill at the bottom of the Changes pane to switch mid-conversation.

Turn on **Auto-switch accounts** and Wisp Deck rotates to your next login automatically when the active one runs out of quota, continuing the conversation where it left off.

The **Subscription** row lets you keep several Claude configurations and switch the active one per session.

---

## Stats

Press **T** in the selector for a usage dashboard covering **Claude Code, OpenCode and Codex** together. It breaks your usage down by month — tokens used, a per-model breakdown, and estimated cost in USD — with a running total across everything, and a Full/Compact view toggle. Handy for keeping an eye on what you're spending.

Observed usage is kept locally in mirrored append-only journals at `~/.config/wisp-deck/usage-history.jsonl` and `usage-history.backup.jsonl`; `usage-cache.json` is only a rebuildable speed-up. Journal history survives transcript pruning, cache resets, upgrades, interrupted writes, and loss or corruption of either journal copy. Like any local-only data, it cannot survive loss of the disk or deliberate deletion of both journals.

---

## Dropping Screenshots & Videos into the AI

Drag a screenshot, video, or any other file from Finder or your desktop onto the AI pane and Wisp Deck hands it straight to your assistant — no copying paths by hand.

If a drag doesn't land where you expect, press **`Ctrl+b` then `i`** inside the session to inject your most recent screenshot (images only) directly into the AI pane.

---

## Status Line

For Claude Code, Wisp Deck sets up a compact status line so you always know where you stand (glyphs approximated):

```
my-project | 23.5% | 512M | 4% | Opus 4.8 [high] | 5h ◼◼◻ 7d ◼◻◻
```

- **Project** you're in (worktrees get their own marker)
- **Context %** — how full Claude's context window is
- **Memory** and **CPU** used by the session's process tree
- **Model** and its reasoning effort
- **5h / 7d usage bars** — how much of the active login's quota is used, colored per account (shown with two or more Claude logins, per the Usage bars setting)

> [!TIP]
> Watch the context percentage — when it climbs high, it's a good time to start a fresh conversation.

---

## Hotkeys

**In the terminal window:**

| Shortcut | Action |
|---|---|
| `Cmd+N` | New window (opens the selector) |
| `Cmd+T` | New tab |
| `Cmd+Shift+Left` | Previous tab |
| `Cmd+Shift+Right` | Next tab |
| `Left Option` | Acts as `Alt` instead of typing special characters |

**Inside a session** (press `Ctrl+b`, then the key):

| Shortcut | Action |
|---|---|
| `Ctrl+b` then `i` | Drop your latest screenshot into the AI pane |
| `Ctrl+b` then `t` | New tab in the spare terminal |
| `Ctrl+b` then `Tab` | Next spare-terminal tab |
| `Ctrl+b` then `Shift+Tab` | Previous spare-terminal tab |
| `Ctrl+b` then `w` | Close the current spare-terminal tab |

---

## Picking Up Where You Left Off

After a reboot, the first launch automatically brings back the projects you had open before — each one reopens in its own tab and the AI conversation resumes where it left off (Claude Code, OpenCode and Codex alike). A restart doesn't cost you your workspace.

---

## Staying Up to Date

Wisp Deck quietly checks for new versions and lets you know when one is available. To update, just run it again:

```sh
npx wisp-deck
```

---

## Credits

Made by **Evgeniy Pyatkov** ([@jackuait](https://github.com/JackUait)) — Telegram: [@that_ai_guy](https://t.me/that_ai_guy).

See [CREDITS.md](CREDITS.md).
