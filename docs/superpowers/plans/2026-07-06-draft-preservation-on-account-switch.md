# Draft Preservation on Account Switch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** After a mid-session Claude account switch, the unsent input-field draft — text and pasted images — is back in the relaunched claude's input field.

**Architecture:** Four new bash functions in `lib/account-switch.sh`, wired into `open_account_switcher`. Before the respawn, `stash_ai_draft` makes claude persist the draft (Esc Esc → shared `history.jsonl`) and extracts its text. After the respawn, a disowned background waiter polls for the ready prompt and `replay_ai_draft` reconstructs the input via tmux bracketed pastes, substituting each `[Image #N]` marker with the cached PNG's absolute path (`<old account root>/image-cache/<sid>/<N>.png`), which claude re-attaches as a live image. Spec: `docs/superpowers/specs/2026-07-06-draft-preservation-on-account-switch-design.md`.

**Tech Stack:** bash (lib/account-switch.sh), python3 (JSON tail extraction — already a lib/ dependency), Go test harness in `test/bash/` (mock tmux via `mockCommand`).

## Global Constraints

- TDD: write each failing test, run it, watch it fail, then implement (repo IRON RULE).
- `shellcheck lib/account-switch.sh` clean after every task that touches it.
- Functions are called from the compact-view ledger which runs under **zsh**: no unquoted filename globs (parameter-expansion patterns are fine), no bash-only syntax that zsh lacks (`for ((;;))` and `[[ ]]` are fine in zsh).
- Everything fail-open: any failure must leave the switch behaving exactly as today (no draft restored, relaunch unaffected).
- Nothing is ever auto-submitted: no `Enter`/`send-keys ... Enter` anywhere in the replay path; text goes through bracketed paste (`paste-buffer -p`) so embedded newlines cannot submit.
- Work directly on `main`; push after the final task.
- All new tests go in `test/bash/draft_restore_test.go`; reuse `accountSwitchSnippet` from `test/bash/account_switch_test.go` (same package `bash_test`).

---

### Task 1: `stash_ai_draft` — extract the draft via Esc-Esc

**Files:**
- Modify: `lib/account-switch.sh` (add function after `current_ai_session`, ~line 133)
- Test: `test/bash/draft_restore_test.go` (create)

**Interfaces:**
- Produces: `stash_ai_draft <tmux_cmd> <pane> <history_file>` — sends the stash keys, waits (≤1.5s) for `<history_file>` to grow, prints the new entry's `display` text (no trailing newline), exit 0 iff a draft was stashed. Exit 1 (prints nothing) on: no growth (empty input), send-keys failure, missing python3.

- [ ] **Step 1: Write the failing tests**

Create `test/bash/draft_restore_test.go`:

```go
package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stash_ai_draft extracts the pane's unsent draft by making claude itself
// persist it: Esc Esc appends the draft to the shared prompt history
// (verified behavior, see the 2026-07-06 spec). The mock tmux appends a
// history entry when it sees the Escape pair, standing in for claude.
func TestStashAIDraft_prints_display_when_history_grows(t *testing.T) {
	dir := t.TempDir()
	hist := writeTempFile(t, dir, "history.jsonl",
		`{"display":"old entry","pastedContents":{}}`+"\n")
	rec := filepath.Join(dir, "tmux.log")
	// Log every call; on the Escape-pair send-keys, append the stashed draft
	// (with an escaped newline, to prove multi-line JSON decoding).
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
printf '%%s\n' "$*" >> %q
if [ "$1" = "send-keys" ] && [ "$4" = "Escape" ] && [ "$5" = "Escape" ]; then
  printf '%%s\n' '{"display":"line one\nline two [Image #2]","pastedContents":{}}' >> %q
fi`, rec, hist))
	env := buildEnv(t, []string{bin})
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("stash_ai_draft tmux %%1 %q", hist)), env)
	assertExitCode(t, code, 0)
	if out != "line one\nline two [Image #2]" {
		t.Fatalf("expected decoded display text, got %q", out)
	}
	logOut, _ := os.ReadFile(rec)
	// The lone interrupt-Esc (stops a streaming turn) precedes the stash pair.
	first := strings.Index(string(logOut), "send-keys -t %1 Escape\n")
	pair := strings.Index(string(logOut), "send-keys -t %1 Escape Escape")
	if first == -1 || pair == -1 || first > pair {
		t.Fatalf("expected lone Escape then Escape pair, log:\n%s", logOut)
	}
}

// An empty input appends nothing (Esc Esc opens the rewind menu instead), so
// no growth within the timeout means "no draft": rc 1, empty output, and the
// switch proceeds exactly as today.
func TestStashAIDraft_fails_when_history_does_not_grow(t *testing.T) {
	dir := t.TempDir()
	hist := writeTempFile(t, dir, "history.jsonl", "")
	bin := mockCommand(t, dir, "tmux", `exit 0`)
	env := buildEnv(t, []string{bin})
	start := time.Now()
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("stash_ai_draft tmux %%1 %q", hist)), env)
	if code == 0 {
		t.Fatalf("expected nonzero exit when history did not grow, got 0: %s", out)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected no output, got %q", out)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("poll did not time out promptly: %v", elapsed)
	}
}

// A missing history file (fresh install) must behave like the empty-input
// case, not crash.
func TestStashAIDraft_missing_history_file_is_no_draft(t *testing.T) {
	dir := t.TempDir()
	bin := mockCommand(t, dir, "tmux", `exit 0`)
	env := buildEnv(t, []string{bin})
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("stash_ai_draft tmux %%1 %q", filepath.Join(dir, "absent.jsonl"))), env)
	if code == 0 {
		t.Fatal("expected nonzero exit for missing history file")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./test/bash/... -run TestStashAIDraft -v`
Expected: FAIL — `stash_ai_draft: command not found` in the snippet output.

- [ ] **Step 3: Implement `stash_ai_draft`**

Add to `lib/account-switch.sh` after `current_ai_session` (before `build_switch_launch_cmd`):

```bash
# stash_ai_draft <tmux_cmd> <pane> <history_file> — extract the AI pane's
# unsent draft by making claude itself persist it: Esc Esc with a non-empty
# input appends the draft (full text, newlines, [Image #N]/[Pasted text #N]
# markers) to the shared prompt history. The lone Escape first interrupts a
# streaming turn (no-op when idle). History growth within the poll window is
# the "there WAS a draft" signal — an empty input appends nothing. Prints the
# stashed draft text; exit 0 iff stashed. Fail-open: any miss just means the
# switch behaves as before this feature.
stash_ai_draft() {
  local tmux_cmd="$1" pane="$2" hist="$3"
  command -v python3 >/dev/null 2>&1 || return 1
  local before=0 after i
  [ -f "$hist" ] && before="$(wc -l < "$hist")"
  "$tmux_cmd" send-keys -t "$pane" Escape 2>/dev/null || return 1
  sleep 0.2
  "$tmux_cmd" send-keys -t "$pane" Escape Escape 2>/dev/null || return 1
  for i in $(seq 1 15); do
    after=0
    [ -f "$hist" ] && after="$(wc -l < "$hist")"
    if [ "$after" -gt "$before" ]; then
      python3 - "$hist" <<'PYEOF'
import json, sys
with open(sys.argv[1], "rb") as f:
    lines = [l for l in f.read().splitlines() if l.strip()]
if lines:
    print(json.loads(lines[-1]).get("display", ""), end="")
PYEOF
      return 0
    fi
    sleep 0.1
  done
  return 1
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./test/bash/... -run TestStashAIDraft -v`
Expected: all three PASS.

- [ ] **Step 5: shellcheck and commit**

```bash
shellcheck lib/account-switch.sh
git add lib/account-switch.sh test/bash/draft_restore_test.go
git commit -m "feat(account-switch): stash_ai_draft extracts the unsent draft via Esc-Esc history stash"
```

---

### Task 2: `draft_cache_root` — locate the old account's image cache

**Files:**
- Modify: `lib/account-switch.sh` (add after `stash_ai_draft`)
- Test: `test/bash/draft_restore_test.go`

**Interfaces:**
- Consumes: nothing from other tasks.
- Produces: `draft_cache_root <accounts_dir> <session_acct>` — prints the config root whose `image-cache/` holds the dying pane's pasted images: `$HOME/.claude` when `session_acct` is empty (Default login), else `<accounts_dir>/<session_acct>`.

- [ ] **Step 1: Write the failing tests**

Append to `test/bash/draft_restore_test.go`:

```go
// The draft's pasted images live under the config root of the account the
// pane WAS running (images are cached at paste time, before the switch).
func TestDraftCacheRoot_default_login_uses_home_claude(t *testing.T) {
	dir := t.TempDir()
	env := buildEnv(t, nil, "HOME="+dir)
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		`draft_cache_root "/cfg/claude-accounts" ""`), env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != dir+"/.claude" {
		t.Fatalf("expected %s/.claude, got %q", dir, out)
	}
}

func TestDraftCacheRoot_managed_login_uses_account_dir(t *testing.T) {
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		`draft_cache_root "/cfg/claude-accounts" "work"`), nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "/cfg/claude-accounts/work" {
		t.Fatalf("expected /cfg/claude-accounts/work, got %q", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./test/bash/... -run TestDraftCacheRoot -v`
Expected: FAIL — `draft_cache_root: command not found`.

- [ ] **Step 3: Implement**

```bash
# draft_cache_root <accounts_dir> <session_acct> — config root of the account
# the pane WAS running before the switch: its image-cache/ holds the draft's
# pasted images (written at paste time). Empty acct = the Default (Keychain)
# login, whose root is ~/.claude. The NEW login only needs to READ this path,
# so no cache sharing across accounts is required.
draft_cache_root() {
  local accounts_dir="$1" acct="$2"
  if [ -n "$acct" ]; then
    printf '%s/%s\n' "$accounts_dir" "$acct"
  else
    printf '%s/.claude\n' "$HOME"
  fi
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./test/bash/... -run TestDraftCacheRoot -v`
Expected: PASS.

- [ ] **Step 5: shellcheck and commit**

```bash
shellcheck lib/account-switch.sh
git add lib/account-switch.sh test/bash/draft_restore_test.go
git commit -m "feat(account-switch): draft_cache_root resolves the old login's image-cache root"
```

---

### Task 3: `wait_ai_pane_ready` — gate the replay on the ready prompt

**Files:**
- Modify: `lib/account-switch.sh` (add after `draft_cache_root`)
- Test: `test/bash/draft_restore_test.go`

**Interfaces:**
- Consumes: nothing from other tasks.
- Produces: `wait_ai_pane_ready <tmux_cmd> <pane> [iters]` — polls `capture-pane -p` (default 60 iterations × 0.5s ≈ 30s) until a frame contains an EMPTY prompt line (`❯` alone, optional trailing spaces). Exit 0 when ready, 1 on timeout. Matching the empty prompt specifically keeps replay keys away from trust/login dialogs, whose option rows also start with `❯ ` but carry text.

- [ ] **Step 1: Write the failing tests**

Append to `test/bash/draft_restore_test.go`:

```go
// The replay must not start while claude shows a trust/login dialog (whose
// option rows start with "❯ 2. No, exit" — pasting there would drive the
// dialog). Only an EMPTY prompt line means the input field is ready. The mock
// serves a dialog frame twice, then a ready frame, via a call-count file.
func TestWaitAIPaneReady_waits_for_empty_prompt_line(t *testing.T) {
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "capture-pane" ]; then
  n=$(cat %q 2>/dev/null || echo 0); n=$((n+1)); echo "$n" > %q
  if [ "$n" -lt 3 ]; then
    printf '%%s\n' "Do you trust this folder?" "❯ 2. No, exit"
  else
    printf '%%s\n' "some banner" "❯ " "statusline"
  fi
fi`, count, count))
	env := buildEnv(t, []string{bin})
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		"wait_ai_pane_ready tmux %1 10"), env)
	assertExitCode(t, code, 0)
	got, _ := os.ReadFile(count)
	if strings.TrimSpace(string(got)) != "3" {
		t.Fatalf("expected ready on 3rd capture, count file: %q", got)
	}
}

func TestWaitAIPaneReady_times_out_on_dialog(t *testing.T) {
	dir := t.TempDir()
	bin := mockCommand(t, dir, "tmux", `
if [ "$1" = "capture-pane" ]; then printf '%s\n' "❯ 2. No, exit"; fi`)
	env := buildEnv(t, []string{bin})
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		"wait_ai_pane_ready tmux %1 2"), env)
	if code == 0 {
		t.Fatal("expected timeout (nonzero exit) while a dialog is showing")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./test/bash/... -run TestWaitAIPaneReady -v`
Expected: FAIL — `wait_ai_pane_ready: command not found`.

- [ ] **Step 3: Implement**

```bash
# wait_ai_pane_ready <tmux_cmd> <pane> [iters] — poll (iters × 0.5s, default
# ~30s) until the relaunched claude shows an EMPTY ready input line: "❯"
# alone on its line. Trust/login/update dialogs also render "❯"-prefixed rows
# but always with text after it, so this match keeps the replay's pastes away
# from dialogs. Timeout is fail-open: the draft stays one Up-press away in
# prompt history.
wait_ai_pane_ready() {
  local tmux_cmd="$1" pane="$2" iters="${3:-60}" i
  for i in $(seq 1 "$iters"); do
    if "$tmux_cmd" capture-pane -p -t "$pane" 2>/dev/null | grep -qE '^❯ *$'; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./test/bash/... -run TestWaitAIPaneReady -v`
Expected: PASS.

- [ ] **Step 5: shellcheck and commit**

```bash
shellcheck lib/account-switch.sh
git add lib/account-switch.sh test/bash/draft_restore_test.go
git commit -m "feat(account-switch): wait_ai_pane_ready gates draft replay on the empty prompt"
```

---

### Task 4: `replay_ai_draft` — reconstruct the input field

**Files:**
- Modify: `lib/account-switch.sh` (add after `wait_ai_pane_ready`)
- Test: `test/bash/draft_restore_test.go`

**Interfaces:**
- Consumes: nothing from other tasks (pure tmux + filesystem).
- Produces: `replay_ai_draft <tmux_cmd> <pane> <draft> <cache_root> <sid>` — pastes the draft back segment by segment. Text segments and image paths both go through a tmux buffer with bracketed paste (`load-buffer -b wispdraft -` / `paste-buffer -p -b wispdraft -t <pane>`). An `[Image #N]` marker becomes the path `<cache_root>/image-cache/<sid>/<N>.png` iff that file exists and `sid`/`N` are sane; otherwise its literal text. Never sends Enter.

- [ ] **Step 1: Write the failing tests**

Append to `test/bash/draft_restore_test.go`:

```go
// The mock tmux records each paste's exact bytes: load-buffer receives the
// segment on stdin (logged with a PASTE: prefix and %n for embedded
// newlines), paste-buffer is the delivery. This pins the whole replay
// contract: split at [Image #N] only, cached markers become file paths (a
// bracketed-pasted image path is re-attached by claude as a live chip — the
// screenshot-drop mechanism), uncached/malformed markers and [Pasted text #N]
// stay literal, newlines survive, and nothing is ever submitted.
func TestReplayAIDraft_pastes_text_and_image_paths_in_order(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "load-buffer" ]; then
  printf 'PASTE:%%s\n' "$(cat | tr '\n' '\001' | tr '\001' '%%')" >> %q
else
  printf '%%s\n' "$*" >> %q
fi`, rec, rec))
	sid := "aaaa-bbbb"
	cache := filepath.Join(dir, "root", "image-cache", sid)
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "2.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	draft := "line one\nline two [Image #2] tail [Pasted text #3 +300 lines] [Image #9]"
	env := buildEnv(t, []string{bin})
	_, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		"replay_ai_draft tmux %%1 %q %q %q", draft, filepath.Join(dir, "root"), sid)), env)
	assertExitCode(t, code, 0)
	logOut, _ := os.ReadFile(rec)
	pastes := []string{}
	for _, l := range strings.Split(string(logOut), "\n") {
		if strings.HasPrefix(l, "PASTE:") {
			pastes = append(pastes, strings.TrimPrefix(l, "PASTE:"))
		}
	}
	want := []string{
		"line one%line two ",
		filepath.Join(dir, "root", "image-cache", sid, "2.png"),
		" tail [Pasted text #3 +300 lines] ",
		"[Image #9]", // no 9.png on disk -> literal marker
	}
	if strings.Join(pastes, "|") != strings.Join(want, "|") {
		t.Fatalf("paste sequence mismatch:\n got %q\nwant %q", pastes, want)
	}
	assertContains(t, string(logOut), "paste-buffer -p") // bracketed: newlines must not submit
	assertNotContains(t, string(logOut), "Enter")        // nothing is ever auto-submitted
}

// An unstamped session id means image markers cannot be mapped — every
// segment degrades to literal text, and the function still succeeds.
func TestReplayAIDraft_empty_sid_keeps_markers_literal(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "load-buffer" ]; then printf 'PASTE:%%s\n' "$(cat)" >> %q; fi`, rec))
	env := buildEnv(t, []string{bin})
	_, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		"replay_ai_draft tmux %%1 %q %q %q", "hi [Image #1]", dir, "")), env)
	assertExitCode(t, code, 0)
	logOut, _ := os.ReadFile(rec)
	assertContains(t, string(logOut), "PASTE:[Image #1]")
	assertNotContains(t, string(logOut), ".png")
}

// A malformed marker (no closing bracket) must not hang the split loop.
func TestReplayAIDraft_unterminated_marker_does_not_hang(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "load-buffer" ]; then printf 'PASTE:%%s\n' "$(cat)" >> %q; fi`, rec))
	env := buildEnv(t, []string{bin})
	done := make(chan int, 1)
	go func() {
		_, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
			"replay_ai_draft tmux %%1 %q %q %q", "text [Image #5", dir, "sid")), env)
		done <- code
	}()
	select {
	case code := <-done:
		assertExitCode(t, code, 0)
	case <-time.After(15 * time.Second):
		t.Fatal("replay_ai_draft hung on an unterminated [Image # marker")
	}
	logOut, _ := os.ReadFile(rec)
	assertContains(t, string(logOut), "PASTE:[Image #5")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./test/bash/... -run TestReplayAIDraft -v`
Expected: FAIL — `replay_ai_draft: command not found`.

- [ ] **Step 3: Implement**

```bash
# _draft_paste <tmux_cmd> <pane> <text> — bracketed-paste literal text into
# the pane via a named tmux buffer. Bracketed (-p) is load-bearing twice
# over: embedded newlines must not submit, and a pasted image PATH is only
# re-attached as a live image chip when it arrives as a paste (the same
# mechanism the screenshot drop uses). Best-effort: a failed paste degrades
# to "draft stays in history".
_draft_paste() {
  local tmux_cmd="$1" pane="$2" text="$3"
  printf '%s' "$text" | "$tmux_cmd" load-buffer -b wispdraft - 2>/dev/null || return 0
  "$tmux_cmd" paste-buffer -p -b wispdraft -t "$pane" 2>/dev/null || true
  return 0
}

# replay_ai_draft <tmux_cmd> <pane> <draft> <cache_root> <sid> — rebuild the
# input field from the stashed draft text. Split at [Image #N] markers; text
# segments paste verbatim, and each marker whose cached PNG exists
# (<cache_root>/image-cache/<sid>/<N>.png, written by the OLD claude at paste
# time) pastes as that absolute path — the new claude re-attaches it as a
# live image. Missing file, unstamped sid, or a malformed marker degrade to
# the literal marker text. [Pasted text #N] markers are never split on: their
# bytes died with the old process (memory-only), so they ride along inside
# text segments. Nothing here ever submits.
replay_ai_draft() {
  local tmux_cmd="$1" pane="$2" draft="$3" cache_root="$4" sid="$5"
  local rest="$draft" pre marker n img
  while [ -n "$rest" ]; do
    pre="${rest%%\[Image #*}"
    if [ "$pre" = "$rest" ]; then
      _draft_paste "$tmux_cmd" "$pane" "$rest"
      break
    fi
    [ -n "$pre" ] && _draft_paste "$tmux_cmd" "$pane" "$pre"
    rest="${rest#"$pre"}"
    if [ "${rest#*\]}" = "$rest" ]; then
      # No closing bracket: not a real marker — paste the remainder literally.
      _draft_paste "$tmux_cmd" "$pane" "$rest"
      break
    fi
    marker="${rest%%\]*}]"
    rest="${rest#*\]}"
    n="${marker#\[Image #}"
    n="${n%\]}"
    img=""
    case "$n" in
      *[!0-9]* | '') ;; # non-numeric: leave img empty -> literal
      *) [ -n "$sid" ] && img="$cache_root/image-cache/$sid/$n.png" ;;
    esac
    if [ -n "$img" ] && [ -f "$img" ]; then
      _draft_paste "$tmux_cmd" "$pane" "$img"
    else
      _draft_paste "$tmux_cmd" "$pane" "$marker"
    fi
    sleep 0.3 # let the image chip render before the next segment lands
  done
  return 0
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./test/bash/... -run TestReplayAIDraft -v`
Expected: all three PASS.

- [ ] **Step 5: shellcheck and commit**

```bash
shellcheck lib/account-switch.sh
git add lib/account-switch.sh test/bash/draft_restore_test.go
git commit -m "feat(account-switch): replay_ai_draft rebuilds the input incl. live image re-attachment"
```

---

### Task 5: Wire stash → relaunch → replay into the switcher

**Files:**
- Modify: `lib/account-switch.sh` — `open_account_switcher` (both the result-file and legacy branches, currently `lib/account-switch.sh:337-352`)
- Test: `test/bash/draft_restore_test.go`

**Interfaces:**
- Consumes: `stash_ai_draft`, `draft_cache_root`, `wait_ai_pane_ready`, `replay_ai_draft` (Tasks 1–4); existing `find_ai_pane`, `current_ai_session`, `relaunch_ai_pane`, `current_session_account`, and the `_rc_*` context locals.
- Produces: `_relaunch_preserving_draft <tmux_cmd> <relaunch_file> <session_acct>` — the shared "switch is happening" path: stash (claude tool only), relaunch, then a disowned background waiter that replays. Reads `_rc_tool`/`_rc_accounts_dir` from the caller's scope (same dynamic-scoping pattern `_read_relaunch_ctx` already relies on). `WISP_DECK_HISTORY_FILE` env override (default `$HOME/.claude/history.jsonl`) keeps it testable.

- [ ] **Step 1: Write the failing tests**

Append to `test/bash/draft_restore_test.go`:

```go
// End-to-end wiring through open_account_switcher with a scripted popup
// result: the stash keys go to the AI pane BEFORE respawn-pane kills it, and
// the (backgrounded) replay pastes after. The mock tmux plays claude's part:
// it appends the history entry on the Escape pair and reports a ready frame
// on capture-pane. The replay is async, so the test polls the log briefly.
func TestOpenAccountSwitcher_preserves_draft_across_relaunch(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	hist := writeTempFile(t, dir, "history.jsonl", "")
	home := filepath.Join(dir, "home")
	sid := "sid-1234"
	cache := filepath.Join(home, ".claude", "image-cache", sid)
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "1.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	// wisp-deck-tui mock: the popup "selects" the managed login by writing the
	// dir to the --result-file the switcher passes.
	tuiBin := mockCommand(t, dir, "wisp-deck-tui", `
out=""
prev=""
for a in "$@"; do
  [ "$prev" = "--result-file" ] && out="$a"
  prev="$a"
done
if [ "$1" = "claude-account-switch" ] && [ "$2" = "--help" ]; then
  echo "claude-account-switch --result-file"; exit 0
fi
[ -n "$out" ] && printf 'work\n' > "$out"`)
	tmuxBin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
case "$1" in
load-buffer) printf 'PASTE:%%s\n' "$(cat)" >> %q; exit 0 ;;
*) printf '%%s\n' "$*" >> %q ;;
esac
if [ "$1" = "send-keys" ] && [ "$4" = "Escape" ] && [ "$5" = "Escape" ]; then
  printf '%%s\n' '{"display":"draft [Image #1]","pastedContents":{}}' >> %q
fi
if [ "$1" = "list-panes" ]; then printf '%%s\n' "%%1 1"; fi
if [ "$1" = "capture-pane" ]; then printf '%%s\n' "❯ "; fi
if [ "$1" = "show-environment" ]; then
  case "$*" in
  *WISP_DECK_CLAUDE_SESSION*) echo "WISP_DECK_CLAUDE_SESSION=%s" ;;
  *WISP_DECK_CLAUDE_ACCOUNT*) echo "WISP_DECK_CLAUDE_ACCOUNT=" ;;
  esac
fi
if [ "$1" = "display-popup" ]; then
  # -E runs the popup command in a shell: this triggers the result-file write.
  eval "${@: -1}" >/dev/null 2>&1 || true
fi`, rec, rec, hist, sid))
	accountsDir := filepath.Join(dir, "claude-accounts")
	if err := os.MkdirAll(filepath.Join(accountsDir, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	relaunch := writeTempFile(t, dir, "relaunch", strings.Join([]string{
		"tool=claude", "claude_cmd=claude", "opencode_cmd=opencode",
		"settings=", "filter=", "project_dir=/proj",
		"accounts_dir=" + accountsDir,
		"pointer=" + filepath.Join(dir, "claude-account"),
		"list=" + filepath.Join(dir, "claude-accounts.list"),
		"colors=" + filepath.Join(dir, "claude-account-colors"),
		"default_label=" + filepath.Join(dir, "claude-account-default-label"),
		"",
	}, "\n"))
	if err := os.WriteFile(filepath.Join(dir, "claude-accounts.list"), []byte("Work:work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := buildEnv(t, []string{tmuxBin, tuiBin},
		"HOME="+home, "WISP_DECK_HISTORY_FILE="+hist)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("open_account_switcher tmux %q; wait", relaunch)), env)
	assertExitCode(t, code, 0)

	var logOut []byte
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		logOut, _ = os.ReadFile(rec)
		if strings.Contains(string(logOut), "PASTE:") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	s := string(logOut)
	esc := strings.Index(s, "send-keys -t %1 Escape Escape")
	respawn := strings.Index(s, "respawn-pane")
	if esc == -1 || respawn == -1 || esc > respawn {
		t.Fatalf("expected stash keys before respawn, log:\n%s", s)
	}
	assertContains(t, s, "PASTE:draft ")
	assertContains(t, s, "PASTE:"+filepath.Join(home, ".claude", "image-cache", sid, "1.png"))
}

// opencode has no Esc-Esc draft stash: a non-claude tool must switch exactly
// as before, with no stray Escape keys sent to the pane.
func TestOpenAccountSwitcher_no_stash_for_opencode(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	tuiBin := mockCommand(t, dir, "wisp-deck-tui", `
out=""
prev=""
for a in "$@"; do
  [ "$prev" = "--result-file" ] && out="$a"
  prev="$a"
done
if [ "$1" = "claude-account-switch" ] && [ "$2" = "--help" ]; then
  echo "claude-account-switch --result-file"; exit 0
fi
[ -n "$out" ] && printf 'work\n' > "$out"`)
	tmuxBin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
printf '%%s\n' "$*" >> %q
if [ "$1" = "list-panes" ]; then printf '%%s\n' "%%1 1"; fi
if [ "$1" = "display-popup" ]; then eval "${@: -1}" >/dev/null 2>&1 || true; fi`, rec))
	accountsDir := filepath.Join(dir, "claude-accounts")
	if err := os.MkdirAll(filepath.Join(accountsDir, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	relaunch := writeTempFile(t, dir, "relaunch", strings.Join([]string{
		"tool=opencode", "claude_cmd=claude", "opencode_cmd=opencode",
		"settings=", "filter=", "project_dir=/proj",
		"accounts_dir=" + accountsDir,
		"pointer=" + filepath.Join(dir, "claude-account"),
		"list=" + filepath.Join(dir, "claude-accounts.list"),
		"colors=" + filepath.Join(dir, "claude-account-colors"),
		"default_label=" + filepath.Join(dir, "claude-account-default-label"),
		"",
	}, "\n"))
	env := buildEnv(t, []string{tmuxBin, tuiBin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("open_account_switcher tmux %q; wait", relaunch)), env)
	assertExitCode(t, code, 0)
	logOut, _ := os.ReadFile(rec)
	assertNotContains(t, string(logOut), "Escape")
	assertContains(t, string(logOut), "respawn-pane")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./test/bash/... -run "TestOpenAccountSwitcher_preserves_draft|TestOpenAccountSwitcher_no_stash" -v`
Expected: FAIL — no `PASTE:` lines / stray behavior, because the wiring does not exist yet.

- [ ] **Step 3: Implement the wiring**

Add before `open_account_switcher` in `lib/account-switch.sh`:

```bash
# _relaunch_preserving_draft <tmux_cmd> <relaunch_file> <session_acct> — the
# "switch is happening" path shared by the result-file and legacy flows:
# stash the pane's unsent draft (claude only — opencode has no Esc-Esc
# stash), relaunch under the new login, then hand the stashed text to a
# DISOWNED background waiter that replays it once the new claude shows its
# empty prompt. Reads _rc_tool/_rc_accounts_dir from the caller's scope (the
# same dynamic-scoping contract _read_relaunch_ctx uses). Every step is
# fail-open: a missed stash or a never-ready pane leaves the switch exactly
# as it behaved before this feature (worst case the draft sits in prompt
# history, one Up away).
_relaunch_preserving_draft() {
  local tmux_cmd="$1" relaunch_file="$2" session_acct="$3"
  local draft="" sid="" pane
  if [ "$_rc_tool" = "claude" ]; then
    sid="$(current_ai_session "$tmux_cmd")"
    pane="$(find_ai_pane "$tmux_cmd")"
    if [ -n "$pane" ]; then
      draft="$(stash_ai_draft "$tmux_cmd" "$pane" \
        "${WISP_DECK_HISTORY_FILE:-$HOME/.claude/history.jsonl}")" || draft=""
    fi
  fi
  relaunch_ai_pane "$tmux_cmd" "$relaunch_file"
  [ -n "$draft" ] || return 0
  local cache_root new_pane
  cache_root="$(draft_cache_root "$_rc_accounts_dir" "$session_acct")"
  new_pane="$(find_ai_pane "$tmux_cmd")"
  [ -n "$new_pane" ] || return 0
  (
    wait_ai_pane_ready "$tmux_cmd" "$new_pane" \
      && replay_ai_draft "$tmux_cmd" "$new_pane" "$draft" "$cache_root" "$sid"
  ) >/dev/null 2>&1 &
  disown 2>/dev/null || true
  return 0
}
```

Then in `open_account_switcher`, replace the two relaunch calls:

```bash
      [ "$chosen" != "$session_acct" ] && relaunch_ai_pane "$tmux_cmd" "$relaunch_file"
```
becomes
```bash
      [ "$chosen" != "$session_acct" ] && _relaunch_preserving_draft "$tmux_cmd" "$relaunch_file" "$session_acct"
```
and (legacy branch)
```bash
    [ "$before" != "$after" ] && relaunch_ai_pane "$tmux_cmd" "$relaunch_file"
```
becomes
```bash
    [ "$before" != "$after" ] && _relaunch_preserving_draft "$tmux_cmd" "$relaunch_file" "$session_acct"
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./test/bash/... -run "TestOpenAccountSwitcher|TestStashAIDraft|TestReplayAIDraft|TestWaitAIPaneReady|TestDraftCacheRoot" -v`
Expected: all PASS, including the pre-existing `TestOpenAccountSwitcher_*` tests (the wiring must not regress the launch flags / legacy flow).

- [ ] **Step 5: shellcheck and commit**

```bash
shellcheck lib/account-switch.sh
git add lib/account-switch.sh test/bash/draft_restore_test.go
git commit -m "feat(account-switch): preserve the unsent draft (incl. images) across a mid-session switch"
```

---

### Task 6: Full verification, manual e2e, push

**Files:**
- No new files; verification only.

**Interfaces:**
- Consumes: everything above.

- [ ] **Step 1: shellcheck all scripts**

Run: `find lib bin -name '*.sh' -exec shellcheck {} + && shellcheck wrapper.sh bin/wisp-deck`
Expected: no findings.

- [ ] **Step 2: Full test suite**

Run: `./run-tests.sh`
Expected: PASS, no failures.

- [ ] **Step 3: Manual e2e (live claude, per spec §Testing 5)**

In a real wisp-deck session with 2+ Claude logins:
1. Type a multi-line draft and paste an image (`[Image #1]` chip visible).
2. Click the account pill, pick the other login.
3. Expected: pane relaunches under the new login; within a few seconds the
   draft reappears in the input with a live `[Image #N]` chip.
4. Submit "what's in the image?" and confirm the model describes it.
Record the result in the commit/PR description. NOTE: the ledger caches
sourced bash — restart the compact-view pane (or the session) before testing
so it picks up the new lib code.

- [ ] **Step 4: Push**

```bash
git pull --rebase && git push && git status
```
Expected: `up to date with 'origin/main'`.
