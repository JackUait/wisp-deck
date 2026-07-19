# Unified Subscription + Login Modal

Date: 2026-07-19
Goal: merge the subscription and account (Claude login) interfaces into one
shared interface — everything reachable from the subscription menu.

## Problem

The splash Settings tab has two separate management UIs:

- **Settings › Subscription** — `internal/tui/subscription_modal.go`, a two-pane
  modal (profiles list / details) managing subscription profiles.
- **Settings › Account** — `internal/tui/claude_account_menu.go`, an inline
  panel managing Claude logins (switch / add / rename / delete).

The mid-session tmux popup (`claude-account-switch`) already merges both row
kinds; the splash should too.

## Design

The subscription modal becomes the single shared interface.

### Profiles pane (left) — two sections, one scrolled list

```
SUBSCRIPTIONS ────────
● Standard Claude   Ready
  My GLM           Needs key
  + Add profile

LOGINS ───────────────
● Default           default
  Work              work-1
  + Add login
```

Row model (cursor space, in order): subscription profiles `0..N-1`,
"+ Add profile" at `N`, logins `N+1..N+M`, "+ Add login" at `N+1+M`.
Subscription "Active" (`claude-config` pointer) and login "Active"
(`selectedAccount`) are orthogonal; each section shows its own ● marker.
Login rows show their config dir where subscriptions show Ready/Needs key,
and wear their persistent account color.

### Details pane (right)

- Login row: LOGIN header, config dir, active state, actions
  `[ Use ] [ Rename ] [ Delete ]` (Delete only for managed logins; Default is
  implicit and only supports Use + Rename of its label).
- "+ Add login" row: ADD LOGIN blurb + `[ Create login ]`.
- Subscription rows/add row: unchanged.

### Behavior

- `u`/`[ Use ]` on a login row switches the active login
  (`selectedAccount` + `persistClaudeAccount`), modal stays open.
- Rename/Delete/Add reuse the existing lifecycle modes
  (`subscriptionRename`, `subscriptionDeleteConfirm`, a new login-name input
  mode), branching by row kind; data ops go through `internal/claudeaccount`
  (`Add`, `Rename`, `Remove`, `SetDefaultLabel`).
- Mouse: profile-pane hit-test covers login rows and both add rows; detail
  buttons include `[ Use ]`.

### Entry points

- **Settings › Subscription** (Enter) — opens the unified modal (unchanged).
- **Settings › Account** (Enter), the **`l`** shortcut and the main-page
  `FocusAccount` Enter — open the same modal focused on the logins section
  (`l` lands on "+ Add login" and opens the label input directly).
  ←→ quick-cycling of the login on the settings row stays.
- The inline account panel (`claude_account_menu.go` render/update paths and
  `accountMenuOpen` state) is removed.

## Out of scope

The mid-session tmux popup, bash orchestration (`lib/account-switch.sh`), and
the ledger pill are untouched — they already share one popup.

## Testing

Go unit tests in `internal/tui` (TDD): row-model ordering, section rendering,
login switch/rename/delete/add flows, mouse hit-tests, retargeted entry
points; existing account-panel tests are rewritten against the modal.
