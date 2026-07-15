# Idle Sound Prevention Contract

## Problem

Idle Sound is persisted per AI tool in
`<config>/wisp-deck/<tool>-features.json`. Several independent defects could
make Off audible:

- missing, incomplete, or invalid JSON previously defaulted to Bottle;
- Settings wrote with truncate-in-place, so a reader could see torn JSON;
- changing the selected AI tool left Settings bound to the old tool's file;
- a preference write could complete after playback read the old value but
  before that audio finished;
- foreground attention and Claude background-job notifications used separate
  readers;
- a plain terminal could manually start OpenCode with an old installed plugin.

Current main already makes the OpenCode plugin event-only, installs it
atomically, repairs it during every setup regardless of the selected tool, and
gates initial and mid-session OpenCode launches. This change preserves that
architecture and closes the remaining preference and plain-shell boundaries.

## Contract

1. Audio requires a regular, bounded file containing strict JSON with exactly
   one lowercase `sound` key whose value is `true`. Missing, invalid, oversized,
   duplicate-key, non-standard, case-variant, or non-regular input is silent.
2. Go notification paths read through `internal/soundpref`. New Go playback
   paths must use the same package instead of decoding feature JSON directly.
3. A feature-file transaction uses the sibling lock
   `.<tool>-features.json.lock`.
4. Writers hold the lock through an atomic sibling-temp rename. Playback holds
   the lock while it re-reads the preference and through the lifetime of
   `afplay`.
5. Therefore, after a successful Off action returns, no audio authorized by
   the old value is still running and no later notification can start without
   observing a later successful opt-in.
6. All production mutations of the selected AI tool go through
   `setSelectedAI`, which rebinds and reloads the matching feature file.
7. A failed Settings write rolls the displayed value back and reports the
   failure. The UI must never claim Off when the persisted value remains On.
8. Settings preview is a direct user action and remains separate from runtime
   notification playback. Off itself never starts a preview.
9. Any Wisp Deck surface that exposes a possible OpenCode process start must
   repair its plugin first. This includes the plain terminal because the user
   can run `opencode` manually there.
10. Agent adapters publish attention state only. Claude's native notification
    channel is disabled, Codex external notifier commands are disabled and its
    OSC 9 terminates inside the private PTY relay, and OpenCode's plugin has no
    notification effect.
11. No agent-generated BEL, OSC notification, or direct audio path configured
    by Wisp Deck may reach the outer terminal. Runtime sound starts only in
    Wisp Deck after the shared live preference gate authorizes it.

## Runtime paths

- `lib/notification-setup.sh`: foreground semantic-attention sound; macOS
  `lockf` plus a live preference read.
- `cmd/wisp-deck-tui/claude_background.go`: background-agent notification;
  `soundpref.WithExclusiveLock` plus a live preference read.
- `internal/tui/mainmenu.go`: atomic Settings writer and explicit preview.
- `internal/soundpref`: canonical Go reader, allowed system-sound names, lock
  path, and exclusive transaction helper.
- `lib/settings-json.sh`: launch-local Claude overlay with
  `preferredNotifChannel=notifications_disabled`.
- `internal/codexadapter/osc9_filter.go`: bounded private-protocol filter that
  reports Codex completion events without forwarding OSC 9 to the terminal.
- `internal/codexadapter/supervisor.go`: exact `notify=[]` launch override that
  disables Codex's independent external notifier command.
- `templates/opencode-plugin.ts`: event-only attention-state publisher with no
  child process, audio, system notification, or terminal-control output.

macOS `lockf` and Go's `flock` share the advisory lock namespace used here. The
regression suite exercises interoperability in both directions with real
processes rather than mocks.

## Prevention tests

- fail-closed cases cover missing files, absent/case-variant/duplicate keys,
  invalid/trailing/non-standard JSON, oversized and FIFO inputs, explicit Off,
  unsafe names, and explicit On;
- concurrent readers require every Settings write to remain valid JSON;
- foreground and background playback tests block inside fake `afplay` and prove
  an Off writer cannot cross the active sound;
- a persistence-failure test requires UI rollback;
- a repository guard rejects new runtime audio sites outside the three audited
  files, requires both notification owners to use the shared live gate, forces
  Claude's native channel off, and rejects raw Codex OSC forwarding;
- Codex filter and real-PTY tests cover plain and tmux-wrapped OSC 9 at every
  split, byte-identical unrelated output, bounded oversized input, and semantic
  completion without terminal notification bytes;
- Codex exact-argv tests require `notify=[]` for every launch form;
- the OpenCode executable contract rejects playback, system-notification, and
  terminal-control effects;
- a source invariant permits only the sound-aware selected-tool setter;
- wrapper tests require plugin repair before plain-shell exec, initial OpenCode
  launch, attention generation creation, and mid-session respawn.

OpenCode loads plugins at process startup. A process that loaded legacy
JavaScript before upgrading cannot be changed in place and needs a one-time
restart; every process boundary controlled by the upgraded release is gated.
