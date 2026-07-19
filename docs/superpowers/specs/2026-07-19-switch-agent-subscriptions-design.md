# Subscriptions in the "Switch agent" popup

**Date:** 2026-07-19
**Status:** Design — awaiting review

## Goal

Let the user switch Claude Code's **subscription/backend** (GLM, MiMo, GPT, …)
directly from the **"Switch agent" popup** — the same quick menu that today
switches Claude accounts (Work / Personal) and agents (Claude / Codex). Today,
changing the backend requires opening the main-menu **Subscription** modal;
this surfaces that switch in the fast, per-pane popup.

## Background — two switching axes already exist

The codebase already has everything the *backend* side needs. Two orthogonal
systems switch what a pane runs:

- **Switch agent popup** (`cmd/wisp-deck-tui/claude_account_switch.go`,
  launched by `open_account_switcher` in `lib/account-switch.sh`) — switches
  the Claude **account** (`CLAUDE_CONFIG_DIR`, tracked per-pane in
  `WISP_DECK_CLAUDE_ACCOUNT`) and the **agent** (claude / codex / opencode).
  This is the menu in the screenshot.
- **Subscription modal** (`internal/tui/subscription_modal.go`, main menu) —
  switches Claude's **backend** by pointing the `claude` CLI at a provider's
  Anthropic-compatible endpoint via `claude --settings <file>`
  (`ANTHROPIC_BASE_URL` / `ANTHROPIC_AUTH_TOKEN` / model mappings). Providers
  are catalogued in `internal/claudeconfig/catalog.go`
  (**zhipu/GLM**, **mimo/MiMo**, **openai-chatgpt/GPT**, the last routed through
  the local GPT bridge in `internal/gptbridge`). Config storage:
  `<cfg>/claude-configs/<file>.json`, list `<cfg>/claude-configs.list`
  (`name:file`), **global** active pointer `<cfg>/claude-config`
  (absent/`standard` = plain Claude).

**This feature is wiring the second axis into the first popup — not building
backend switching from scratch.** Add/rename/delete/API-key/sign-in stay in the
main-menu modal (out of scope here).

## Approved UX

Subscriptions nest **under the Claude group, below the account rows**, with a
**single** green active dot spanning accounts and subscriptions:

```
┌──────── Switch agent ─────────┐

   Claude
     Work            ●
     Personal
     GLM
     MiMo
     GPT
   Codex

  ↑↓ move · ↵ switch · esc cancel
└───────────────────────────────┘
```

Semantics (exactly one row is "active" for this pane):

| Pick | Effect |
|------|--------|
| An **account** (Work / Personal / Default) | Relaunch Claude on that login **with the standard Anthropic backend** — i.e. also reset the subscription pointer to `standard`. |
| A **subscription** (GLM / MiMo / GPT) | Relaunch Claude with that subscription's settings file; account (`CLAUDE_CONFIG_DIR`) is left as-is but auth comes from the provider token. |
| An **agent** (Codex) | Unchanged from today — respawn under that agent, touching neither account nor subscription pointer. |

The green dot marks the pane's **current** mode: on an account row when the
pane runs standard Claude, or on a subscription row when the pane runs a
backend. Picking the row that already has the dot is a no-op (cancel-equivalent).

Rationale for "account pick ⇒ standard": a backend subscription overrides the
Anthropic login, so an account choice only means anything under standard
Claude. Collapsing to one active selection keeps the popup a simple "what is
this pane" picker (matches the approved mockup: *"Pick Personal → standard
Claude, Personal"*).

## Design

### 1. Per-pane subscription stamp: `WISP_DECK_CLAUDE_CONFIG`

Accounts already stamp the active dir per-pane in `WISP_DECK_CLAUDE_ACCOUNT`
so the dot reflects *this pane*, not the global pointer (two panes can run
different accounts). Subscriptions currently have **no** per-pane filename
stamp — only the coarse `WISP_DECK_CLAUDE_PROVIDER` marker
(`zhipu|mimo|openai-chatgpt`), which can't distinguish two profiles that share
a provider, and the global `claude-config` pointer, which drifts from a pane
after another pane switches.

Add a parallel per-pane stamp `WISP_DECK_CLAUDE_CONFIG` = the active config
**filename** (empty = standard), set everywhere `WISP_DECK_CLAUDE_ACCOUNT` is:
- `wrapper.sh` at launch (from `resolve_claude_config_path` / the global
  pointer), included in the `new-session -e …` env list.
- `lib/account-switch.sh` relaunch paths (alongside the existing
  `set-environment WISP_DECK_CLAUDE_ACCOUNT`, lines 748 / 1070).

The popup reads this via a new `--active-config` flag to place the dot.

### 2. Go popup — `cmd/wisp-deck-tui/claude_account_switch.go`

- **Row model** `switchRow` (line 45) gains a subscription dimension, e.g.
  `Config string` (the filename) plus a `Ready bool`. A row is exactly one of:
  account (`Dir`), agent (`Tool`), or subscription (`Config`).
- **Tree builder** `switchRowsForSession` (68–94): after the account rows and
  **before** the non-claude agent rows, append one row per line of
  `claude-configs.list`, using the display name. Result order:
  `[Default, …accounts…, …subscriptions…, …agents…]`. Subscriptions render
  indented under the existing non-selectable "Claude" header (same indent as
  accounts, `innerLines` 376–437); agents stay at header level. Cursor
  resolution extends to land on the active subscription when
  `--active-config` names one.
- **Active dot**: `i == m.active` already drives `activeDot`. Compute
  `m.active` from `--active-config` (subscription) falling back to `--active`
  (account) — never both.
- **Glyph / color**: give subscription rows the provider accent already used by
  the subscription modal / `claudeconfig` catalog, mirroring `toolRowGlyph` /
  `toolRowColor` (445–470).
- **Readiness / dimming**: a configured subscription that is **not ready**
  (API-key providers with no stored `ANTHROPIC_AUTH_TOKEN`, via the cheap
  file-based `claudeconfig.ConfigReady`) renders **dimmed and is skipped in
  navigation and clicks**, so we never relaunch into an un-authed backend.
  **ChatGPT is always selectable** — its sign-in is handled at launch by the
  existing GPT bridge / subscription modal auth flow (no slow `codex` probe in
  the popup).
- **Result contract**: `switchResultValue` (99–104) returns `config:<file>`
  for a subscription row (accounts stay bare `<dir>`, agents stay
  `tool:<name>`). `runClaudeAccountSwitch` writes the same to the result file
  and emits `{"selected":true,"config":"<file>","changed":<bool>}`.
- **New flags** in `init()` (588–600): `--configs <listfile>`,
  `--configs-dir <dir>`, `--active-config <file>`. When `--configs` is absent
  or empty the popup is byte-for-byte today's behavior.

### 3. Bash consumer — `lib/account-switch.sh`

- **Context plumbing**: `write_relaunch_context`
  (`account-switch.sh:518`, fed by `wrapper.sh`) records the configs list path,
  configs dir, and the pane's active config filename; `_read_relaunch_ctx`
  loads them into `_rc_*`.
- **Capability probe**: add `switcher_supports_subscription_rows` (grep the
  binary `--help` for `--active-config`), mirroring
  `switcher_supports_session_flags` / `switcher_supports_agent_rows`
  (73–100). Only pass `--configs/--configs-dir/--active-config` when supported
  → older binaries behave exactly as today.
- **Consume the new prefix** in `open_account_switcher` (1200–1230):
  - `config:<file>` → `set_active_claude_config <pointer> <file>`, then relaunch
    the claude pane with the new settings overlay **preserving the draft**
    (reuse `_relaunch_preserving_draft` + the settings staging
    `prepare_claude_relaunch_settings` / `stage_claude_relaunch_settings`,
    554–611, which already read the config pointer). Stamp
    `WISP_DECK_CLAUDE_CONFIG`.
  - **account pick** (existing branch, 1215/1219): before relaunching, also
    `set_active_claude_config <pointer> standard` so an account choice returns
    the pane to the standard backend. No-op when already standard, so existing
    account-switch tests are unaffected.

### 4. Untouched machinery

Base-URL / auth-token / model-mapping generation (`internal/claudeconfig`),
the GPT bridge (`internal/gptbridge`, launched from `lib/tmux-session.sh`
115–184 on `WISP_DECK_CLAUDE_PROVIDER=openai-chatgpt`), and the
connector-suppression overlay (`lib/settings-json.sh`
`write_claude_launch_settings`) all already run off the config pointer /
provider marker. Setting the pointer + stamping the pane is all this feature
adds; the launch path does the rest.

## Backward compatibility

- Old `wisp-deck-tui` binary (no `--active-config`): capability probe fails,
  subscription flags omitted, popup = today.
- No configured subscriptions (`claude-configs.list` empty/absent): no
  subscription rows; popup = today.
- Existing account/agent switching semantics are unchanged except that an
  account pick now also resets the subscription pointer to `standard` (a no-op
  unless a backend was active).

## Error handling

- Missing/short-read result file, cancel (`{"selected":false}`), and
  `changed:false` (picked the already-active row) → no relaunch, as today.
- A `config:<file>` naming a file that no longer resolves
  (`resolve_claude_config_path` empty) → treat as no-op (do not relaunch into a
  broken settings path); the dimming rule already prevents selecting un-ready
  rows.
- Draft preservation on every subscription relaunch is mandatory (same
  guarantee as account switch).

## Testing (TDD — test first, watch fail, then implement)

**Go — `cmd/wisp-deck-tui/claude_account_switch_cmd_test.go`:**
- `switchRowsForSession` appends subscription rows after accounts, before
  agents; correct order and indices with e.g.
  `[Default, Work, Personal, GLM, MiMo, GPT, Codex]`.
- Cursor lands on the active subscription when `--active-config` is set; on the
  active account otherwise.
- `switchResultValue` returns `config:<file>` for a subscription row.
- `selectResultJSON`/result-file variant for `config:<file>` and the
  `{"selected":true,"config":…}` JSON.
- Not-ready subscription is rendered dimmed and skipped by ↑↓ and by clicks;
  ChatGPT stays selectable.
- New flags registered (`--configs`, `--configs-dir`, `--active-config`).
- Render/`innerLines` test: subscription rows indented under the "Claude"
  header with provider glyph; single active dot placement.

**Bash — `test/bash/` (new `account_switch_subscription_test.go`, plus additions
to existing suites):**
- `open_account_switcher` with a scripted popup mock returning `config:<file>`
  sets the config pointer and relaunches preserving the draft; stamps
  `WISP_DECK_CLAUDE_CONFIG`.
- An account pick while a subscription is active resets the pointer to
  `standard`.
- `switcher_supports_subscription_rows` gates the new flags.
- `wrapper.sh` / `write_relaunch_context` plumb the configs list + active config
  into the relaunch context; `wrapper.sh` stamps `WISP_DECK_CLAUDE_CONFIG` at
  launch.
- Regression: existing account-switch and draft-restore tests still pass
  (account pick without any subscription configured is unchanged).

**Quality gates:** `shellcheck` on modified scripts, full `./run-tests.sh`.

## Out of scope

- Managing subscriptions (add / rename / delete / edit API key / ChatGPT
  sign-in) — stays in the main-menu Subscription modal.
- New providers beyond the existing catalog.
- Any change to how a backend is launched once selected.

## Open questions

1. **Readiness dimming** — include the dim-and-skip guard for keyless API-key
   subscriptions in v1 (recommended, cheap, prevents broken panes), or ship
   plain switching first? Design assumes **included**.
2. **Order within the group** — subscriptions listed in `claude-configs.list`
   order (recommended) vs. alphabetical.
