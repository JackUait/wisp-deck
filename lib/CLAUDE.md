# lib — gotchas

### The launch critical path (NEVER block it)

Everything `wrapper.sh` runs **before `select_project_interactive`** is the
launch critical path: the user is staring at the loading splash for exactly
that long. Budget is **~130ms**. The splash is there to make the wait pleasant,
**not** to give you room to do work behind it.

**The rule: no blocking subprocess before the picker.** No `npx`, `npm`, `node`,
`curl`, `brew` — nothing that touches the network or boots a language runtime.

This has already shipped once. `resolve_opencode_cmd` ran
`npx --no-install opencode-ai --version` to decide *which* npx string could
launch OpenCode — eagerly, every launch, for every tool. It cost **3s warm,
6-13s under load**, and every Claude user paid it in full to compute a string
that was then thrown away. It hid behind the splash for releases.

When you need an expensive answer on this path, do one of:

1. **Answer it without the subprocess.** Usually the expensive call is deciding
   more than you need. `opencode_available()` (`lib/ai-tools.sh`) is the
   pattern: a PATH check answers "can we run it?" for free, and the probe that
   answers "*how* do we run it?" is deferred to the one branch that launches it.
2. **Move it after the picker**, to the branch that actually needs it.
3. **Background and disown it**, like `check_for_update` — spawning is fine,
   *blocking* is the defect.

Guarded by `test/bash/launch_critical_path_test.go`, which mocks every expensive
command to sleep 20s and fails if the picker takes over 5s. It tests the
property, not the instance, so it catches whatever the next offender turns out
to be — and it names the command in the failure. `opencode_availability_test.go`
adds a static guard against reintroducing that specific probe.

### The post-pick path and the tmux launch chain (same discipline)

The stretch from project selection to `attach-session` is critical path too:
the splash is stopped and the panes don't exist yet, so every synchronous
spawn here is dead screen time. Three launch invariants, each with a guard:

1. **No blocking subprocess between pick and tmux.** One python3 spawn (the
   Claude launch-settings overlay) is the allowed exception; migrations must
   gate themselves behind a cheap bash check (see
   `remove_waiting_indicator_hooks`'s grep fast path), and capability probes
   must cache by binary signature (see `gt_claude_filter_prefix`,
   `gt_ledger_native_capable`). Guarded by
   `test/bash/launch_post_pick_path_test.go` (property test, names the
   offender).
2. **No foreground `run-shell` inside the launch chain.** tmux runs the
   chain's commands in order; a foreground `run-shell` holds the splits and
   the attach hostage for as long as its script runs (the ledger-hover
   install once blocked on a server-wide 15s lock). Use `run-shell -b` —
   formats like `#{pane_id}` still expand at the command's position in the
   chain. Guarded by `test/bash/ledger_hover_nonblocking_test.go`.
3. **A tab must never end up attached to a broken layout.** tmux executes the
   rest of the chain even when a `split-window` fails (this shipped: a
   pre-resize tiny pty made both splits fail and the tab sat on a lone
   full-width ledger forever). The session size comes from `_sane_term_size`
   (never raw `_detect_term_size` — guarded by
   `test/bash/sane_term_size_test.go`), and `gt_ensure_panes_watch`
   (backgrounded around the launch) rebuilds whatever panes are missing once
   the window has real space — guarded by `test/bash/pane_heal_test.go`.

### A large tab chip renders state the agent owns, so the bar sanitizes it

The tab bar has two chip modes (`tab_bar` in the settings file). `compact` is
the original numbered chip; `large` — the **default** — adds the tab's own title
and the elapsed time of the turn running in it. Absent key, absent file and
unknown value all resolve to large, in `tab_view_mode` AND in the Go menu's
`SetTabBar`: disagree and a session opens on a bar the Settings row denies is
selected.

A chip's title is the agent pane's `pane_title`, which is where Claude stamps
its summary of the current turn — so the bar renders a string the model wrote.

- **tmux parses `#[...]` out of the EXPANDED format**, so a turn titled
  `#[fg=red]` repaints the rest of the bar in a colour of the model's choosing.
  Verified live on 3.6a. `#{q:...}` does **not** escape it (only
  `#{s/#/…/:...}` does), so `tab_view_chip_title` deletes every `#` in bash,
  where it is testable. A comma is harmless — a value substituted into
  `#{W:...}` is not re-parsed for the iterator's separator — but a style comma
  written literally in the format still needs `#,`.
- **The ellipsis is what separates a running turn from a finished one.** Claude
  prints `✽ Hyperspacing… (8m 25s · ↓ 16.9k tokens)` while working and replaces
  it in place with `✻ Cooked for 1h 38m 25s` when the turn ends. Matching the
  summary makes a tab claim to be working forever.
- **The inventory is unit-separated (`\037`), never tab-separated.** A tab is an
  IFS *whitespace* character, so bash collapses runs of them: a window with two
  empty fields in a row shifts every later field left, and the pane title lands
  in the wrong variable. This shipped in the first draft of the stamping pass.
- **Only write a value that moved.** `set-option` repaints the client, and this
  pass runs twice a second in every open session on the machine.
- **Both status-left writes take the same mode.** The launch chain writes the
  bar once when it builds the session and again when it realigns to the agent
  pane; disagreeing makes the bar change shape a moment after the tab opens.
- **A card's fill breaks wherever `#[default]` appears.** A large chip is a
  filled card — a rounded cap at each end and one background carried from the
  number through the title to the progress — and `#[default]` restores the
  bar's base style, so reaching for it anywhere between the caps punches a hole
  in that card's own background. The first draft did exactly that after the
  number badge. Every segment between the caps, *including both branches of the
  progress conditional*, carries `bg=` explicitly, and the only `#[default]` in
  a chip sits immediately after the closing cap — which is what
  `TestTabViewStatusLeft_a_cards_fill_is_unbroken` asserts by counting.
- **The caps are octal escapes, not literal glyphs.** They are powerline
  codepoints (U+E0B6/U+E0B4) in the Private Use Area: Ghostty draws them itself
  so no Nerd Font is involved, and tmux accounts each as one cell (probed
  against a live client with `#{pN:}` padding before the format was built). They
  are written `$'\356\202\266'` because `/bin/bash` here is 3.2 — no
  `$'\u'` — and because a literal PUA byte does not reliably survive editing.
- **Card chrome is part of what a chip spends.** `tab_view_title_budget`
  reserves the caps and padding (18 per card, 22 fixed for the label and the
  [+] card). Widening the chrome without widening the reserve does not shrink
  titles — it pushes the right-hand tabs past the window edge, where tmux
  clips them out of existence.

Per-window title and progress are state that becomes valid *later* — a window is
created before its agent has named anything — so, exactly like the ledger's
account pill in `internal/tui/CLAUDE.md`, they are re-resolved on every watcher tick rather than
loaded once. Guarded by `test/bash/tab_view_large_test.go` (the pure
title/progress/budget functions against verbatim captures from live panes, the
stamping pass against a spy tmux, the watcher wiring and both launch modes) and
`internal/tui/tab_bar_setting_test.go`.

### The switcher card belongs to the ledger, and a popup's `-y` is its bottom edge

The Switch card is the account pill's menu — the pill in the ledger's footer is
the only thing that opens it — so `open_account_switcher` centers it in the
**ledger pane's** rectangle. It has been centered on the agent pane and on the
whole window before; both float it out in the transcript, detached from the
affordance the user just clicked. Fallbacks are a ledger too narrow for the card
or no ledger pane at all → the window's center (tmux **clips** a popup at the
pane edge, it never shrinks one, so a clipped card is worse than a detached one),
then measure-unavailable → the full-window dimmed-backdrop popup.

Placing it means converting between three coordinate systems that all look alike:

- **`-y` is the popup's BOTTOM boundary; `-x` is its LEFT edge.** The man page
  gives it away by defining position `P` as "the bottom **left** of the pane".
  Handing tmux the wanted top row hoists the card a full card-height too high,
  and tmux then **clamps** it flat against the top of the terminal — so the
  mistake reads as "the popup ignores its position", not as a near miss. Pass
  `top + height`.
- **A popup is positioned in CLIENT rows; panes live in WINDOW rows.** A top
  status line offsets the two by its height (`#{status}` lines when
  `#{status-position}` is `top`), and a popup at `-y 0` paints over that status
  line. Columns need no correction — a status line steals rows, not columns.
- **`_session_side_panes` prints `"<ledger> <spare>"`, so `read -r a b` is a
  trap.** With no ledger the leading field is EMPTY and `read` collapses it,
  handing the spare pane's id over as the ledger and centering the card on the
  wrong pane. Split by prefix (`${side_panes%% *}`).

Guarded by `TestOpenAccountSwitcher_centers_the_card_in_the_ledger_pane`,
`_offsets_the_card_past_a_top_status_line`,
`_falls_back_to_the_window_when_the_card_outgrows_the_ledger`,
`_centers_the_card_in_the_window` (the no-ledger case, which is also the
`read`-collapse regression guard) and `_window_fallback_when_measure_fails`.
To re-check the anchoring against a real tmux, view an attached client through an
outer tmux pane — `capture-pane` on the outer pane renders the inner client's
screen *including* the popup, which is otherwise uncapturable.

### A tab in trouble says so, and says how bad it is

The tab title is the session's status at a glance, and it used to have only one
thing it could report about a turn that ended: 🔔. A turn that **died** rendered exactly
the same bell as one that finished, so the tab strip could not distinguish work
completed from work lost. `reason` already travelled the protocol — the tick
simply rendered the phase alone.

Two cues, split by how certain the trouble is:

- **❌ — a confirmed failure**: `phase=attention, reason=error`. All three
  adapters already produce it (Claude's error status, Codex's system-error,
  OpenCode's error).
- **⚠️ — a possible one**: the session reported fine and then stopped reporting.

**Certainty picks the cue, not severity alone.** Sustained silence has a
documented benign cause — Claude serializing on macOS Security XPC publishes
nothing for *minutes* under concurrency while working perfectly — so the
uncertain signal gets the softer glyph. Putting ❌ on it would cry wolf on
healthy sessions and teach the user to ignore the one cue that means something
died.

- **The error cue is checked BEFORE the seen swap, and survives it.** A bell
  decays to 👀 once the user looks, because looking is what a bell asks for.
  Looking at a failure does not fix it, so ❌ is not swapped out — otherwise a
  glance at the tab strip erases the only record that the turn died.
- **A warning needs a valid read first, and a sustained silence after it.**
  `_ATTENTION_WATCH_EVER_VALID` gates it: a session that has NEVER reported is a
  launch in progress, not a fault, and warning through every cold start would
  make the cue meaningless. `_ATTENTION_WATCH_QUIET_LIMIT` (default 60 ticks,
  ~30s at the 0.5s interval) is the sustain — one missed read is routine.
- **The warning never covers a cue the phase earned.** A tab waiting on the user
  has something specific to report; the silence is only a guess.
- **It is a live reading, not a latch.** Any valid snapshot resets the counter,
  so a session that starts reporting again clears the cue without a relaunch.
- **There are TWO rendering sites.** `apply_tab_title` returns early in `model`
  mode — the agent named the tab itself — so the per-tick model re-emit renders
  the cue separately. Both `case` statements default to the plain title, so
  missing one fails *soft*: model-title users would simply never see a cue, and
  a manual check of the other mode looks green.

Guarded by `test/bash/tab_title_trouble_test.go` — including
`_a_failed_turn_shows_a_cross_focused_or_not` (the seen-swap precedence),
`_model_title_mode_carries_the_trouble_cue` (the second site),
`_never_warns_before_the_session_has_ever_reported` and
`_a_session_that_reports_again_clears_the_warning`.

### A tab follows its agent's working directory, not the worktrees it creates

An agent that enters a git worktree used to leave the rest of the tab behind:
the ledger kept diffing the checkout nobody was working in, the spare terminal
sat in it, and a crash-restore reopened it. The tab now follows.

**The signal is the agent's cwd, never `git worktree list`.** A bare
`git worktree add` does not move the agent — following it would detach the
ledger from where the agent actually works — and subagent worktrees and this
repo's own test suite create worktrees constantly, so a list-poll would yank the
ledger into a temp checkout mid-test-run. Claude's account-local registry record
carries `cwd`, and it moves in BOTH directions (`EnterWorktree` and
`ExitWorktree`), so one field drives the follow and the snap back out. This is
Claude-only by construction, not by omission: no other agent moves its own
working directory.

- **No second discovery path.** `ClaudeRegistryMapper` already finds and
  validates that record every poll (live PID, `procStart`, launch-tree scope),
  so it reports the cwd and `claude-attention` publishes it to
  `<generation>/cwd`. It is a **sidecar, not a sixth field** of the attention
  state: that record is a fixed five-field versioned protocol every consumer
  pins, and a location is not a semantic phase. Like `SessionID`, the cwd always
  comes from the interactive record — a parked session's job record speaks for
  the status only.
- **The agent pane is never respawned.** `_apply_worktree_switch` relaunches it
  fresh, which is right for the switcher's own row (the user chose it) and
  destructive here: the conversation running in that pane is the one that just
  created the worktree. Its non-agent half is `_retarget_session_context` +
  `_retarget_session_side_panes`, and the follow calls only those.
- **`ExitWorktree` removes the worktree as it leaves,** so the tab is asked to
  follow home FROM a checkout that no longer exists — and `git worktree list`
  from a deleted directory reports nothing, refusing the snap-back and stranding
  the tab on a dead path forever. A dead anchor re-roots at the closest surviving
  ancestor; a Claude worktree lives at `<main>/.claude/worktrees/<name>`, so that
  reaches the repository that owned it while any other repository still fails.
- **The steady state does not fork.** Both watcher reads are builtins, and only a
  directory differing from the session's own reaches the shell that validates it.
  Each distinct directory is attempted **once** — a refused one (the agent cd'd
  out of the project) would otherwise spawn a shell twice a second — and
  convergence clears the memo, so re-entering a worktree still follows.
- **That shell is a fresh `bash`.** The wrapper may run under `bash --posix`
  (Ghostty's `/bin/sh -c` launch), where the process substitution
  `_session_worktrees` reads git through is disabled.
- **A session that exits while still inside a worktree cannot snap back** — the
  agent is gone — so the restore snapshot records the worktree path, and a
  removed one restores nothing.

Guarded by `test/bash/worktree_follow_test.go` (including
`_never_respawns_the_agent_pane`, `_follows_home_after_the_worktree_is_removed`
and `_never_respawns_the_spare_as_a_ledger` — the last a `read -r ledger spare`
field-collapse this extraction fixed), `test/bash/worktree_follow_watcher_test.go`
(`TestAttentionWatcherTick_follows_the_agent_into_a_worktree` is what pins the
tick to the follow at all), `test/bash/worktree_follow_wiring_test.go`, and
`TestClaudeRegistryMapperReportsTheSessionsWorkingDirectory` plus
`TestWorkingDirectoryWriterPublishesBesideTheAttentionState`.

### A tmux call with no `-t` lands on the tab the user last typed in

A tmux command with no target resolves to the "current" session. Inside a pane
that is the pane's own session, because tmux reads `$TMUX_PANE`. Everywhere
else it is the session with the newest activity: the tab the user last typed
in. Measured on 3.6a, and it matches `cmd-find.c`. The session number inside
`$TMUX` is ignored.

"Everywhere else" is most of wisp-deck:

- the wrapper and its background watchers, which run outside tmux;
- `run-shell`, `run-shell -b` and hooks, whose child gets `$TMUX` but NOT
  `$TMUX_PANE`, even when fired by a key in the right pane;
- a second client batch before its `attach-session`. `new-session` makes its
  own chain safe; the later batch is a new client.

This shipped as "tabs shuffle their git tree and terminal". An agent in tab A
ran `EnterWorktree`. A's watcher followed it, but the retarget helpers asked
tmux for "this" session untargeted. They rebuilt the ledger and spare of tab B
(on screen at the time) inside A's worktree, rewrote B's spare config, and
stamped B's `WISP_DECK_PATH`, so a crash-restore would reopen B there too. The
same shape could respawn another tab's AGENT pane: the quota auto-switch runs
its relaunch under `run-shell -b`.

Rules:

- **Code that runs outside a pane names its session:** `-t "=$session"` (the
  `=` makes the match exact; a bare name also matches a prefix). A verb that
  takes a pane or window target needs `=$session:`: there `=name` resolves to
  nothing, and a bare `0` means pane 0 of the CURRENT window. Or hand the
  pane down as `TMUX_PANE=<id>`, as `auto_switch_maybe_trigger` does. The
  relaunch helpers were written for the switcher, which runs in a pane, so the
  pane is how they find their tab.
- **No refusal is safer than a wrong target.** `follow_agent_checkout` refuses
  a session tmux does not have. The auto-switch refuses without a pane.
- **Key bindings are server-wide.** Every launch rewrites them, so a value baked
  in at bind time belongs to the tab launched LAST. A per-tab value comes from
  `#{q:session_name}` / `#{q:window_id}`, which expand at key time.
  `prefix+t/w/Tab/BTab` once baked `$_spare_label`: in every tab they drove the
  newest tab's terminal, and `prefix+w` closed its tabs.
- **The same holds one level down.** A tab-view window adds a sibling session
  to the SAME inner spare server. `spare_tabs_outer_key` finds the right one
  from the window's spare-pane tty.
- **Buffers are server-wide too.** Draft replay uses one buffer per pane
  (`wispdraft<pane>`, dropped on paste). Tabs on one login cross the
  auto-switch threshold together.

Mock tests could not catch this: the logging mock answers `list-panes` the
same for every target. Guarded against a real, private tmux server
(`TMUX_TMPDIR` of its own) holding two tabs, with the OTHER tab current, in
`test/bash/cross_session_isolation_test.go`: the follow, the auto-switch run,
the spare keys (fired through wrapper.sh's own bind string). Statically,
`TestWrapperBinds_bake_no_session_value`, the second launch batch, and
`TestRunShellEntryPoints_name_their_tab`, which fails any `run-shell` in
wrapper.sh or lib that neither expands `#{q:session_name}`, carries this
session's name, nor hands down `TMUX_PANE=`. Such a
test must drop `TMUX`/`TMUX_PANE` from its env: inherited from a live pane,
they would point it at the user's real server.

### A here-document is a pipe under bash 5.3, and a pipe holds 512 bytes

Bash 5.3 writes a here-document — and a here-string, which is one — into a
**pipe** rather than a temp file whenever it is under a hardcoded 64KB, and it
writes the whole body *before* the reader starts. So the body has to fit the
pipe, and a pipe is only as big as the kernel granted it. On a Mac running a
deck of long-lived stands it grants the **512-byte minimum**: ~22,000 open pipe
fds exhaust the pipe budget and capacity never grows from there. Measured
2026-09-05: 500B fine, 512B hangs forever, 70KB fine again (the temp-file
fallback). `/bin/bash` (3.2) and zsh are unaffected, and TMPDIR is not involved.

Nothing about this looks like a bug in the script. The shell blocks in
`heredoc_write` with no error, no timeout and no output, so the caller simply
waits. It shipped as **"the model switcher does nothing"**:
`_current_session_identities` reads `tmux show-environment` (PATH alone runs
past 512 bytes) through `done <<< "$session_env"`, so the first click on the
account pill deadlocked its bash — and because `LedgerModel.openAccountSwitch`
guards on `switchingAccount` until `OpenSwitcher` returns, *every later click
was a silent no-op* for the life of the pane. The same deadlock sat on the
launch path in `get_loading_art` (704B of ASCII art), so a new tab hung on the
splash.

- **Never feed a here-document anything that can outgrow 512 bytes.** Fixed
  text goes through `printf '%s\n' 'line' 'line'`; an embedded script goes
  through `python3 -c "$script"` (`-c` puts `sys.argv[1:]` exactly where
  `python3 -` did); a variable read line by line goes through
  `< <(printf '%s\n' "$var")`.
- **Process substitution is NOT available everywhere.** tmux runs `run-shell`
  under `/bin/sh`, which is bash 3.2 in POSIX mode, where `< <(...)` is a
  **syntax error** — the file fails to parse and the click dies before dispatch.
  Ten lib files parse there today and must keep parsing there; in those, split
  on newline instead (`IFS=$'\n'; set -f; for line in $var`), which also keeps
  the loop in the current shell where a pipe would subshell it.
  `TestShellCodeThatParsesUnderBinSh_keepsParsingThere` compares each file
  against its own HEAD version, so it fails only on a file that *lost* the
  property.
- **A here-string carrying small, bounded data is left alone** — a terminal
  size, an 8-entry palette, one window's pane list. Rewriting those in the
  POSIX-safe form buys nothing and costs legibility.
- **Only the literal body is decidable**, so
  `TestShippedShellCode_hasNoHereDocumentThePipeCannotHold` caps that at 400
  bytes; a here-string's size is whatever the variable holds at runtime, which
  is why the deadlock is also pinned behaviorally by
  `TestCurrentSessionIdentities_survives_an_environment_past_the_pipe_buffer`.

Reducing the number of open stands restores normal pipe sizes and hides all of
this again — which is exactly why it presents as intermittent.

### Restore-queue pops are authorized, never ambient

An interactive launch may consume a restore-queue entry only through
`restore_pop_authorized`: it built the queue, it holds the one-shot chain
ticket its spawner issued via `restore_advance`, or it launched inside the
crash-storm grace window of the queue build. Popping without authorization is
the wrong-tab bug: a user's fresh Cmd+T tab silently restores another
project while their intended session opens elsewhere. Never add a
`restore_queue_pop` call site that skips the gate. Guarded by
`test/bash/restore_chain_ticket_test.go`.

Every Wisp session that can run Codex owns a private durable identity path at
`session-identities/<session>.codex`, stamped as
`WISP_DECK_CODEX_SESSION_FILE`. The semantic adapter must persist its exact
current root UUID there, including later `/new` transitions; snapshots,
restore queues, and tool switches must prefer that sidecar over stale tmux
stamps and cwd/rollout guessing. Observer loss before the first identity and
identity-write failure are fatal rather than silently leaving a live,
unrestoreable chat. A restored Codex tab may launch an exact
`codex resume <uuid>` or the interactive `codex resume` selector, but it must
never fall back to plain Codex: a plain launch silently replaces the lost
conversation with an empty one. Guarded by
`test/bash/codex_crash_restore_test.go` and the Codex supervisor tests.
