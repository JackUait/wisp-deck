# Draft preservation across mid-session account switch

**Date:** 2026-07-06
**Status:** Designed; every primitive verified empirically on Claude Code 2.1.201

## Problem

The mid-session account switch (`lib/account-switch.sh`) relaunches the AI pane with
`tmux respawn-pane -k`, killing the running `claude` process. Anything the user had
typed-but-not-submitted in Claude Code's input field — draft text, pasted images
(`[Image #N]`), pasted long text (`[Pasted text #N]`) — lives in the dying process's
memory and is lost. The goal: after a switch, the draft is back in the input field,
images included.

## Facts established (each verified live on Claude Code 2.1.201)

1. **Claude Code does not persist unsent drafts.** Nothing on exit or crash;
   `--resume`/`-c` never restores the input field; no flag/env/setting changes this.
2. **Double-Esc stashes the draft text to disk.** `Esc Esc` with a non-empty input
   clears it AND appends it to `~/.claude/history.jsonl` as
   `{"display": "<full draft text incl. placeholders>", "pastedContents": {}, ...}`.
   Multi-line drafts keep their newlines in `display` (verified). With an EMPTY
   input, nothing is appended (`Esc Esc` opens the rewind menu instead — harmless
   in a pane that is about to be killed). `history.jsonl` is already symlink-shared
   across all managed accounts (`WISP_DECK_CLAUDE_SHARED_STATE_ITEMS`), so the
   entry is readable no matter which login writes it.
3. **`Up`-recall does NOT revive images.** In a new claude process, `Up` restores
   the draft text, but `[Image #N]` placeholders come back as dead text — the model
   receives no image (verified in-account and cross-account; an early "success"
   turned out to be the model guessing the color, exposed by control runs and by
   the transcript containing no image block).
4. **Pasted images are already on disk at paste time**, at
   `$CLAUDE_CONFIG_DIR/image-cache/<sessionId>/<N>.png`, written the moment the
   user pastes. `N` matches the `[Image #N]` label even when text pastes share the
   counter (verified: after `[Pasted text #3]`, the next image was `[Image #4]`
   and its file `4.png`).
5. **Bracketed-pasting an image file's PATH into the input creates a LIVE
   attachment.** `tmux load-buffer` + `paste-buffer -p` with an absolute `.png`
   path renders as an `[Image #N]` chip, and the model genuinely sees the image
   (verified with a solid-purple test PNG answered "Purple"). This is the same
   mechanism the existing screenshot-drop filter relies on. It works regardless of
   which account claude runs — the path is just read from disk — and the pasted
   image is re-cached under the NEW session's `image-cache/<sid>/N.png`, so
   chained switches keep working.
6. **Long text pastes are NOT recoverable.** `[Pasted text #N +M lines]` content
   lives only in process memory (no `paste-cache` file is written at paste time);
   after the kill the bytes are gone. Only the marker text can be restored.

## Approaches considered

### A. Esc-Esc stash + path-paste replay (RECOMMENDED)

Extract the draft by sending `Esc Esc` (claude itself writes the full text to the
shared `history.jsonl`), then after the relaunch reconstruct the input:
bracketed-paste the text segments and, for each `[Image #N]` marker, bracketed-paste
the absolute path `<old cache root>/image-cache/<old sid>/<N>.png`, which claude
turns back into a live image chip.

- **Pros:** full-fidelity text (multi-line included) and images (actual bytes, not
  a screen scrape); every primitive uses observed, stable claude behavior (the
  drop-a-path mechanism is already load-bearing for wisp-deck's screenshot
  feature); all changes confined to bash (`lib/account-switch.sh`); no changes to
  the shared-state list; fail-open at every step.
- **Cons:** depends on two claude behaviors that could change in a future release
  (Esc-Esc-saves-to-history, path-paste-attaches); needs a readiness poll before
  replaying; `[Pasted text #N]` comes back as a dead marker (claude-side
  limitation, see fact 6).

### B. Esc-Esc stash + `Up` recall (rejected)

One keypress to restore, no parsing. Rejected: `Up` restores text only — images
come back as dead placeholders in a new process, even with the caches shared
across accounts (both verified). Kept as the implicit manual fallback: whenever
the automated replay fails, the stashed draft is still one `Up` away.

### C. PTY-filter shadow buffer (rejected)

The AI pane already runs behind `wisp-deck-tui screenshot-filter`, a PTY proxy
that sees every stdin byte, so it could mirror the input buffer and replay it.
Rejected: mirroring claude's line editing (cursor motion, history recall, kill
ring) is unbounded emulation work, and the filter never sees image bytes anyway —
a paste is one Ctrl+V byte on stdin; claude reads the clipboard itself.

### D. capture-pane scrape + retype (rejected)

Only captures the visible slice of a long draft, loses images, and parses a TUI
frame that changes across claude releases.

### E. No-kill switch via the rotation proxy (out of scope)

In proxy mode the account changes at the HTTP layer and claude never restarts, so
the input field trivially survives. But the switch pill is explicitly disabled in
proxy mode (wrapper.sh only writes the relaunch context with the proxy OFF), and
folding account switching into the proxy is an architecture change. Noted as the
long-term direction that dissolves this problem entirely.

## Design (approach A)

All changes live in `lib/account-switch.sh`; the Go popup is untouched.

### 1. Stash: extract the draft before the kill

In `open_account_switcher`, after the user picked a different login and before
`relaunch_ai_pane`:

```
stash_ai_draft <tmux_cmd> <pane>   # prints the stashed draft text; rc 0 iff stashed
  record history.jsonl line count
  send-keys Escape                 # interrupts a running turn if busy; no-op if idle
  ~200ms pause
  send-keys Escape Escape          # the stash pair
  poll up to ~1.5s for history.jsonl to grow
  on growth: print the `display` field of the new last line
```

The line-count delta is the "was there a draft?" signal — an empty input appends
nothing, so the poll times out and no restore is attempted. The stray keys are
harmless in any claude UI state because the pane is respawn-killed moments later.

### 2. Map: resolve image markers to files

Split `display` on `[Image #N]` markers. Each marker resolves to
`<cache_root>/image-cache/<sid>/<N>.png`, where:

- `sid` is the pane's conversation id already stamped in the tmux session env
  (`WISP_DECK_CLAUDE_SESSION`, maintained by the statusline — the same id the
  relaunch resumes);
- `cache_root` is the OLD account's config root (the account the pane ran before
  the switch, from `current_session_account`): `~/.claude` for Default, else the
  account's dir. The new login only needs to READ that path; no cache sharing
  required.

A marker whose file is missing (cache pruned, unstamped session) is replayed as
its literal text — degraded but never blocking. `[Pasted text #N]` markers are
always replayed as literal text (fact 6).

### 3. Replay: reconstruct the input after the relaunch

`relaunch_ai_pane` gains a restore step, run only when the stash succeeded, as a
`disown`ed background waiter so the ledger's click handler never blocks:

1. Poll `tmux capture-pane` (bounded, ~30s) until the relaunched claude shows its
   ready input line — matching the ready frame specifically, not just any `❯`,
   because on a trust/login/update screen stray keys would drive that dialog.
   On timeout, give up silently: the draft still sits in prompt history (`Up`).
2. Replay segments in order via a tmux buffer: `load-buffer` + `paste-buffer -p`
   (bracketed paste) for BOTH text segments and image paths — bracketed paste
   keeps embedded newlines from submitting and turns each path into an image
   chip. Small settle delay between segments so chips render in order.

Nothing is ever auto-submitted; the reconstructed draft just sits in the input.

### 4. Intentionally not restored

- Long-paste contents (`[Pasted text #N]`) — memory-only in claude, gone at kill.
- Cursor position (replay leaves the cursor at the end).
- Clipboard state (the replay never touches the system clipboard).

## Error handling

Every step is best-effort and fail-open: failed stash, unreadable history, missing
cache file, or a never-ready relaunch each degrade to today's behavior (draft lost
or draft-in-history) without blocking or corrupting the relaunch. Both polls have
hard timeouts. The waiter must guard against the pane having been re-respawned by
a second switch (compare pane id + a generation stamp before pasting).

## Testing

TDD in `test/bash/` with the existing mock-tmux harness:

1. `stash_ai_draft`: success iff history grows within the timeout; prints the new
   entry's `display`; sends interrupt-Esc before the stash pair; empty-input path
   returns failure and prints nothing.
2. Marker mapping: `[Image #N]` → `<root>/image-cache/<sid>/N.png` for Default and
   managed roots; missing file → literal-text segment; `[Pasted text #N]` →
   literal; interleaved counters (`[Image #4]` after `[Pasted text #3]`) map to
   `4.png`.
3. Replay waiter: pastes only after the mock capture shows a ready frame; never
   after timeout; segments delivered in order; text goes through bracketed paste.
4. `open_account_switcher` ordering: stash → relaunch → replay only on a changed
   selection; cancel sends no keys.
5. Manual e2e (documented in the PR): type multi-line text + paste two images →
   switch account via the pill → draft returns with live image chips under the
   new login; submit and confirm the model sees the images.

## Field findings from implementation (2026-07-06)

Two facts the live e2e added to the design, both implemented:

- **The empty prompt is `❯` + U+00A0** (no-break space), not an ASCII space —
  the ready-poll matches both.
- **The stash read races other live sessions**: history.jsonl is global, so
  another session's prompt can be appended between our Esc-Esc and the tail
  read. The stash only accepts appended entries whose `project` field matches
  this pane's project dir; growth made solely of foreign entries counts as
  "no draft".

Verified end-to-end on 2026-07-06 (Claude Code 2.1.201): draft + pasted image
under the Default login → `_relaunch_preserving_draft` → pane resumed the same
conversation under the managed login with the draft restored and the image
re-attached (`⎿ [Image #N]` attachment indicator on submit).

## Regression guards (2026-07-06)

Two guards keep the draft-loss bug from ever returning:

- `TestDraftRestore_endToEnd_realTmux_draft_survives_switch` (always on) —
  drives the REAL click flow through a real tmux server: stash keys, respawn,
  ready-gate, disowned waiter, bracketed-paste replay with the image path.
  Sensitivity proven: it fails when the wiring is reverted to the old bare
  `relaunch_ai_pane`.
- `TestLiveClaude_draft_assumptions` (gated) — pins the three real-claude
  behaviors the feature rests on (Esc-Esc history stash, NBSP-padded empty
  prompt via the production `wait_ai_pane_ready`, path-paste → image chip).
  **Run it whenever the claude binary is upgraded:**
  `WISP_DECK_LIVE_CLAUDE_E2E=1 go test ./test/bash/ -run TestLiveClaude -v`
  (needs a logged-in claude; appends one throwaway entry to history.jsonl).

## Open risks

- Esc-Esc-stashes-draft and path-paste-attaches are observed behaviors, not
  contracts; a claude release could change either. Both degrade to
  draft-in-history (manual `Up`), and the e2e regression test will catch a break.
- If the statusline has not yet stamped `WISP_DECK_CLAUDE_SESSION` (very young
  session), image markers cannot be mapped; replay falls back to literal marker
  text. Acceptable: the same condition already forces the switch down its
  fresh-launch (no-resume) path.
