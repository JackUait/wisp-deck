# Per-Project Session Stacking Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One Ghostty tab per project: re-picking an already-open project consolidates into a single tab holding a stack of full wisp sessions, switchable via keybindings and shown in the outer tmux status bar.

**Architecture:** All wisp sessions already live on the same default tmux server, so a tab's client can `switch-client` between them instantly. A new `lib/session-stack.sh` owns detection, a per-tab stack registry (`$SHARE_DIR/stacks/<owner-session>`), adoption/handoff, bar painting, cycle/close helpers, and an owner-pid orphan reaper. `wrapper.sh` wires it in: detects same-project sessions at pick time (tmux-only, critical-path safe), adopts them after its own session boots, and gains a stack-aware cleanup. The wrapper's server-wide `set-option exit-unattached on` teardown is **removed** (it would destroy unattached stacked sessions) and replaced by the reaper.

**Tech Stack:** bash (macOS stock bash 3.2 — no `mapfile`, no associative arrays), tmux, Go integration tests in `test/bash/` via the existing `helpers_test.go` harness.

## Global Constraints

- Spec: `docs/superpowers/specs/2026-07-21-per-project-session-stacking-design.md`.
- **No blocking subprocess on the launch critical path or post-pick path** — stacking work is tmux-only bash; `launch_critical_path_test.go` and `launch_post_pick_path_test.go` must stay green.
- **No foreground `run-shell` in the tmux launch chain** (use `run-shell -b`).
- Restore is untouched in v1: restored tabs never consolidate; `restore_pop_authorized` and the queue are not modified.
- Outer tmux mouse stays off — all stack interaction is keyboard-only.
- A session is owned by exactly one tab at every instant; no zombie, no double-kill, no wrong-kill.
- TDD: every task writes its failing test first. shellcheck on every modified script. `git push` after the suite passes.
- Session names may contain spaces (project names) — always iterate names line-wise, never word-split.
- Work directly on `main` (user preference); stage only files this plan touches (other Claude sessions share this checkout).

## File Structure

- **Create** `lib/session-stack.sh` — all stacking logic (detection, registry, bar, cycle/close, request, adoption, teardown, reaper). Independently sourceable like every `lib/*.sh`.
- **Create** `test/bash/session_stack_test.go` — behavior tests for the lib.
- **Create** `test/bash/session_stack_wrapper_test.go` — static wiring guards on `wrapper.sh` (pattern: `opencode_availability_test.go`).
- **Modify** `wrapper.sh` — lib list, request claim, adopt-list capture, owner-pid stamp, adoption call, keybindings, stack-aware cleanup, `exit-unattached` removal, reaper start.

---

### Task 1: Detection — `stack_sessions_for_project`

**Files:**
- Create: `lib/session-stack.sh`
- Test: `test/bash/session_stack_test.go`

**Interfaces:**
- Produces: `stack_sessions_for_project <tmux_cmd> <project_dir> [exclude_session]` → prints matching live wisp session names, one per line, in creation order; exit 0 always. A session matches when its tmux session env contains exactly `WISP_DECK_PATH=<project_dir>`.

- [ ] **Step 1: Write the failing test**

Create `test/bash/session_stack_test.go`:

```go
package bash_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// mockTmux writes a tmux mock whose list-sessions/show-environment answers
// come from canned data and which appends every other invocation to
// $dir/tmux.log for assertions.
func mockTmux(t *testing.T, dir, listSessions, envCase string) string {
	t.Helper()
	body := fmt.Sprintf(`
log="%s/tmux.log"
case "$1" in
  list-sessions)
    printf '%%b' %q ;;
  show-environment)
    # $2=-t $3=<session> [$4=<var>]
    all=""
    case "$3" in
%s
    esac
    if [ -n "${4:-}" ]; then
      printf '%%b' "$all" | grep "^$4=" || exit 1
    else
      printf '%%b' "$all"
    fi ;;
  has-session)
    printf '%%s\n' "$@" >> "$log" ;;
  *)
    printf '%%s\n' "$*" >> "$log" ;;
esac
`, dir, listSessions, envCase)
	return mockCommand(t, dir, "tmux", body)
}

const stackEnvTwoApps = `
      "dev-app-111") all='WISP_DECK=1\nWISP_DECK_PATH=/tmp/app\nWISP_DECK_PROJECT=app\nWISP_DECK_TOOL=claude\n' ;;
      "dev-web-222") all='WISP_DECK=1\nWISP_DECK_PATH=/tmp/web\nWISP_DECK_PROJECT=web\nWISP_DECK_TOOL=claude\n' ;;
      "dev-app-333") all='WISP_DECK=1\nWISP_DECK_PATH=/tmp/app\nWISP_DECK_PROJECT=app\nWISP_DECK_TOOL=codex\n' ;;
`

func TestStackSessionsForProject_matches_only_project_sessions_in_creation_order(t *testing.T) {
	dir := t.TempDir()
	bin := mockTmux(t, dir,
		"300 dev-app-333\n100 dev-app-111\n200 dev-web-222\n", stackEnvTwoApps)
	out, code := runBashFunc(t, "lib/session-stack.sh", "stack_sessions_for_project",
		[]string{filepath.Join(bin, "tmux"), "/tmp/app"}, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "dev-app-111\ndev-app-333" {
		t.Errorf("got %q, want dev-app-111 then dev-app-333", got)
	}
}

func TestStackSessionsForProject_excludes_named_session(t *testing.T) {
	dir := t.TempDir()
	bin := mockTmux(t, dir,
		"100 dev-app-111\n300 dev-app-333\n", stackEnvTwoApps)
	out, code := runBashFunc(t, "lib/session-stack.sh", "stack_sessions_for_project",
		[]string{filepath.Join(bin, "tmux"), "/tmp/app", "dev-app-111"}, nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "dev-app-111")
	assertContains(t, out, "dev-app-333")
}

func TestStackSessionsForProject_no_server_prints_nothing_exit_zero(t *testing.T) {
	dir := t.TempDir()
	bin := mockCommand(t, dir, "tmux", `exit 1`)
	out, code := runBashFunc(t, "lib/session-stack.sh", "stack_sessions_for_project",
		[]string{filepath.Join(bin, "tmux"), "/tmp/app"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty output, got %q", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./test/bash/ -run TestStackSessionsForProject -v`
Expected: FAIL — `lib/session-stack.sh` does not exist.

- [ ] **Step 3: Write minimal implementation**

Create `lib/session-stack.sh`:

```bash
#!/bin/bash
# Per-project session stacking. One Ghostty tab per project, holding a stack
# of full wisp sessions switchable via keybindings; the outer status bar shows
# the stack. See docs/superpowers/specs/2026-07-21-per-project-session-stacking-design.md.
#
# Everything here is fail-open: helpers exit 0 and print nothing when the tmux
# server is gone or data is missing, and callers treat that as "no stack".
# Session names may contain spaces — iterate line-wise, never word-split.

# stack_sessions_for_project <tmux_cmd> <project_dir> [exclude_session]
# Print the names of live wisp sessions whose WISP_DECK_PATH equals
# <project_dir>, one per line, oldest first. Tmux-only: this runs on the
# launch critical path.
stack_sessions_for_project() {
  local tmux_cmd="$1" project_dir="$2" exclude="${3:-}"
  local created s
  "$tmux_cmd" list-sessions -F '#{session_created} #{session_name}' 2>/dev/null \
    | sort -n | while IFS=' ' read -r created s; do
      [ -n "$s" ] || continue
      [ "$s" = "$exclude" ] && continue
      "$tmux_cmd" show-environment -t "$s" WISP_DECK_PATH 2>/dev/null \
        | grep -qxF "WISP_DECK_PATH=$project_dir" || continue
      printf '%s\n' "$s"
    done
  return 0
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./test/bash/ -run TestStackSessionsForProject -v`
Expected: PASS (all three).

- [ ] **Step 5: shellcheck and commit**

```bash
shellcheck lib/session-stack.sh
git add lib/session-stack.sh test/bash/session_stack_test.go
git commit -m "feat(stack): detect live sessions of a project"
```

---

### Task 2: Stack registry + per-session file cleanup

**Files:**
- Modify: `lib/session-stack.sh`
- Test: `test/bash/session_stack_test.go`

**Interfaces:**
- Produces:
  - `stack_add <cfg_root> <owner_session> <session>` → appends (idempotent) to `<cfg_root>/stacks/<owner_session>`, creating dirs; exit 1 only if the dir can't be created.
  - `stack_remove_entry <cfg_root> <owner_session> <session>` → removes the exact line; exit 0 even if absent.
  - `stack_list <cfg_root> <owner_session>` → prints entries, exit 0 even if no file.
  - `stack_session_files_cleanup <cfg_root> <session>` → removes `spare-<s>.conf`, `relaunch-<s>`, `proxy-<s>.log`, `proxy-account-<s>`, `spare-zdotdir-<s>/`.

- [ ] **Step 1: Write the failing tests** (append to `test/bash/session_stack_test.go`)

```go
func TestStackRegistry_add_list_remove_roundtrip(t *testing.T) {
	cfg := t.TempDir()
	script := fmt.Sprintf(`
source lib/session-stack.sh
stack_add %q "dev-app-1" "dev-app-1"
stack_add %q "dev-app-1" "dev-app-2"
stack_add %q "dev-app-1" "dev-app-2"   # idempotent
stack_list %q "dev-app-1"
echo ---
stack_remove_entry %q "dev-app-1" "dev-app-2"
stack_list %q "dev-app-1"
`, cfg, cfg, cfg, cfg, cfg, cfg)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	if !strings.Contains(out, "dev-app-1\ndev-app-2\n---") {
		t.Errorf("add/list wrong (dup or order): %q", out)
	}
	after := out[strings.Index(out, "---"):]
	assertNotContains(t, after, "dev-app-2")
}

func TestStackSessionFilesCleanup_removes_all_per_session_files(t *testing.T) {
	cfg := t.TempDir()
	s := "dev-app-42"
	for _, f := range []string{"spare-" + s + ".conf", "relaunch-" + s, "proxy-" + s + ".log", "proxy-account-" + s} {
		writeTempFile(t, cfg, f, "x")
	}
	script := fmt.Sprintf(`
mkdir -p %q/spare-zdotdir-%s
source lib/session-stack.sh
stack_session_files_cleanup %q %q
ls %q
`, cfg, s, cfg, s, cfg)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	for _, f := range []string{"spare-", "relaunch-", "proxy-"} {
		assertNotContains(t, out, f)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./test/bash/ -run 'TestStackRegistry|TestStackSessionFilesCleanup' -v`
Expected: FAIL — functions not defined.

- [ ] **Step 3: Implement** (append to `lib/session-stack.sh`)

```bash
# Stack registry: <cfg_root>/stacks/<owner_session> lists every session that
# tab owns (including its own). The owning wrapper's cleanup kills exactly
# this list; adoption edits it. Single writer per file in practice (the
# owning wrapper and the close/adopt helpers it spawns), no locking.

stack_add() {
  local cfg="$1" owner="$2" session="$3" f
  mkdir -p "$cfg/stacks" 2>/dev/null || return 1
  f="$cfg/stacks/$owner"
  grep -qxF "$session" "$f" 2>/dev/null && return 0
  printf '%s\n' "$session" >> "$f"
}

stack_remove_entry() {
  local cfg="$1" owner="$2" session="$3" f tmp
  f="$cfg/stacks/$owner"
  [ -f "$f" ] || return 0
  tmp="$f.tmp.$$"
  grep -vxF "$session" "$f" > "$tmp" 2>/dev/null || true
  mv "$tmp" "$f"
}

stack_list() {
  cat "$1/stacks/$2" 2>/dev/null
  return 0
}

# Remove the SHARE_DIR files wrapper.sh creates per session (mirrors the rm
# lines in its cleanup()). Used for adopted sessions, whose original wrapper
# is gone by the time they die.
stack_session_files_cleanup() {
  local cfg="$1" s="$2"
  rm -f "$cfg/spare-${s}.conf" "$cfg/relaunch-${s}" \
    "$cfg/proxy-${s}.log" "$cfg/proxy-account-${s}"
  rm -rf "$cfg/spare-zdotdir-${s}"
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./test/bash/ -run 'TestStackRegistry|TestStackSessionFilesCleanup' -v` → PASS.

- [ ] **Step 5: shellcheck and commit**

```bash
shellcheck lib/session-stack.sh
git add lib/session-stack.sh test/bash/session_stack_test.go
git commit -m "feat(stack): stack registry and per-session file cleanup"
```

---

### Task 3: Session bar — chips and repaint

**Files:**
- Modify: `lib/session-stack.sh`
- Test: `test/bash/session_stack_test.go`

**Interfaces:**
- Consumes: `stack_sessions_for_project` (Task 1); `get_theme_accent`/`gt_resolve_theme` from `lib/theme.sh` when loaded (optional — falls back to accent 209).
- Produces:
  - `stack_bar_chips <project_name> <self_session> <accent> <session>...` → prints the status-left string. With ≤1 session it prints exactly today's default `" ⬡ <project> "` (bar looks unchanged). With N≥2, chips `1..N` follow; the self chip is `#[fg=colour235,bg=colour<accent>,bold] <i> #[default...]`, others `#[fg=colour245]`.
  - `stack_repaint <tmux_cmd> <cfg_root> <project_name> <project_dir>` → sets `status-left` on every session of the project, each with itself as the active chip and its own tool's accent.

- [ ] **Step 1: Write the failing tests** (append)

```go
func TestStackBarChips_single_session_is_plain_project_chip(t *testing.T) {
	out, code := runBashFunc(t, "lib/session-stack.sh", "stack_bar_chips",
		[]string{"app", "dev-app-1", "209", "dev-app-1"}, nil)
	assertExitCode(t, code, 0)
	if out != " ⬡ app " {
		t.Errorf("single-session bar must equal today's default, got %q", out)
	}
}

func TestStackBarChips_marks_self_with_accent(t *testing.T) {
	out, code := runBashFunc(t, "lib/session-stack.sh", "stack_bar_chips",
		[]string{"app", "dev-app-2", "141", "dev-app-1", "dev-app-2"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "bg=colour141,bold] 2 ")   // self chip, accented
	assertContains(t, out, "#[fg=colour245] 1 ")      // other chip, plain
	assertContains(t, out, "⬡ app")
}

func TestStackRepaint_sets_status_left_per_session_with_self_active(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir()
	bin := mockTmux(t, dir, "100 dev-app-111\n300 dev-app-333\n", stackEnvTwoApps)
	script := fmt.Sprintf(`
source lib/session-stack.sh
stack_repaint %q %q "app" "/tmp/app"
cat %q/tmux.log
`, filepath.Join(bin, "tmux"), cfg, dir)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "set-option -t dev-app-111 status-left")
	assertContains(t, out, "set-option -t dev-app-333 status-left")
	// dev-app-111's own bar must accent chip 1; dev-app-333's chip 2.
	for _, want := range []string{"bold] 1 ", "bold] 2 "} {
		assertContains(t, out, want)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./test/bash/ -run 'TestStackBarChips|TestStackRepaint' -v` → FAIL (not defined).

- [ ] **Step 3: Implement** (append to `lib/session-stack.sh`)

```bash
# stack_bar_chips <project_name> <self_session> <accent> <session>...
# The outer status-left for one session of a stack. With a single session the
# bar is exactly today's " ⬡ project " so the common case looks unchanged.
# The bar's base style stays fg=white,bg=colour236,bold (status-left-style set
# at launch); chips restore it after their own colours.
stack_bar_chips() {
  local project="$1" self="$2" accent="$3"
  shift 3
  local out=" ⬡ ${project} " i=0 s
  if [ "$#" -le 1 ]; then
    printf '%s' "$out"
    return 0
  fi
  for s in "$@"; do
    i=$((i + 1))
    if [ "$s" = "$self" ]; then
      out="${out}#[fg=colour235,bg=colour${accent},bold] ${i} #[default]#[fg=white,bg=colour236,bold] "
    else
      out="${out}#[fg=colour245] ${i} #[default]#[fg=white,bg=colour236,bold] "
    fi
  done
  printf '%s' "$out"
}

# stack_repaint <tmux_cmd> <cfg_root> <project_name> <project_dir>
# Rebuild every project session's status-left. Each session's bar marks its
# OWN chip active (the visible bar always belongs to the current session), in
# the accent of that session's tool — honouring the user theme preset when
# lib/theme.sh is loaded.
stack_repaint() {
  local tmux_cmd="$1" cfg="$2" project="$3" dir="$4"
  local sessions=() s tool pref accent chips
  while IFS= read -r s; do
    [ -n "$s" ] && sessions+=("$s")
  done < <(stack_sessions_for_project "$tmux_cmd" "$dir")
  [ "${#sessions[@]}" -gt 0 ] || return 0
  pref="$(grep '^theme=' "$cfg/settings" 2>/dev/null | cut -d= -f2 | tr -d '[:space:]')"
  for s in "${sessions[@]}"; do
    tool="$("$tmux_cmd" show-environment -t "$s" WISP_DECK_TOOL 2>/dev/null | cut -d= -f2-)"
    accent=209
    if command -v get_theme_accent >/dev/null 2>&1 \
      && command -v gt_resolve_theme >/dev/null 2>&1; then
      accent="$(get_theme_accent "$(gt_resolve_theme "$pref" "$tool")")"
    fi
    chips="$(stack_bar_chips "$project" "$s" "$accent" "${sessions[@]}")"
    "$tmux_cmd" set-option -t "$s" status-left "$chips" 2>/dev/null || true
  done
  return 0
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./test/bash/ -run 'TestStackBarChips|TestStackRepaint' -v` → PASS.

- [ ] **Step 5: shellcheck and commit**

```bash
shellcheck lib/session-stack.sh
git add lib/session-stack.sh test/bash/session_stack_test.go
git commit -m "feat(stack): session bar chips and per-session repaint"
```

---

### Task 4: Cycle and close-current helpers

**Files:**
- Modify: `lib/session-stack.sh`
- Test: `test/bash/session_stack_test.go`

**Interfaces:**
- Consumes: Task 1–3 helpers; `cleanup_tmux_session <name> <watcher_pid> <tmux_cmd>` (lib/tmux-session.sh); `attention_cleanup`, `keep_awake_drop` when loaded (guarded by `command -v`).
- Produces:
  - `stack_cycle <tmux_cmd> <current_session> <next|prev>` → `switch-client -t <neighbour>`; no-op (exit 0) with <2 sessions.
  - `stack_close_current <tmux_cmd> <cfg_root> <current_session>` → switch client to a neighbour (if any), full per-session teardown of the current session, drop it from every stack file, repaint. Killing the last session performs no switch — the dying client unwinds the owning wrapper.

- [ ] **Step 1: Write the failing tests** (append)

```go
func TestStackCycle_switches_to_next_and_wraps(t *testing.T) {
	dir := t.TempDir()
	bin := mockTmux(t, dir, "100 dev-app-111\n300 dev-app-333\n", stackEnvTwoApps)
	script := fmt.Sprintf(`
source lib/session-stack.sh
stack_cycle %q "dev-app-333" next
cat %q/tmux.log
`, filepath.Join(bin, "tmux"), dir)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "switch-client -t dev-app-111") // wraps from last to first
}

func TestStackCycle_single_session_is_noop(t *testing.T) {
	dir := t.TempDir()
	bin := mockTmux(t, dir, "200 dev-web-222\n", stackEnvTwoApps)
	script := fmt.Sprintf(`
source lib/session-stack.sh
stack_cycle %q "dev-web-222" next
cat %q/tmux.log 2>/dev/null || true
`, filepath.Join(bin, "tmux"), dir)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "switch-client")
}

func TestStackCloseCurrent_switches_then_kills_and_deregisters(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir()
	bin := mockTmux(t, dir, "100 dev-app-111\n300 dev-app-333\n", stackEnvTwoApps)
	script := fmt.Sprintf(`
source lib/session-stack.sh
cleanup_tmux_session() { echo "CLEANUP:$1" ; }   # stub the heavy teardown
stack_add %q "dev-app-111" "dev-app-111"
stack_add %q "dev-app-111" "dev-app-333"
stack_close_current %q %q "dev-app-333"
echo "STACK:$(stack_list %q dev-app-111 | tr '\n' ',')"
cat %q/tmux.log
`, cfg, cfg, filepath.Join(bin, "tmux"), cfg, cfg, dir)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "switch-client -t dev-app-111")
	assertContains(t, out, "CLEANUP:dev-app-333")
	assertContains(t, out, "STACK:dev-app-111,")
	assertNotContains(t, out, "dev-app-333,")
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./test/bash/ -run 'TestStackCycle|TestStackCloseCurrent' -v` → FAIL.

- [ ] **Step 3: Implement** (append to `lib/session-stack.sh`)

```bash
# stack_cycle <tmux_cmd> <current_session> <next|prev>
# Move the pressing client to the neighbouring session of the same project.
# Bound in wrapper.sh with #{session_name} so it always acts on the session
# the key was pressed in (bind-key is server-global — never bake a name).
stack_cycle() {
  local tmux_cmd="$1" current="$2" direction="${3:-next}"
  local path sessions=() s idx=-1 i n target
  path="$("$tmux_cmd" show-environment -t "$current" WISP_DECK_PATH 2>/dev/null | cut -d= -f2-)"
  [ -n "$path" ] || return 0
  while IFS= read -r s; do
    [ -n "$s" ] && sessions+=("$s")
  done < <(stack_sessions_for_project "$tmux_cmd" "$path")
  n=${#sessions[@]}
  [ "$n" -gt 1 ] || return 0
  for ((i = 0; i < n; i++)); do
    [ "${sessions[$i]}" = "$current" ] && idx=$i
  done
  [ "$idx" -ge 0 ] || return 0
  if [ "$direction" = "prev" ]; then
    target="${sessions[$(((idx - 1 + n) % n))]}"
  else
    target="${sessions[$(((idx + 1) % n))]}"
  fi
  "$tmux_cmd" switch-client -t "$target"
}

# stack_close_current <tmux_cmd> <cfg_root> <current_session>
# Close ONLY the current stack session: move the client to a neighbour first
# (killing the session under the client would end the whole tab), then full
# per-session teardown, then deregister from whichever stack file holds it.
# Closing the LAST session skips the switch — the client dies with the
# session and the owning wrapper's cleanup unwinds the tab.
stack_close_current() {
  local tmux_cmd="$1" cfg="$2" current="$3"
  local path project sessions=() s neighbour="" root f
  path="$("$tmux_cmd" show-environment -t "$current" WISP_DECK_PATH 2>/dev/null | cut -d= -f2-)"
  project="$("$tmux_cmd" show-environment -t "$current" WISP_DECK_PROJECT 2>/dev/null | cut -d= -f2-)"
  root="$("$tmux_cmd" show-environment -t "$current" WISP_DECK_ATTENTION_ROOT 2>/dev/null | cut -d= -f2-)"
  if [ -n "$path" ]; then
    while IFS= read -r s; do
      [ -n "$s" ] && [ "$s" != "$current" ] && sessions+=("$s")
    done < <(stack_sessions_for_project "$tmux_cmd" "$path")
  fi
  [ "${#sessions[@]}" -gt 0 ] && neighbour="${sessions[0]}"
  [ -n "$neighbour" ] && "$tmux_cmd" switch-client -t "$neighbour" 2>/dev/null
  cleanup_tmux_session "$current" "" "$tmux_cmd"
  command -v attention_cleanup >/dev/null 2>&1 && attention_cleanup "$root" 2>/dev/null
  command -v keep_awake_drop >/dev/null 2>&1 && keep_awake_drop "$cfg" "$current" 2>/dev/null
  stack_session_files_cleanup "$cfg" "$current"
  for f in "$cfg"/stacks/*; do
    [ -f "$f" ] || continue
    stack_remove_entry "$cfg" "${f##*/}" "$current"
  done
  [ -n "$neighbour" ] && stack_repaint "$tmux_cmd" "$cfg" "$project" "$path"
  return 0
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./test/bash/ -run 'TestStackCycle|TestStackCloseCurrent' -v` → PASS.

- [ ] **Step 5: shellcheck and commit**

```bash
shellcheck lib/session-stack.sh
git add lib/session-stack.sh test/bash/session_stack_test.go
git commit -m "feat(stack): cycle and close-current session helpers"
```

---

### Task 5: New-session request (write + claim)

**Files:**
- Modify: `lib/session-stack.sh`
- Test: `test/bash/session_stack_test.go`

**Interfaces:**
- Consumes: `restore_trigger_tab` from `lib/session-restore.sh` (Cmd+T via osascript — same mechanism as the restore chain).
- Produces:
  - `stack_request_new <tmux_cmd> <cfg_root> <session>` → resolves the session's project dir, writes one-shot `<cfg_root>/stack-request` (`<epoch>|<dir>`), triggers Cmd+T; on trigger failure removes the request and returns 1 (caller shows a tmux message).
  - `stack_request_claim <cfg_root>` → mv-claims the request atomically; prints the project dir; returns 1 when absent, stale (>60s), malformed, or the dir is gone. Consumed by wrapper.sh before the picker.

- [ ] **Step 1: Write the failing tests** (append)

```go
func TestStackRequest_write_then_claim_roundtrip(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir()
	proj := t.TempDir()
	env := fmt.Sprintf(`
      "dev-app-111") all='WISP_DECK=1\nWISP_DECK_PATH=%s\n' ;;
`, proj)
	bin := mockTmux(t, dir, "100 dev-app-111\n", env)
	script := fmt.Sprintf(`
source lib/session-stack.sh
restore_trigger_tab() { return 0; }   # stub the osascript Cmd+T
stack_request_new %q %q "dev-app-111" || echo "WRITE-FAILED"
stack_request_claim %q
stack_request_claim %q || echo "SECOND-CLAIM-FAILS"
`, filepath.Join(bin, "tmux"), cfg, cfg, cfg)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "WRITE-FAILED")
	assertContains(t, out, proj)
	assertContains(t, out, "SECOND-CLAIM-FAILS") // one-shot
}

func TestStackRequestClaim_rejects_stale_request(t *testing.T) {
	cfg := t.TempDir()
	proj := t.TempDir()
	writeTempFile(t, cfg, "stack-request", fmt.Sprintf("100|%s\n", proj)) // epoch 100 = ancient
	script := fmt.Sprintf(`
source lib/session-stack.sh
stack_request_claim %q || echo "STALE-REJECTED"
`, cfg)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "STALE-REJECTED")
	assertNotContains(t, out, proj)
}

func TestStackRequestNew_failed_trigger_removes_request(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir()
	bin := mockTmux(t, dir, "100 dev-app-111\n", stackEnvTwoApps)
	script := fmt.Sprintf(`
source lib/session-stack.sh
restore_trigger_tab() { return 1; }
stack_request_new %q %q "dev-app-111" && echo "UNEXPECTED-OK"
[ -f %q/stack-request ] && echo "REQUEST-LEFT-BEHIND"
true
`, filepath.Join(bin, "tmux"), cfg, cfg)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "UNEXPECTED-OK")
	assertNotContains(t, out, "REQUEST-LEFT-BEHIND")
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./test/bash/ -run TestStackRequest -v` → FAIL.

- [ ] **Step 3: Implement** (append to `lib/session-stack.sh`)

```bash
# One-shot "open a fresh stack session for this project" request — the same
# ticket pattern as the restore chain (see restore_issue_chain_ticket): the
# hotkey writes the request and simulates Cmd+T; the fresh tab's wrapper
# mv-claims it and skips the picker. Stale (>60s) requests are never claimed,
# so a broken trigger can't hijack a tab the user opens later.

# stack_request_new <tmux_cmd> <cfg_root> <session>
stack_request_new() {
  local tmux_cmd="$1" cfg="$2" session="$3" dir
  dir="$("$tmux_cmd" show-environment -t "$session" WISP_DECK_PATH 2>/dev/null | cut -d= -f2-)"
  [ -n "$dir" ] || return 1
  printf '%s|%s\n' "$(date +%s)" "$dir" > "$cfg/stack-request" 2>/dev/null || return 1
  if ! restore_trigger_tab; then
    rm -f "$cfg/stack-request"
    return 1
  fi
}

# stack_request_claim <cfg_root> — prints the requested project dir.
stack_request_claim() {
  local cfg="$1" req="$1/stack-request" claimed stamp dir now
  [ -f "$req" ] || return 1
  claimed="$req.claimed.$$"
  mv "$req" "$claimed" 2>/dev/null || return 1
  IFS='|' read -r stamp dir < "$claimed" || true
  rm -f "$claimed"
  case "$stamp" in '' | *[!0-9]*) return 1 ;; esac
  now="$(date +%s)"
  [ $((now - stamp)) -le 60 ] || return 1
  [ -d "$dir" ] || return 1
  printf '%s\n' "$dir"
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./test/bash/ -run TestStackRequest -v` → PASS.

- [ ] **Step 5: shellcheck and commit**

```bash
shellcheck lib/session-stack.sh
git add lib/session-stack.sh test/bash/session_stack_test.go
git commit -m "feat(stack): one-shot new-session request write/claim"
```

---

### Task 6: Adoption, adopted-away check, owner teardown

**Files:**
- Modify: `lib/session-stack.sh`
- Test: `test/bash/session_stack_test.go`

**Interfaces:**
- Consumes: registry (Task 2), `cleanup_tmux_session` (stubbed in tests).
- Produces:
  - `stack_adopt_all <tmux_cmd> <cfg_root> <new_session> <owner_pid> <old_session>...` → per session: verify alive → `stack_add` to the NEW owner's file → set env `WISP_DECK_ADOPTED_BY=<new_session>` → set env `WISP_DECK_OWNER_PID=<owner_pid>`. **This order is the no-zombie invariant**: registered with the new owner *before* the marker tells the old owner to skip it. A crash between the steps leaves the session doubly covered (both would kill it), never orphaned.
  - `stack_finalize_adoption <tmux_cmd> <new_session> <old_session>...` → polls up to 20s for a client attached to `<new_session>`, then `detach-client -s` each old session (unwinds the old wrappers). Backgrounded by wrapper.sh.
  - `stack_adopted_away <tmux_cmd> <session>` → exit 0 iff the session is alive and its `WISP_DECK_ADOPTED_BY` env is non-empty.
  - `stack_owner_teardown <tmux_cmd> <cfg_root> <owner_session>` → for every stack-file entry except `<owner_session>` itself: skip if dead or adopted away by a *different* owner; else read its attention root from session env, `cleanup_tmux_session`, `attention_cleanup`, `keep_awake_drop`, `stack_session_files_cleanup`. Finally remove the stack file.

- [ ] **Step 1: Write the failing tests** (append)

```go
func TestStackAdoptAll_registers_before_marking(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir()
	bin := mockTmux(t, dir, "100 dev-app-111\n", stackEnvTwoApps)
	script := fmt.Sprintf(`
source lib/session-stack.sh
stack_adopt_all %q %q "dev-app-999" "4242" "dev-app-111"
echo "STACK:$(stack_list %q dev-app-999 | tr '\n' ',')"
cat %q/tmux.log
`, filepath.Join(bin, "tmux"), cfg, cfg, dir)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "STACK:dev-app-111,")
	assertContains(t, out, "set-environment -t dev-app-111 WISP_DECK_ADOPTED_BY dev-app-999")
	assertContains(t, out, "set-environment -t dev-app-111 WISP_DECK_OWNER_PID 4242")
}

func TestStackAdoptedAway_true_only_when_marker_set(t *testing.T) {
	dir := t.TempDir()
	envCase := `
      "dev-app-111") all='WISP_DECK=1\nWISP_DECK_PATH=/tmp/app\nWISP_DECK_ADOPTED_BY=dev-app-999\n' ;;
      "dev-app-333") all='WISP_DECK=1\nWISP_DECK_PATH=/tmp/app\n' ;;
`
	bin := mockTmux(t, dir, "100 dev-app-111\n300 dev-app-333\n", envCase)
	script := fmt.Sprintf(`
source lib/session-stack.sh
stack_adopted_away %q "dev-app-111" && echo "111-ADOPTED"
stack_adopted_away %q "dev-app-333" || echo "333-OWNED"
`, filepath.Join(bin, "tmux"), filepath.Join(bin, "tmux"))
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "111-ADOPTED")
	assertContains(t, out, "333-OWNED")
}

func TestStackOwnerTeardown_kills_owned_skips_adopted_away(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir()
	envCase := `
      "dev-app-111") all='WISP_DECK=1\nWISP_DECK_PATH=/tmp/app\nWISP_DECK_ATTENTION_ROOT=/tmp/att-111\n' ;;
      "dev-app-333") all='WISP_DECK=1\nWISP_DECK_PATH=/tmp/app\nWISP_DECK_ADOPTED_BY=dev-app-777\n' ;;
`
	bin := mockTmux(t, dir, "100 dev-app-111\n300 dev-app-333\n", envCase)
	script := fmt.Sprintf(`
source lib/session-stack.sh
cleanup_tmux_session() { echo "CLEANUP:$1"; }
attention_cleanup() { echo "ATTENTION:$1"; }
stack_add %q "dev-owner-9" "dev-owner-9"
stack_add %q "dev-owner-9" "dev-app-111"
stack_add %q "dev-owner-9" "dev-app-333"
stack_owner_teardown %q %q "dev-owner-9"
[ -f %q/stacks/dev-owner-9 ] && echo "STACKFILE-LEFT"
true
`, cfg, cfg, cfg, filepath.Join(bin, "tmux"), cfg, cfg)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "CLEANUP:dev-app-111")     // owned → killed
	assertContains(t, out, "ATTENTION:/tmp/att-111")  // root read from env before kill
	assertNotContains(t, out, "CLEANUP:dev-app-333")  // adopted by dev-app-777 → skipped
	assertNotContains(t, out, "CLEANUP:dev-owner-9")  // own session left to wrapper
	assertNotContains(t, out, "STACKFILE-LEFT")
}
```

Note: the `has-session` branch of `mockTmux` exits 0 for any named session in the canned list — extend the mock so `has-session -t <s>` exits 0 only when `<s>` appears in the `list-sessions` data, e.g. replace its branch with:

```bash
  has-session)
    printf '%b' LISTDATA | grep -qF " $3" ;;
```

(where `LISTDATA` is the same canned string; wire it in the Go helper).

- [ ] **Step 2: Run to verify failure**

Run: `go test ./test/bash/ -run 'TestStackAdopt|TestStackAdoptedAway|TestStackOwnerTeardown' -v` → FAIL.

- [ ] **Step 3: Implement** (append to `lib/session-stack.sh`)

```bash
# stack_adopt_all <tmux_cmd> <cfg_root> <new_session> <owner_pid> <old>...
# ORDER IS THE NO-ZOMBIE INVARIANT: a session is appended to the NEW owner's
# stack file (so the new wrapper's cleanup will kill it) BEFORE its
# adopted-by marker is set (which makes the OLD wrapper skip killing it).
# A crash between the two leaves the session doubly covered, never orphaned.
stack_adopt_all() {
  local tmux_cmd="$1" cfg="$2" new_session="$3" owner_pid="$4"
  shift 4
  local s
  for s in "$@"; do
    "$tmux_cmd" has-session -t "$s" 2>/dev/null || continue
    stack_add "$cfg" "$new_session" "$s" || continue
    "$tmux_cmd" set-environment -t "$s" WISP_DECK_ADOPTED_BY "$new_session" 2>/dev/null || continue
    "$tmux_cmd" set-environment -t "$s" WISP_DECK_OWNER_PID "$owner_pid" 2>/dev/null || true
  done
  return 0
}

# stack_finalize_adoption <tmux_cmd> <new_session> <old>...
# Wait for the adopting tab's client to attach, then detach the old tabs'
# clients so their wrappers unwind in adopted-away mode. Backgrounded by
# wrapper.sh — must never block the launch. The server-wide exit-unattached
# option no longer exists (removed with stacking), so the detach itself can
# not take the server down.
stack_finalize_adoption() {
  local tmux_cmd="$1" new_session="$2"
  shift 2
  local i s
  for i in $(seq 1 100); do
    [ -n "$("$tmux_cmd" list-clients -t "$new_session" 2>/dev/null)" ] && break
    sleep 0.2
  done
  for s in "$@"; do
    "$tmux_cmd" detach-client -s "$s" 2>/dev/null || true
  done
  return 0
}

# stack_adopted_away <tmux_cmd> <session> — was this tab's session taken over
# by a newer tab? (Its wrapper must then exit without killing anything.)
stack_adopted_away() {
  local tmux_cmd="$1" s="$2" v
  "$tmux_cmd" has-session -t "$s" 2>/dev/null || return 1
  v="$("$tmux_cmd" show-environment -t "$s" WISP_DECK_ADOPTED_BY 2>/dev/null | cut -d= -f2-)"
  [ -n "$v" ]
}

# stack_owner_teardown <tmux_cmd> <cfg_root> <owner_session>
# Kill every session this tab owns except its own (the wrapper's existing
# cleanup lines handle that one) and except sessions adopted away since.
stack_owner_teardown() {
  local tmux_cmd="$1" cfg="$2" owner="$3"
  local s adopted_by root
  while IFS= read -r s; do
    [ -n "$s" ] || continue
    [ "$s" = "$owner" ] && continue
    "$tmux_cmd" has-session -t "$s" 2>/dev/null || continue
    adopted_by="$("$tmux_cmd" show-environment -t "$s" WISP_DECK_ADOPTED_BY 2>/dev/null | cut -d= -f2-)"
    if [ -n "$adopted_by" ] && [ "$adopted_by" != "$owner" ]; then
      continue
    fi
    root="$("$tmux_cmd" show-environment -t "$s" WISP_DECK_ATTENTION_ROOT 2>/dev/null | cut -d= -f2-)"
    cleanup_tmux_session "$s" "" "$tmux_cmd"
    command -v attention_cleanup >/dev/null 2>&1 && attention_cleanup "$root" 2>/dev/null
    command -v keep_awake_drop >/dev/null 2>&1 && keep_awake_drop "$cfg" "$s" 2>/dev/null
    stack_session_files_cleanup "$cfg" "$s"
  done < <(stack_list "$cfg" "$owner")
  rm -f "$cfg/stacks/$owner"
  return 0
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./test/bash/ -run 'TestStackAdopt|TestStackAdoptedAway|TestStackOwnerTeardown' -v` → PASS.

- [ ] **Step 5: shellcheck and commit**

```bash
shellcheck lib/session-stack.sh
git add lib/session-stack.sh test/bash/session_stack_test.go
git commit -m "feat(stack): adoption handoff and stack-aware owner teardown"
```

---

### Task 7: Orphan reaper (replaces server-wide exit-unattached)

**Files:**
- Modify: `lib/session-stack.sh`
- Test: `test/bash/session_stack_test.go`

**Interfaces:**
- Produces:
  - `stack_reap_orphans <tmux_cmd> <cfg_root>` → for each wisp session (`WISP_DECK=1` in env) with a numeric `WISP_DECK_OWNER_PID` whose process is dead: two-strike reap (first pass records the name in `<cfg_root>/stacks/.reap-marks`, second pass kills via `cleanup_tmux_session` + `stack_session_files_cleanup`). Sessions with a live owner get their mark cleared. Sessions without an owner-pid env (older wisp versions) are never touched.
  - `stack_reaper_watch <tmux_cmd> <cfg_root> [interval]` → loops `stack_reap_orphans` every `interval` (default 30) seconds while the tmux server is up. Backgrounded by wrapper.sh.

- [ ] **Step 1: Write the failing tests** (append)

```go
func TestStackReapOrphans_two_strikes_then_kill(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir()
	envCase := `
      "dev-app-111") all='WISP_DECK=1\nWISP_DECK_PATH=/tmp/app\nWISP_DECK_OWNER_PID=999999\n' ;;
`
	bin := mockTmux(t, dir, "100 dev-app-111\n", envCase) // pid 999999: guaranteed dead
	script := fmt.Sprintf(`
source lib/session-stack.sh
cleanup_tmux_session() { echo "CLEANUP:$1"; }
stack_reap_orphans %q %q
echo "AFTER-FIRST"
stack_reap_orphans %q %q
`, filepath.Join(bin, "tmux"), cfg, filepath.Join(bin, "tmux"), cfg)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	first := out[:strings.Index(out, "AFTER-FIRST")]
	assertNotContains(t, first, "CLEANUP:")            // strike one: marked only
	assertContains(t, out, "CLEANUP:dev-app-111")      // strike two: reaped
}

func TestStackReapOrphans_live_owner_never_reaped(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir()
	envCase := fmt.Sprintf(`
      "dev-app-111") all='WISP_DECK=1\nWISP_DECK_PATH=/tmp/app\nWISP_DECK_OWNER_PID=%s\n' ;;
`, "'\"$$\"'") // the test shell's own live pid
	bin := mockTmux(t, dir, "100 dev-app-111\n", envCase)
	script := fmt.Sprintf(`
source lib/session-stack.sh
cleanup_tmux_session() { echo "CLEANUP:$1"; }
stack_reap_orphans %q %q
stack_reap_orphans %q %q
`, filepath.Join(bin, "tmux"), cfg, filepath.Join(bin, "tmux"), cfg)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "CLEANUP:")
}

func TestStackReapOrphans_ignores_sessions_without_owner_pid(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir()
	bin := mockTmux(t, dir, "100 dev-app-111\n", stackEnvTwoApps) // no OWNER_PID in env
	script := fmt.Sprintf(`
source lib/session-stack.sh
cleanup_tmux_session() { echo "CLEANUP:$1"; }
stack_reap_orphans %q %q
stack_reap_orphans %q %q
`, filepath.Join(bin, "tmux"), cfg, filepath.Join(bin, "tmux"), cfg)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "CLEANUP:")
}
```

(In the live-owner test, resolve the env-case pid via the snippet itself: simplest is to write the canned env from inside the bash script with `$$`; if the Go-side quoting fights back, spawn a long `sleep 300 &` in the snippet, use `$!`, and `kill %1` at the end.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./test/bash/ -run TestStackReapOrphans -v` → FAIL.

- [ ] **Step 3: Implement** (append to `lib/session-stack.sh`)

```bash
# Orphan GC. The wrapper used to end its tmux chain with the SERVER-wide
# `set-option exit-unattached on` — fatal under stacking, where background
# stack sessions legitimately have no attached client. This reaper replaces
# it: every wisp session carries its owning wrapper's pid in session env
# (stamped at launch, restamped on adoption), and a session whose owner died
# without running its trap (SIGKILL, panic) is torn down. Two-strike so a
# launch racing between new-session and the env stamp is never hit.

# stack_reap_orphans <tmux_cmd> <cfg_root>
stack_reap_orphans() {
  local tmux_cmd="$1" cfg="$2"
  local marks="$cfg/stacks/.reap-marks" s env pid
  mkdir -p "$cfg/stacks" 2>/dev/null || return 0
  while IFS= read -r s; do
    [ -n "$s" ] || continue
    env="$("$tmux_cmd" show-environment -t "$s" 2>/dev/null)" || continue
    printf '%s\n' "$env" | grep -qx 'WISP_DECK=1' || continue
    pid="$(printf '%s\n' "$env" | sed -n 's/^WISP_DECK_OWNER_PID=//p' | head -n 1)"
    case "$pid" in '' | *[!0-9]*) continue ;; esac
    if kill -0 "$pid" 2>/dev/null; then
      if grep -qxF "$s" "$marks" 2>/dev/null; then
        grep -vxF "$s" "$marks" > "$marks.tmp.$$" 2>/dev/null || true
        mv "$marks.tmp.$$" "$marks" 2>/dev/null || rm -f "$marks.tmp.$$"
      fi
      continue
    fi
    if grep -qxF "$s" "$marks" 2>/dev/null; then
      cleanup_tmux_session "$s" "" "$tmux_cmd"
      stack_session_files_cleanup "$cfg" "$s"
      grep -vxF "$s" "$marks" > "$marks.tmp.$$" 2>/dev/null || true
      mv "$marks.tmp.$$" "$marks" 2>/dev/null || rm -f "$marks.tmp.$$"
    else
      printf '%s\n' "$s" >> "$marks"
    fi
  done < <("$tmux_cmd" list-sessions -F '#{session_name}' 2>/dev/null)
  return 0
}

# stack_reaper_watch <tmux_cmd> <cfg_root> [interval]
stack_reaper_watch() {
  local tmux_cmd="$1" cfg="$2" interval="${3:-30}"
  while "$tmux_cmd" has-session 2>/dev/null; do
    stack_reap_orphans "$tmux_cmd" "$cfg"
    sleep "$interval"
  done
  return 0
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./test/bash/ -run TestStackReapOrphans -v` → PASS.

- [ ] **Step 5: shellcheck and commit**

```bash
shellcheck lib/session-stack.sh
git add lib/session-stack.sh test/bash/session_stack_test.go
git commit -m "feat(stack): owner-pid orphan reaper"
```

---

### Task 8: Wrapper wiring — env stamp, exit-unattached removal, reaper, libs, binds

**Files:**
- Modify: `wrapper.sh`
- Test: `test/bash/session_stack_wrapper_test.go` (static guards, pattern of `opencode_availability_test.go`)

**Interfaces:**
- Consumes: every Task 1–7 function.
- Produces: wrapper behavior later tasks and the guards rely on. Keybindings: `prefix+n` next session, `prefix+p` previous, `prefix+X` close current session, `prefix+S` new stack session. (`n`/`p` default to window cycling — wisp sessions are single-window, so they're free.)

- [ ] **Step 1: Write the failing static guards**

Create `test/bash/session_stack_wrapper_test.go`:

```go
package bash_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func wrapperSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(repoPath(t, "wrapper.sh")) // use the repo-root helper pattern from helpers_test.go; if none exists, filepath.Join("..", "..", "wrapper.sh")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The server-wide exit-unattached option kills unattached stacked sessions.
// The orphan reaper replaced it; it must never come back to wrapper.sh.
// (lib/spare-tabs.sh's inner-server `set -g exit-unattached on` is fine.)
func TestWrapper_never_sets_server_exit_unattached(t *testing.T) {
	if strings.Contains(wrapperSource(t), "exit-unattached") {
		t.Fatal("wrapper.sh sets exit-unattached — incompatible with session stacking; the orphan reaper owns leak GC now")
	}
}

func TestWrapper_stamps_owner_pid_in_session_env(t *testing.T) {
	if !strings.Contains(wrapperSource(t), `WISP_DECK_OWNER_PID=$$`) {
		t.Fatal("wrapper.sh must stamp WISP_DECK_OWNER_PID into the session env (orphan reaper contract)")
	}
}

func TestWrapper_loads_session_stack_lib_and_starts_reaper(t *testing.T) {
	src := wrapperSource(t)
	if !strings.Contains(src, "session-stack") {
		t.Fatal("wrapper.sh must source lib/session-stack.sh")
	}
	if !strings.Contains(src, "stack_reaper_watch") {
		t.Fatal("wrapper.sh must background stack_reaper_watch")
	}
}

func TestWrapper_binds_use_session_format_not_baked_names(t *testing.T) {
	src := wrapperSource(t)
	for _, fn := range []string{"stack_cycle", "stack_close_current", "stack_request_new"} {
		re := regexp.MustCompile(fn + `[^\n]*#\{session_name\}`)
		if !re.MatchString(src) {
			t.Fatalf("wrapper.sh bind for %s must pass #{session_name} (bind-key is server-global; a baked name acts on the wrong session)", fn)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./test/bash/ -run TestWrapper_ -v`
Expected: FAIL on all four (wrapper still sets exit-unattached, no stamp, no lib, no binds).

- [ ] **Step 3: Modify wrapper.sh**

3a. Add `session-stack` to the lib list (line 73), after `session-restore`:

```bash
_gt_libs=(theme ai-tools projects process input tui install menu-tui project-actions ledger-hover tmux-session settings-json notification-setup keep-awake tab-title-watcher terminals/ghostty session-restore session-stack claude-configs claude-accounts claude-shared-settings auto-switch attention account-switch compact-view screenshot spare-tabs)
```

3b. Stamp the owner pid: in the `new-session` batch (line 626), append one more `-e` pair after `-e "WISP_DECK_SEQ=${_wd_launch_seq}"`:

```
-e "WISP_DECK_OWNER_PID=$$"
```

3c. Remove the server-wide GC: in the final chain (line 715-723), delete the trailing `\; set-option exit-unattached on` so the chain ends at `attach-session -t "$SESSION_NAME" 2>&3`. The orphan reaper (3d) replaces it.

3d. Start the reaper next to the snapshot heartbeat (after line 672):

```bash
# Orphan GC for the stacking world (replaces the server-wide exit-unattached
# teardown): reap sessions whose owning wrapper died without its trap.
stack_reaper_watch "$TMUX_CMD" "$SHARE_DIR" 30 >/dev/null 2>>"${WISP_DECK_ERROR_LOG:-/dev/null}" &
STACK_REAPER_PID=$!
```

And kill it in `cleanup()` next to the heartbeat kill:

```bash
[ -n "${STACK_REAPER_PID:-}" ] && kill "$STACK_REAPER_PID" 2>/dev/null || true
```

3e. Add the stack keybindings to the second batch (lines 715-721), before `run-shell -b "$_ledger_hover_setup"`. First build the commands above the batch, next to `_screenshot_bind` (~line 683):

```bash
# Session-stack binds. #{session_name} expands at KEY PRESS with the pressing
# client's session — never bake a session name into a server-global bind.
_stack_lib_src="source \"$_WRAPPER_DIR/lib/session-stack.sh\""
_stack_next_bind="bash -c '$_stack_lib_src && stack_cycle \"$TMUX_CMD\" \"#{session_name}\" next'"
_stack_prev_bind="bash -c '$_stack_lib_src && stack_cycle \"$TMUX_CMD\" \"#{session_name}\" prev'"
_stack_close_bind="bash -c 'source \"$_WRAPPER_DIR/lib/process.sh\"; source \"$_WRAPPER_DIR/lib/ledger-hover.sh\"; source \"$_WRAPPER_DIR/lib/spare-tabs.sh\"; source \"$_WRAPPER_DIR/lib/tmux-session.sh\"; source \"$_WRAPPER_DIR/lib/attention.sh\"; source \"$_WRAPPER_DIR/lib/keep-awake.sh\"; source \"$_WRAPPER_DIR/lib/theme.sh\"; $_stack_lib_src && stack_close_current \"$TMUX_CMD\" \"$SHARE_DIR\" \"#{session_name}\"'"
_stack_new_bind="bash -c 'source \"$_WRAPPER_DIR/lib/session-restore.sh\"; $_stack_lib_src && stack_request_new \"$TMUX_CMD\" \"$SHARE_DIR\" \"#{session_name}\" || \"$TMUX_CMD\" display-message \"Wisp: could not open a tab (Ghostty Accessibility permission?)\"'"
```

Then in the batch:

```bash
  bind-key n run-shell -b "$_stack_next_bind" \; \
  bind-key p run-shell -b "$_stack_prev_bind" \; \
  bind-key X run-shell -b "$_stack_close_bind" \; \
  bind-key S run-shell -b "$_stack_new_bind" \; \
```

- [ ] **Step 4: Run guards and the launch property tests**

Run: `go test ./test/bash/ -run 'TestWrapper_|TestLaunchCriticalPath|TestLaunchPostPickPath' -v`
Expected: PASS. (The stack additions are tmux-only and backgrounded; the property tests confirm no new blocking spawn.)

- [ ] **Step 5: shellcheck and commit**

```bash
shellcheck wrapper.sh
git add wrapper.sh test/bash/session_stack_wrapper_test.go
git commit -m "feat(stack): wrapper env stamp, reaper, stack keybinds; drop exit-unattached"
```

---

### Task 9: Wrapper wiring — request claim, adopt-list capture, adoption, cleanup rework

**Files:**
- Modify: `wrapper.sh`
- Test: `test/bash/session_stack_wrapper_test.go`

**Interfaces:**
- Consumes: `stack_request_claim`, `stack_sessions_for_project`, `stack_add`, `stack_adopt_all`, `stack_finalize_adoption`, `stack_repaint`, `stack_adopted_away`, `stack_owner_teardown`.

- [ ] **Step 1: Write the failing static guards** (append to `session_stack_wrapper_test.go`)

```go
func TestWrapper_claims_stack_request_before_picker(t *testing.T) {
	src := wrapperSource(t)
	claim := strings.Index(src, "stack_request_claim")
	picker := strings.Index(src, "select_project_interactive")
	if claim < 0 || picker < 0 || claim > picker {
		t.Fatal("wrapper.sh must claim a pending stack-request before falling through to the picker")
	}
}

func TestWrapper_consolidates_only_interactive_picks(t *testing.T) {
	src := wrapperSource(t)
	if !strings.Contains(src, "stack_sessions_for_project") {
		t.Fatal("wrapper.sh must capture the adopt list via stack_sessions_for_project")
	}
	// The capture must be gated on the consolidation flag so restored/arg
	// launches never adopt (restore chain = one tab per entry).
	idx := strings.Index(src, "stack_sessions_for_project")
	region := src[max(0, idx-400):idx]
	if !strings.Contains(region, "_gt_consolidate") || !strings.Contains(region, "RESTORE_MODE") {
		t.Fatal("adopt-list capture must be gated on _gt_consolidate=1 and RESTORE_MODE=0")
	}
}

func TestWrapper_cleanup_is_stack_aware(t *testing.T) {
	src := wrapperSource(t)
	cleanupStart := strings.Index(src, "cleanup() {")
	if cleanupStart < 0 {
		t.Fatal("cleanup() not found")
	}
	cleanupBody := src[cleanupStart : cleanupStart+strings.Index(src[cleanupStart:], "\n}")]
	for _, fn := range []string{"stack_adopted_away", "stack_owner_teardown"} {
		if !strings.Contains(cleanupBody, fn) {
			t.Fatalf("cleanup() must call %s", fn)
		}
	}
}

func TestWrapper_registers_own_session_and_backgrounds_finalizer(t *testing.T) {
	src := wrapperSource(t)
	if !regexp.MustCompile(`stack_add "\$SHARE_DIR" "\$SESSION_NAME" "\$SESSION_NAME"`).MatchString(src) {
		t.Fatal("wrapper.sh must register its own session in its stack file")
	}
	if !regexp.MustCompile(`stack_finalize_adoption[^\n]*&\s*$`).MatchString(src) {
		t.Fatal("stack_finalize_adoption must be backgrounded (it waits for the attach)")
	}
}
```

(Add a tiny local `func max(a, b int) int` if the toolchain predates builtins — repo Go version permitting, the builtin `max` works.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./test/bash/ -run TestWrapper_ -v` → the four new guards FAIL.

- [ ] **Step 3: Modify wrapper.sh**

3a. **Request claim** — in the interactive branch, immediately after the surplus-launch check (after line 210) and before the picker `printf` (line 213), insert:

```bash
  # A stack-request tab (prefix+S in an existing session) skips the picker and
  # lands straight in the requested project; the consolidation below then
  # adopts the requester's sessions and closes the requester's tab.
  _stack_req_dir=""
  _stack_req_dir="$(stack_request_claim "$SHARE_DIR")" || _stack_req_dir=""
  if [ -n "$_stack_req_dir" ]; then
    cd "$_stack_req_dir" || exit 1
    PROJECT_NAME="$(basename "$_stack_req_dir")"
    _gt_consolidate=1
    type stop_loading_screen &>/dev/null && stop_loading_screen
  else
    # ... existing picker code (printf tab title, stop_loading_screen, while true ... done) —
    # wrap the existing block from line 212 through 288 in this else-branch, one indent level in.
  fi
```

Also set the flag in the picker's select branch — inside `select-project|open-once)` (line 243), before `break`:

```bash
          _gt_consolidate=1
```

3b. **Adopt-list capture** — after the project is final (after line 290's closing `fi`, before `PROJECT_DIR="$(pwd)"`):

```bash
# Same-project sessions already open in other tabs. Captured pre-launch,
# adopted post-launch. Tmux-only — this sits on the post-pick critical path.
# Gated: interactive picks and stack requests only; a restored tab must never
# consolidate (the restore chain relies on one tab per queue entry).
_gt_adopt=()
if [ "${_gt_consolidate:-0}" = "1" ] && [ "$RESTORE_MODE" -eq 0 ]; then
  while IFS= read -r _gt_s; do
    [ -n "$_gt_s" ] && _gt_adopt+=("$_gt_s")
  done < <(stack_sessions_for_project "$TMUX_CMD" "$(pwd)")
  unset _gt_s
fi
```

3c. **Registration + adoption** — after `write_relaunch_context`/`export WISP_DECK_RELAUNCH_FILE` (line 661), insert:

```bash
# Register this tab's own session, then adopt same-project sessions from
# other tabs. The finalizer detaches the old tabs' clients only after OUR
# client is attached, so the project is never left with zero attached
# clients mid-handoff. Repaint is immediate: the bar must show the stack the
# moment the user can see the pane.
stack_add "$SHARE_DIR" "$SESSION_NAME" "$SESSION_NAME"
if [ "${#_gt_adopt[@]}" -gt 0 ]; then
  stack_adopt_all "$TMUX_CMD" "$SHARE_DIR" "$SESSION_NAME" "$$" "${_gt_adopt[@]}"
  stack_finalize_adoption "$TMUX_CMD" "$SESSION_NAME" "${_gt_adopt[@]}" \
    >/dev/null 2>>"${WISP_DECK_ERROR_LOG:-/dev/null}" &
  stack_repaint "$TMUX_CMD" "$SHARE_DIR" "$PROJECT_NAME" "$PROJECT_DIR"
fi
```

3d. **Cleanup rework** — replace the `cleanup()` body (lines 380-398) with:

```bash
cleanup() {
  stop_tab_title_watcher
  # A session that logged nothing leaves no file behind; one that hit a real
  # error keeps its log (pruned after a week by gt_mute_terminal_stderr).
  if [ -n "${_WD_ERROR_LOG:-}" ] && [ ! -s "$_WD_ERROR_LOG" ]; then
    rm -f "$_WD_ERROR_LOG"
  fi
  [ -n "${HEARTBEAT_PID:-}" ] && kill_tree "$HEARTBEAT_PID" TERM 2>/dev/null || true
  [ -n "${STACK_REAPER_PID:-}" ] && kill "$STACK_REAPER_PID" 2>/dev/null || true
  if stack_adopted_away "$TMUX_CMD" "$SESSION_NAME"; then
    # A newer tab adopted this tab's sessions: they live on. Kill only
    # wrapper-local jobs; the sessions, their SHARE_DIR files, their
    # attention roots and keep-awake holders now belong to the adopter
    # (its stack file covers every one of them).
    kill "$WATCHER_PID" 2>/dev/null || true
    rm -f "$SHARE_DIR/stacks/$SESSION_NAME"
    return 0
  fi
  # Release before anything else: whatever follows may fail, and leaving the
  # machine unable to sleep is worse than leaving a temp file behind.
  keep_awake_drop "${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck" "$SESSION_NAME" 2>/dev/null || true
  # Adopted sessions first (their attention roots are read from session env
  # before the kill), then this tab's own session via the existing path.
  stack_owner_teardown "$TMUX_CMD" "$SHARE_DIR" "$SESSION_NAME"
  cleanup_tmux_session "$SESSION_NAME" "$WATCHER_PID" "$TMUX_CMD"
  attention_cleanup "${WISP_DECK_ATTENTION_ROOT:-}" 2>/dev/null || true
  rm -f "$SHARE_DIR/spare-${SESSION_NAME}.conf"
  rm -f "$SHARE_DIR/proxy-${SESSION_NAME}.log"
  rm -f "$SHARE_DIR/proxy-account-${SESSION_NAME}"
  rm -f "$SHARE_DIR/relaunch-${SESSION_NAME}"
  rm -rf "$SHARE_DIR/spare-zdotdir-${SESSION_NAME}"
}
```

- [ ] **Step 4: Run the full guard + property set**

Run: `go test ./test/bash/ -run 'TestWrapper_|TestStack|TestLaunchCriticalPath|TestLaunchPostPickPath' -v`
Expected: PASS.

- [ ] **Step 5: shellcheck and commit**

```bash
shellcheck wrapper.sh
git add wrapper.sh test/bash/session_stack_wrapper_test.go
git commit -m "feat(stack): wrapper consolidation, request claim, stack-aware cleanup"
```

---

### Task 10: Critical-path static guard for the new lib

**Files:**
- Test: `test/bash/session_stack_test.go`

- [ ] **Step 1: Write the failing-or-passing guard** (append; pattern of `opencode_availability_test.go`)

```go
// session-stack code runs on the launch critical path (detection) and inside
// bound keys. It must stay tmux-and-filesystem only — no runtime boots, no
// network.
func TestSessionStackLib_spawns_no_expensive_commands(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "lib", "session-stack.sh"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, banned := range []string{"npx", "node ", "curl", "brew ", "npm "} {
		if strings.Contains(src, banned) {
			t.Fatalf("lib/session-stack.sh contains %q — expensive spawns are banned on the launch path", banned)
		}
	}
}
```

(Add `"os"` to the test file imports.)

- [ ] **Step 2: Run it**

Run: `go test ./test/bash/ -run TestSessionStackLib -v`
Expected: PASS immediately (guard test — it exists to catch future regressions; watch it fail by temporarily adding `# npx` … actually verify it CAN fail: insert `: npx` into the lib, run, see FAIL, revert).

- [ ] **Step 3: Commit**

```bash
git add test/bash/session_stack_test.go
git commit -m "test(stack): static guard against expensive spawns in session-stack lib"
```

---

### Task 11: Final verification and push

- [ ] **Step 1: shellcheck everything modified**

```bash
shellcheck lib/session-stack.sh wrapper.sh
```
Expected: clean.

- [ ] **Step 2: Full test suite**

```bash
./run-tests.sh
```
Expected: PASS. If a timing test flakes, re-run it in isolation before assuming breakage (known: worktree/compact-view wall-clock asserts flake under parallel load). Render failures with `go run ./cmd/ci-report` if anything is red.

- [ ] **Step 3: Manual smoke test (dev machine, live symlinks)**

The repo's lib/wrapper are symlinked live. Open a wisp tab on a project; open a second tab, pick the same project. Verify: you land in a fresh session, the bar shows two chips, `prefix+n` cycles, the old tab closed, `tmux ls` shows both sessions, closing the tab kills both (no zombies in `ps`). Verify `prefix+S` opens a tab that lands in the project (needs Ghostty Accessibility permission), and `prefix+X` closes just the current session.

- [ ] **Step 4: Push**

```bash
git pull --rebase
git push
git status   # MUST show "up to date with origin"
```

---

## Known v1 limitations (by design, from the spec)

- Adopted sessions lose their tab-title watcher (the adopting tab's title follows only its own session's attention state).
- Restore reopens stacked sessions as separate tabs (the snapshot heartbeat records each session independently; the restore chain is untouched).
- Bar chips are not clickable (outer tmux mouse stays off for the spare pane).
