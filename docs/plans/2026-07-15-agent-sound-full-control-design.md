# Agent Sound Full-Control Design

## Status

Approved on 2026-07-15. This design supersedes the narrower notification-path
boundary in `2026-07-15-agent-sound-ownership-design.md`.

## Goal and boundary

Wisp Deck is the only component allowed to cause automatic attention audio in
every agent process it launches, restores, resumes, or switches to. Claude,
Codex, and OpenCode may publish semantic state to Wisp Deck, but their native
notification systems, lifecycle hooks, external plugins, and terminal output
must not independently cause sound.

The boundary includes foreground sessions, resumed sessions, mid-session tool
or account switches, and Claude background agents created from a controlled
foreground session. It includes terminal controls that Ghostty could translate
into sound, even when the user's current Ghostty configuration is silent.

An audio command explicitly requested and approved by the user remains a normal
agent tool action. Preventing every possible user-authorized child process from
using CoreAudio would require process-wide OS isolation, would break legitimate
audio work, and is outside this notification-ownership contract. Plain and
spare shells become user-controlled after Wisp hands them to the user; manually
bypassing Wisp's agent launchers from those shells is likewise not a
Wisp-managed agent launch.

## Audited escape classes

The complete audit found these automatic sound routes:

1. Claude's native `preferredNotifChannel`, including BEL and terminal-specific
   desktop notifications.
2. Claude lifecycle hooks from user, project, local, plugin, or inherited
   settings. Any hook event can execute a sound command, not only
   `Notification` or `Stop`.
3. Codex's top-level external `notify` command.
4. Codex TUI notifications (`auto`, `bel`, or `osc9`).
5. Codex lifecycle hooks, including plugin-bundled hooks.
6. OpenCode's native TUI `attention` system, which can play built-in or custom
   sound files and independently emit desktop notifications.
7. OpenCode npm, global-file, project-file, and custom-directory plugins. The
   local audit found a legacy Wisp/Ghost Tab `ghost-tab.ts` that directly calls
   `afplay` alongside the newer event-only plugin.
8. Standalone BEL and Ghostty OSC 9 notification controls emitted by an agent,
   hook, plugin, or rendered model/tool output, including tmux passthrough
   wrappers and arbitrary PTY read boundaries.
9. Initial launches, resume fallback chains, account/tool relaunches, session
   restore, and background-agent inheritance that could omit a control applied
   only to one happy-path argv.

Ghostty command-finish notifications are terminal-owned shell integration, not
agent notification output. Wisp does not change the user's global Ghostty
configuration. The agent PTY filter prevents an agent from manufacturing the
BEL or OSC 9 inputs Ghostty treats as agent notifications.

## Architecture

### Claude: launch-local notification and hook shutdown

Each Claude attention generation receives an atomic private settings overlay.
The overlay preserves unrelated selected Wisp settings but forces:

```json
{
  "preferredNotifChannel": "notifications_disabled",
  "disableAllHooks": true
}
```

The setting is applied to every fresh and fallback resume invocation. The
Claude process-registry observer remains the semantic state source, so Wisp
does not depend on lifecycle hooks. Background agents spawned by that
controlled Claude process inherit the controlled session configuration.

If the private overlay cannot be created or validated, Wisp fails the launch
instead of running Claude with agent-owned notifications. User settings are
never mutated.

### Codex: disable both command notifiers and hooks

The Codex TUI and app-server receive exact command-line overrides for:

```toml
notify = []
features.hooks = false
```

The TUI continues to emit only `agent-turn-complete` as OSC 9 because Wisp uses
that event as a private completion discriminator. The existing Codex PTY
adapter consumes it before terminal output. Every fresh/resume and
embedded/remote form uses the same exact configuration list.

### OpenCode: pure server, native event observer, silent TUI

OpenCode no longer depends on an auto-loaded filesystem plugin. Wisp starts an
authenticated loopback `opencode serve --pure` process and attaches a
`opencode --pure` TUI to it. `--pure` prevents npm, global-file, project-file,
and custom-directory external plugins from executing inside the Wisp-managed
server or client.

Wisp subscribes directly to the server's documented SSE event stream and feeds
the existing OpenCode reducer with session, question, permission, and error
events. Server and TUI lifetimes are supervised as one generation; the server
uses a random per-generation password and loopback-only address. A bounded
parser, response-size limits, timeouts, exact session identity, and generation
fencing protect the observer boundary.

The TUI receives an atomic private `OPENCODE_TUI_CONFIG` whose `attention`
object forces `enabled`, `notifications`, and `sound` to `false`. This is
defense in depth even though the semantic observer does not need native TUI
attention.

The installer retires only positively identified legacy Wisp/Ghost Tab plugin
files. Unknown user files are not deleted; `--pure` makes them inert in
Wisp-managed launches. The obsolete Wisp OpenCode plugin is removed only after
the native observer owns all launch paths.

If the pure server, authenticated observer, silent TUI config, or attach path
cannot be established, Wisp fails the OpenCode launch before rotating away a
working session.

### Shared terminal egress filter

A reusable bounded streaming filter sits immediately between every agent PTY
and tmux. It suppresses:

- standalone BEL;
- plain Ghostty OSC 9 notifications terminated by BEL or ST;
- tmux passthrough-wrapped OSC 9 notifications.

It preserves ordinary bytes and unrelated terminal controls byte-for-byte,
including BEL used only as the terminator of a non-notification OSC sequence.
It handles every read split, malformed and oversized input, nested tmux escape
doubling, and EOF without unbounded buffering.

Codex reuses this filter while retaining semantic delivery of its private OSC
9 completion. Claude's screenshot PTY proxy and the new OpenCode adapter apply
the same output policy without treating filtered notification text as semantic
state.

## Sound data flow

The only automatic audio flow is:

1. A controlled adapter observes semantic state without an audio side effect.
2. It publishes a generation-fenced attention state record.
3. Wisp's title watcher observes a new attention sequence.
4. `notification-setup.sh` takes the per-tool lock and re-reads the live strict
   sound preference.
5. Only Wisp invokes `afplay` for an explicit valid opt-in.

Claude background-job notifications remain Wisp-owned and use the same Go
sound-preference package and advisory lock. Settings preview remains an
explicit user action rather than automatic attention audio.

## Verification contract

Completion requires evidence for every escape class:

- exact Claude overlays force both native-notification and hook shutdown while
  preserving unrelated settings and source files;
- every Claude launch/relaunch/resume path uses the controlled overlay;
- exact Codex TUI and app-server argv disable `notify` and hooks;
- OpenCode server and attach argv use `--pure`, authenticated loopback, and the
  silent private TUI config;
- an executable OpenCode integration consumes representative SSE events and
  publishes attention without loading any filesystem plugin;
- known legacy Wisp/Ghost Tab audio plugins are safely retired, while unknown
  user files are preserved and inert under `--pure`;
- the shared egress filter covers standalone BEL, plain/tmux OSC 9, every split,
  malformed/oversized recovery, EOF, and exact preservation of all unrelated
  controls;
- real PTY integrations for Claude, Codex, and OpenCode prove notification
  bytes never reach their outer terminal output;
- source invariants reject new agent-owned playback, hook/plugin enablement,
  raw notification forwarding, and uncontrolled launch paths;
- focused tests, race tests, the authoritative repository suite, build, local
  install, installed path, SHA-256 equality, and code signature all pass.

A running ledger pane or Wisp session must be relaunched after installation so
its agent process and PTY boundary use the new controls.
