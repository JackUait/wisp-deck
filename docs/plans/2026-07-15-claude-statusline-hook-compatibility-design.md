# Claude Status-Line and Hook Compatibility Design

## Status

Approved on 2026-07-15 by the user's instruction to continue with the proposed
status-line repair. This design amends only the Claude hook-shutdown portion of
`2026-07-15-agent-sound-full-control-design.md`.

## Problem

Wisp Deck's per-launch Claude settings overlay forces
`disableAllHooks: true`. Claude Code defines that setting as disabling both
lifecycle hooks and `statusLine` execution, so every Claude session launched
after the change loses Wisp's configured status line and subagent status line.

The base settings, shared-account symlinks, and status-line scripts remain
valid. Older sessions launched without the flag render the line, while every
observed session launched with it does not.

## Approaches considered

### Restore Claude-native customizations

Remove the forced `disableAllHooks` value while retaining
`preferredNotifChannel: notifications_disabled`. Claude again runs the user's
configured status line, subagent line, and lifecycle/plugin hooks. This is the
smallest repair and preserves Claude's customization contract.

Trade-off: Wisp cannot guarantee that an arbitrary user- or plugin-authored
lifecycle hook will never invoke an audio command directly. Such custom hook
behavior is outside Wisp's native-notification ownership boundary.

### Render status outside Claude

Keep all hooks disabled and reproduce the status line in tmux or another Wisp
pane. This preserves the strict hook shutdown but requires a second status
protocol, cannot consume Claude's native status-line JSON directly, and would
lose or duplicate model, context, effort, usage, and subagent behavior.

### Clone and sanitize Claude configuration

Launch Claude with a generated configuration tree that copies every user and
project customization except lifecycle hooks. Plugin-bundled and project-local
hooks make this incomplete without also changing plugin and settings loading;
it is fragile across Claude releases and risks silently dropping unrelated user
configuration.

## Decision

Use the first approach. The private launch overlay continues to suppress
Claude's built-in notification channel but no longer sets `disableAllHooks`.
Wisp's shared terminal filter and attention playback gate continue to own the
native notification paths they control. User-installed hook commands retain
their normal Claude semantics.

The generated overlay preserves a source configuration's existing
`disableAllHooks` value. Wisp must not override an explicit user choice in
either direction: a user who deliberately disables hooks also deliberately
accepts Claude's documented status-line behavior.

## Data flow and error handling

`write_claude_launch_settings` still copies the selected settings source into
an atomic mode-0600 generation-local file. It overrides only
`preferredNotifChannel`, leaving hook and status-line settings untouched. All
existing parse, permission, rename, relaunch, and fail-closed behavior remains
unchanged.

Existing Claude sessions retain their startup settings. Sessions launched with
the broken overlay must be relaunched after installation to load the repaired
overlay and render the status line.

## Verification

The launch-settings tests must prove that:

- an explicit source `disableAllHooks: false` remains false;
- an explicit source `disableAllHooks: true` remains true;
- an overlay without a source contains only the native-notification override;
- hooks, plugins, permissions, unrelated settings, source immutability, atomic
  publication, and mode 0600 remain covered;
- focused Bash-package tests, the authoritative repository suite, local build,
  install path, SHA-256 equality, and code signature pass.
