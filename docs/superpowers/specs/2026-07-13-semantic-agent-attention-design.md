# Semantic Agent Attention Design

**Date:** 2026-07-13

**Status:** Approved

## Goal

Notify the user only when Claude Code, Codex, or OpenCode actually needs the
user: a foreground turn finished without automatic continuation, an unresolved
question or permission is blocking progress, or a terminal error needs action.
Tool calls, retries, subagent completion, transient idle gaps, initial idle, and
unknown state must not notify.

## Decision

Replace marker files, prompt scraping, and time-based debounce with a semantic
per-generation state protocol. Tool adapters publish normalized state. A single
shell consumer owns titles, sound, theme, and keep-awake behavior.

Each tool launch gets a private state file. Its complete record is one line:

```text
1\t<generation>\t<sequence>\t<phase>\t<reason>\n
```

Fields are constrained to:

- `phase`: `ready`, `working`, `attention`, or `unknown`
- `reason`: `-`, `done`, `question`, `permission`, or `error`
- `sequence`: an unsigned counter advanced only for a semantic state change or
  a new attention identity

The publisher atomically renames a temporary sibling over the state file. It
must never create the parent directory. Removing the generation directory
therefore permanently fences a stale writer.

## Session and generation lifecycle

The wrapper creates a mode-0700 session root with `mktemp -d`. Initial launch,
account respawn, tool respawn, and restore each allocate a new random generation
directory and state file.

An atomically replaced descriptor points at the current generation:

```text
1\t<generation>\t<tool>\t<state-file>\n
```

Before a respawn, the controller:

1. allocates and publishes the next generation;
2. updates the tmux tool, generation, descriptor, and state-file environment;
3. builds the command with those values;
4. respawns the tagged AI pane.

Late events can then modify only an obsolete state file. Cleanup terminates
owned helpers and removes the whole session root. Restored sessions always get
a fresh root; attention state is never persisted in the restore snapshot.

## Common consumer

The existing tab-title watcher becomes a protocol consumer. Every tick it
re-reads the descriptor instead of retaining a launch-time tool. It resolves
the unique pane tagged `@gt_ai=1` by stable pane ID, never by geometry or a fixed
index.

The consumer:

- alerts exactly once for a new `(generation, sequence)` whose phase is
  `attention`;
- restores the ordinary title when state returns to `working` or `ready`;
- treats `working` and `unknown` as active for keep-awake;
- never alerts for malformed, missing, stale, or unknown state;
- reads the current tool for sound flags, title text, and automatic theme.

Priority inside an adapter is:

```text
question > permission > error > done > working > ready/unknown
```

Question and permission identities are retained independently so clearing one
request cannot hide another.

## Claude Code adapter

`wisp-deck-tui claude-attention` supervises the complete existing Claude launch
chain. The chain still owns resume fallback, account/proxy environment, settings,
and the screenshot filter.

The adapter polls the exact launch account's `sessions/<pid>.json` registry. A
candidate is accepted only when all of these hold:

- its filename PID equals `.pid`;
- `.kind` is `interactive`;
- the process is a descendant of the supervised launch chain;
- `.procStart` equals the process start time from one UTC `ps` snapshot;
- exactly one valid shallowest candidate exists.

Invalid JSON, schema drift, ambiguity, an absent record, or an unknown status
publishes `unknown`, never inferred attention.

Reduction rules:

- initial `idle` -> `ready`, without notification;
- `busy` -> `working` and arm foreground completion;
- `waiting` -> immediate question or permission attention;
- armed `busy -> idle` -> done attention;
- `waiting -> idle` preserves the unresolved attention;
- an unexpected nonzero supervisor exit -> error attention;
- an externally signalled exit creates no new notification.

Claude Stop and tool hooks are removed as truth sources. Existing Wisp hooks
remain only as migration targets for the removal helper. Claude's notification
channel is overridden through the launch-local settings file, not a globally
leased settings mutation.

Independent Agent View background sessions are not descendants. A separate
account-global broker polls `claude agents --json --all`, keys jobs by config
root and job ID, and emits one global attention event for blocked, completed, or
failed jobs. It never assigns a background job to an arbitrary interactive pane.

## Codex adapter

`wisp-deck-tui codex-adapter` owns:

- one private `codex app-server` Unix socket;
- a passive initialized WebSocket observer;
- the remote Codex TUI in a child PTY;
- an OSC9 parser that observes output while forwarding every byte unchanged.

App-server status provides question, approval, and system-error truth. The
observer is read-only: it never starts, resumes, forks, unsubscribes, answers a
request, or submits a turn. A known restore UUID seeds correlation because a
passive connection does not receive another connection's resume response.

Codex is launched with only `agent-turn-complete` OSC9 notifications enabled.
That notification supplies completion truth because the TUI suppresses it when
a queued follow-up or active goal continues. App-server `idle` alone never
creates done attention.

If private-server setup fails, the adapter runs embedded Codex with OSC
completion tracking and leaves interactive-request state unknown. It never
falls back to prompt scraping. Observer disconnects publish unknown and retry
the read-only connection without restarting the TUI.

## OpenCode adapter

The installed OpenCode plugin becomes event-only and is inert outside Wisp. It
publishes the normalized protocol from:

- `question.asked`, replied, and rejected;
- `permission.asked` and replied;
- `session.status` (`busy`, `retry`, `idle`);
- `session.error`;
- session create, update, and delete events used for parent correlation.

Root busy/retry arms completion. Armed root idle produces done once. Initial
idle and child idle do not notify. Questions and permissions from children do
notify because they block progress. Root error suppresses the following idle
duplicate.

Startup hydrates sessions/status plus pending question and permission endpoints.
A structurally invalid, unauthenticated, partial, or event-raced snapshot is
discarded and leaves state unknown until a clean retry. The plugin contains no
sound, title, spinner, tool-completion timer, or deprecated `session.idle`
handling.

The plugin installer runs idempotently during setup, every successful
`ensure_opencode`, and before an OpenCode launch. Replacement is atomic so tool
switches and updates cannot leave a stale template installed.

## Failure policy

False notification is the more damaging failure. Every adapter therefore
fails silent to `unknown` for missing identity, malformed protocol, stale
generation, unsupported versions, ambiguous roots, or observer loss. Unknown
keeps the machine awake but never rings, changes the tab to attention, or
synthesizes completion.

## Verification

Tests must be written and observed failing before production changes. Coverage
includes:

- protocol parsing, atomic replacement, stale-parent fencing, and sequence
  deduplication;
- generation rotation before both account and tool respawns;
- dynamic watcher tool/pane/state selection;
- Claude registry identity and reducer transitions;
- Codex UDS handshake, passive-method allowlist, reducer, fallback matrix, and
  byte-preserving OSC parsing across every chunk boundary;
- executable OpenCode reducer/hydration tests plus installer coverage;
- cleanup, restore, concurrent sessions, and late old-writer integration tests.

Final verification is shellcheck for every modified shell entry point, focused
runtime probes for all three adapters, the full Go test suite, a fresh
`wisp-deck-tui` build, and a clean pushed branch.

## Authoritative references

- Claude CLI and Agent View: <https://code.claude.com/docs/en/cli-reference>,
  <https://code.claude.com/docs/en/agent-view>
- Claude hooks: <https://code.claude.com/docs/en/hooks>
- Codex app server: <https://learn.chatgpt.com/docs/app-server>
- Codex notification configuration:
  <https://learn.chatgpt.com/docs/config-file/config-advanced#notifications>
- OpenCode plugins: <https://opencode.ai/docs/plugins/>
