# Fast Account and Subscription Switching Design

## Goal

Make the native ledger's account/subscription chooser appear immediately and remove every avoidable process from the path between clicking an identity pill and confirming a target.

## Current Bottleneck

The native Go ledger delegated a pill click to Bash. Before showing the chooser, that path:

1. started a new Bash process and sourced the switch libraries;
2. executed `wisp-deck-tui claude-account-switch --help` three times to probe flags;
3. captured every tmux pane to synthesize a backdrop;
4. opened a tmux popup; and
5. started a second Go TUI.

Local measurement put the three duplicate capability probes alone at roughly 325 ms per switch, with shell startup, pane capture, and popup startup on top.

## Architecture

The already-running native Go ledger owns selection UI. Its asynchronous session loader parses accounts, subscriptions, disabled state, readiness, identity colors, available tools, and the current session-scoped identity into immutable `SwitchOption` rows. Clicking the pill only flips in-memory Bubble Tea state, so the chooser paints on the next frame without filesystem I/O or a subprocess.

After confirmation, the ledger closes the chooser and starts one asynchronous Bash command with an exact `SwitchChoice { Kind, Value }`. Bash uses `apply_account_switch_choice` to enter the established account, subscription, or tool relaunch path. Draft preservation, exact session resume, attention-generation fencing, shared-state synchronization, backend settings, and per-session tmux stamps remain unchanged.

The standalone tmux popup remains the shell-ledger and version-skew fallback. Its three feature checks share one cached help probe.

## Data Flow

1. `SessionSource.Load` reads the relaunch context and session-scoped tmux stamps.
2. It constructs fully resolved switch rows off the Bubble Tea loop.
3. A pill click opens the in-process overlay and selects the active row.
4. Arrow keys, mouse motion, and clicks only mutate in-memory cursor state.
5. Enter/click on any ready row yields an exact typed choice for post-click revalidation.
6. `ExecAccountSwitcher` asynchronously invokes the fixed Bash adapter with argv-safe values.
7. Bash applies the choice and reuses the existing relaunch implementation.
8. Completion reloads the session context, repository snapshot, and backdrop.

## Correctness and Error Handling

- Account, subscription, and tool values are passed as argv, never interpolated into shell code.
- Invalid choice kinds are rejected at the Go adapter boundary and again in Bash.
- Missing managed account directories fail rather than silently selecting Default.
- Every confirmed row is rechecked by the asynchronous adapter, so a stale `Active` marker cannot discard an exact choice; a truly active choice remains a no-op at the relaunch layer.
- Disabled subscriptions remain hidden unless active; unready subscriptions remain visible but unselectable.
- Confirmation revalidates subscription membership, disabled/readiness state, managed-account list membership and directories, tool availability, and required executables before mutating pointers or respawning. Account and subscription switches require Claude; ChatGPT subscriptions also require Codex.
- Cancellation starts no process and mutates no persistent state.
- Errors return through the existing ledger action-error channel.
- The fallback popup retains legacy binary compatibility.

## Performance Contract

- Pill click to chooser paint performs no external process call.
- Cursor movement and rendering perform no account/config filesystem access.
- Only confirmation starts the relaunch adapter; stale active markers are checked after confirmation and genuinely unchanged choices no-op there.
- Post-selection identity lookup uses one combined tmux environment query, including legacy sessions whose stamps are absent.
- Subscription changes update both relaunch settings keys in one file rewrite.
- Standalone fallback capability detection executes the Go binary at most once per shell process and parses its help without forking `grep`.

## Testing

TDD regression coverage verifies:

- switch options are precomputed with identity, readiness, color, and active state;
- clicking paints the chooser without invoking the switch adapter;
- keyboard and mouse navigation remain in-process;
- keyboard and mouse confirmation apply the exact choice asynchronously even when the preloaded active marker is stale;
- stale account, subscription, and tool choices are rejected before mutation, including removed account-list entries and missing Claude/Codex executables;
- account, subscription, and tool choices bypass the popup and relaunch correctly;
- stamped and legacy sessions resolve account/config identity with one tmux query;
- the fallback's capability checks share one help probe;
- existing draft/session/backend/fallback regressions remain green.
