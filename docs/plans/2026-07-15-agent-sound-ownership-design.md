# Agent Sound Ownership Design

## Goal

Every native notification path that Wisp Deck enables for Claude, Codex, or
OpenCode must terminate inside Wisp Deck. Agents may publish semantic attention
events, but no agent-generated audio trigger may reach Ghostty. Wisp Deck is the
only runtime owner of sound playback and continues to honor each tool's Idle
Sound preference through the existing locked live gate.

This contract covers the built-in notification paths Wisp Deck configures. It
does not rewrite unrelated user hooks or plugins and does not attempt to stop an
agent from running an audio command explicitly requested by the user.

## Existing gap

The playback calls themselves are already centralized: foreground attention,
Claude background jobs, and Settings preview are the only audited `afplay`
sites. OpenCode's installed plugin only publishes state.

Two upstream triggers can still escape the ownership boundary:

- Claude is launched with `preferredNotifChannel=terminal_bell`, so Claude
  writes BEL to the session terminal. Ghostty currently defaults to a visual,
  no-audio bell, but a terminal configuration can make that agent-generated BEL
  audible.
- Codex is launched with an exact OSC 9 completion override. Its private PTY
  relay parses the OSC 9 frame for semantic attention and then forwards the
  original frame to the outer terminal, where Ghostty can independently turn it
  into a notification and sound.

## Ownership boundary

### Claude

The launch-local settings overlay forces
`preferredNotifChannel=notifications_disabled`. Wisp Deck does not need
Claude's notification channel for attention: the Claude supervisor already
derives semantic state from Claude's process registry. The user's settings file
remains untouched, and the existing migration for historical global
`terminal_bell` leases remains unchanged.

### Codex

Codex keeps the exact `agent-turn-complete` / `osc9` launch overrides because
the OSC event is a useful completion discriminator when app-server observation
is incomplete. The difference is placement: OSC 9 is a private protocol between
the Codex child PTY and Wisp Deck, not terminal output.

A bounded streaming filter sits in the PTY output loop. It:

- emits every ordinary byte unchanged and in order;
- recognizes plain OSC 9 terminated by BEL or ST;
- recognizes tmux passthrough-wrapped OSC 9;
- reports complete valid frames to the existing reducer;
- suppresses confirmed OSC 9 frames before writing to the outer terminal;
- handles arbitrary read boundaries, including one byte per read;
- bounds candidate payload memory at the existing 64 KiB limit and discards an
  oversized notification through its terminator;
- flushes an incomplete non-notification prefix at EOF while dropping an
  incomplete confirmed notification.

Other terminal controls, including titles, colors, hyperlinks, non-OSC-9
controls, and malformed non-notification prefixes, remain byte-identical.

### OpenCode

The installed plugin remains event-only. Its executable contract continues to
reject child-process imports, `afplay`, `osascript`, terminal control output,
and other notification effects. No new OpenCode launch configuration is needed.

## Playback flow

For all three tools, the resulting flow is:

1. The adapter publishes a normalized attention state file.
2. `lib/tab-title-watcher.sh` observes a new attention sequence.
3. `lib/notification-setup.sh` takes the per-tool preference lock and re-reads
   the live feature document.
4. Only Wisp Deck invokes `afplay`, and only for an explicit valid opt-in.

Claude background-job notifications retain their separate Wisp Deck notifier,
which uses the same sound preference package and lock.

## Failure behavior

- Failure to create Claude's disabling overlay remains launch-fatal rather than
  falling back to an agent-controlled notification channel.
- A malformed or oversized Codex OSC 9 frame never becomes a completion event
  and never reaches the terminal once its OSC 9 prefix is confirmed.
- Failure to write filtered Codex output follows the existing PTY teardown path.
- Sound preference read, lock, or playback failures remain fail-silent.

## Verification

Regression coverage proves:

- Claude overlays preserve unrelated settings and force only
  `notifications_disabled`;
- Codex plain and tmux-wrapped OSC 9 frames are recognized but absent from PTY
  output across every split, while surrounding ordinary bytes are exact;
- the real Codex PTY relay consumes a fragmented notification and still emits
  its completion event;
- OpenCode's executable plugin has no playback or terminal-notification effect;
- a repository-wide source guard rejects new agent-owned audio triggers and new
  playback sites outside Wisp Deck's audited owners;
- the focused suites, full repository test suite, build, local install,
  installed checksum, command path, and code signature all pass.
