# Session Restore After Reboot — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When the user opens Ghost Tab the first time after a reboot, silently reopen every tab that was open before the reboot, each resuming its AI conversation.

**Architecture:** A heartbeat loop in each running tab re-derives a snapshot of all alive Ghost Tab tmux sessions (identified by a `GHOST_TAB=1` session-env marker) into `~/.config/ghost-tab/last-session`. The tmux server dies at reboot, freezing the file. On the next interactive launch, a once-per-boot gate (keyed on `sysctl kern.boottime`) spawns one window per snapshot line via a per-terminal launch hook, in a non-interactive `wrapper.sh --restore` mode that launches the AI tool with its resume flag.

**Tech Stack:** bash (sourced `lib/*.sh` modules), tmux, Go test harness in `test/bash/` (calls bash via `os/exec`), shellcheck.

---

## File Structure

- **Create** `lib/session-restore.sh` — all restore logic as pure, dependency-injected functions: `current_boot_id`, `write_session_snapshot`, `launch_restore_window`, `maybe_restore_session`, `parse_restore_flag`.
- **Create** `test/bash/session_restore_test.go` — behavior tests for the module.
- **Modify** `lib/tmux-session.sh` — `build_ai_launch_cmd` honours `GHOST_TAB_RESUME=1`.
- **Modify** `lib/terminals/{ghostty,iterm2,kitty,wezterm}.sh` — add `terminal_launch_restore`.
- **Modify** `test/bash/terminal_{ghostty,iterm2,kitty,wezterm}_test.go` — tests for the new hook.
- **Modify** `test/bash/tmux_session_test.go` (create if absent) — resume-flag test.
- **Modify** `wrapper.sh` — source new modules, stamp session env, start heartbeat, gate restore, handle `--restore`.

All snapshot/marker files live under `${XDG_CONFIG_HOME:-$HOME/.config}/ghost-tab/`:
- `last-session` — `boot_id|project|path|tool|terminal` per alive session.
- `last-restore-boot` — boot id of the most recent restore.

---

## Task 1: `current_boot_id` + module skeleton

**Files:**
- Create: `lib/session-restore.sh`
- Test: `test/bash/session_restore_test.go`

- [ ] **Step 1: Write the failing test**

Create `test/bash/session_restore_test.go`:

```go
package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCurrentBootId_parses_sec_value(t *testing.T) {
	dir := t.TempDir()
	// macOS sysctl prints: { sec = 1700000000, usec = 123456 } Thu ...
	binDir := mockCommand(t, dir, "sysctl", `echo "{ sec = 1700000000, usec = 123456 } Thu Jan  1 00:00:00 2024"`)
	env := buildEnv(t, []string{binDir})
	out, code := runBashFunc(t, "lib/session-restore.sh", "current_boot_id", nil, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "1700000000" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "1700000000")
	}
}

func TestCurrentBootId_empty_when_sysctl_fails(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "sysctl", `exit 1`)
	env := buildEnv(t, []string{binDir})
	out, _ := runBashFunc(t, "lib/session-restore.sh", "current_boot_id", nil, env)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty, got %q", strings.TrimSpace(out))
	}
}

// referenced by later tasks; keep import of filepath/os used
var _ = filepath.Join
var _ = os.Environ
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./test/bash/... -run TestCurrentBootId -v`
Expected: FAIL — `lib/session-restore.sh` does not exist / function not found.

- [ ] **Step 3: Write minimal implementation**

Create `lib/session-restore.sh`:

```bash
#!/bin/bash
# Session restore — snapshot alive Ghost Tab tmux sessions and reopen them
# after a reboot. Depends on: terminals/adapter.sh (load_terminal_adapter).

# Print the current macOS boot id (the kern.boottime sec value).
# Stable for one uptime; changes on every reboot. Empty on failure.
current_boot_id() {
  local out
  out="$(sysctl -n kern.boottime 2>/dev/null)" || return 0
  echo "$out" | sed -n 's/.*sec = \([0-9][0-9]*\).*/\1/p'
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./test/bash/... -run TestCurrentBootId -v`
Expected: PASS (both cases).

- [ ] **Step 5: Commit**

```bash
git add lib/session-restore.sh test/bash/session_restore_test.go
git commit -m "feat: add current_boot_id for session restore"
```

---

## Task 2: `write_session_snapshot`

**Files:**
- Modify: `lib/session-restore.sh`
- Test: `test/bash/session_restore_test.go`

- [ ] **Step 1: Write the failing test**

Append to `test/bash/session_restore_test.go`:

```go
func TestWriteSessionSnapshot_writes_ghost_sessions_only(t *testing.T) {
	dir := t.TempDir()
	// Mock tmux: list two sessions; only dev-app-1 carries GHOST_TAB=1.
	tmuxBody := `
case "$1" in
  list-sessions) echo "dev-app-1"; echo "other-sess" ;;
  show-environment)
    if [ "$3" = "dev-app-1" ]; then
      printf 'GHOST_TAB=1\nGHOST_TAB_BOOT=111\nGHOST_TAB_PROJECT=app\nGHOST_TAB_PATH=/p/app\nGHOST_TAB_TOOL=claude\nGHOST_TAB_TERMINAL=ghostty\n'
    else
      printf 'SOMEVAR=1\n'
    fi ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	env := buildEnv(t, []string{binDir})
	snap := filepath.Join(dir, "last-session")
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "111|app|/p/app|claude|ghostty"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteSessionSnapshot_empty_when_no_ghost_sessions(t *testing.T) {
	dir := t.TempDir()
	tmuxBody := `
case "$1" in
  list-sessions) echo "other-sess" ;;
  show-environment) printf 'SOMEVAR=1\n' ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	env := buildEnv(t, []string{binDir})
	snap := filepath.Join(dir, "last-session")
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	data, _ := os.ReadFile(snap)
	if strings.TrimSpace(string(data)) != "" {
		t.Errorf("expected empty snapshot, got %q", string(data))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./test/bash/... -run TestWriteSessionSnapshot -v`
Expected: FAIL — `write_session_snapshot` not found.

- [ ] **Step 3: Write minimal implementation**

Append to `lib/session-restore.sh`:

```bash
# Re-derive the live snapshot from alive Ghost Tab tmux sessions.
# Usage: write_session_snapshot <tmux_cmd> <snapshot_file>
# A session is "ours" iff its session environment contains GHOST_TAB=1.
# Writes atomically (temp + mv). One line per session:
#   boot_id|project|path|tool|terminal
write_session_snapshot() {
  local tmux_cmd="$1" snap_file="$2"
  local tmp="${snap_file}.tmp.$$"
  : > "$tmp"
  local s env boot proj path tool term
  for s in $("$tmux_cmd" list-sessions -F '#{session_name}' 2>/dev/null); do
    env="$("$tmux_cmd" show-environment -t "$s" 2>/dev/null)" || continue
    echo "$env" | grep -q '^GHOST_TAB=1$' || continue
    boot="$(echo "$env" | sed -n 's/^GHOST_TAB_BOOT=//p')"
    proj="$(echo "$env" | sed -n 's/^GHOST_TAB_PROJECT=//p')"
    path="$(echo "$env" | sed -n 's/^GHOST_TAB_PATH=//p')"
    tool="$(echo "$env" | sed -n 's/^GHOST_TAB_TOOL=//p')"
    term="$(echo "$env" | sed -n 's/^GHOST_TAB_TERMINAL=//p')"
    echo "${boot}|${proj}|${path}|${tool}|${term}" >> "$tmp"
  done
  mv "$tmp" "$snap_file"
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./test/bash/... -run TestWriteSessionSnapshot -v`
Expected: PASS (both cases).

- [ ] **Step 5: Commit**

```bash
git add lib/session-restore.sh test/bash/session_restore_test.go
git commit -m "feat: snapshot alive ghost-tab tmux sessions"
```

---

## Task 3: `launch_restore_window` dispatcher

**Files:**
- Modify: `lib/session-restore.sh`
- Test: `test/bash/session_restore_test.go`

- [ ] **Step 1: Write the failing test**

Append to `test/bash/session_restore_test.go`:

```go
func TestLaunchRestoreWindow_loads_adapter_and_calls_hook(t *testing.T) {
	// Stub load_terminal_adapter + terminal_launch_restore so we can record args.
	root := projectRoot(t)
	mod := filepath.Join(root, "lib", "session-restore.sh")
	rec := filepath.Join(t.TempDir(), "rec")
	script := `
source ` + quote(mod) + `
load_terminal_adapter() { :; }                 # stub: pretend adapter loaded
terminal_launch_restore() { echo "$1|$2|$3" > ` + quote(rec) + `; }
launch_restore_window "ghostty" "/w/wrapper.sh" "/p/app" "claude"
`
	_, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("hook not called: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "/w/wrapper.sh|/p/app|claude"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
```

Add this helper near the top of `test/bash/session_restore_test.go` (after the imports):

```go
func quote(s string) string { return "\"" + s + "\"" }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./test/bash/... -run TestLaunchRestoreWindow -v`
Expected: FAIL — `launch_restore_window` not found.

- [ ] **Step 3: Write minimal implementation**

Append to `lib/session-restore.sh`:

```bash
# Load the adapter for <terminal> and open a window restoring <path>/<tool>.
# Usage: launch_restore_window <terminal> <wrapper_path> <project_path> <ai_tool>
# Relies on load_terminal_adapter + terminal_launch_restore being available
# (sourced by the caller, e.g. wrapper.sh).
launch_restore_window() {
  local terminal="$1" wrapper="$2" path="$3" tool="$4"
  load_terminal_adapter "$terminal" || return 1
  terminal_launch_restore "$wrapper" "$path" "$tool"
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./test/bash/... -run TestLaunchRestoreWindow -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add lib/session-restore.sh test/bash/session_restore_test.go
git commit -m "feat: add launch_restore_window dispatcher"
```

---

## Task 4: `maybe_restore_session` gate

**Files:**
- Modify: `lib/session-restore.sh`
- Test: `test/bash/session_restore_test.go`

- [ ] **Step 1: Write the failing test**

Append to `test/bash/session_restore_test.go`:

```go
// Helper: run maybe_restore_session with a stub launch_restore_window that
// records every call (one line per spawn) to recFile.
func runMaybeRestore(t *testing.T, configDir, curBoot, recFile string) (string, int) {
	t.Helper()
	root := projectRoot(t)
	mod := filepath.Join(root, "lib", "session-restore.sh")
	script := `
source ` + quote(mod) + `
launch_restore_window() { echo "$1|$2|$3|$4" >> ` + quote(recFile) + `; }
maybe_restore_session ` + quote(configDir) + ` ` + quote(curBoot) + ` "/w/wrapper.sh"
`
	return runBashSnippet(t, script, nil)
}

func TestMaybeRestore_spawns_prior_boot_lines_and_writes_marker(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty\n111|web|/p/web|codex|ghostty\n")
	rec := filepath.Join(dir, "rec")
	_, code := runMaybeRestore(t, dir, "222", rec)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("no spawns recorded: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "ghostty|/w/wrapper.sh|/p/app|claude\nghostty|/w/wrapper.sh|/p/web|codex"
	if got != want {
		t.Errorf("spawns:\n got %q\nwant %q", got, want)
	}
	marker, _ := os.ReadFile(filepath.Join(dir, "last-restore-boot"))
	if strings.TrimSpace(string(marker)) != "222" {
		t.Errorf("marker = %q, want 222", string(marker))
	}
}

func TestMaybeRestore_noop_when_already_restored_this_boot(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "last-session", "111|app|/p/app|claude|ghostty\n")
	writeTempFile(t, dir, "last-restore-boot", "222\n")
	rec := filepath.Join(dir, "rec")
	_, code := runMaybeRestore(t, dir, "222", rec)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(rec); err == nil {
		t.Error("expected no spawns when boot already restored")
	}
}

func TestMaybeRestore_noop_when_no_snapshot(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	_, code := runMaybeRestore(t, dir, "222", rec)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(rec); err == nil {
		t.Error("expected no spawns when snapshot missing")
	}
}

func TestMaybeRestore_skips_current_boot_lines(t *testing.T) {
	dir := t.TempDir()
	// All lines are from the current boot -> nothing to restore.
	writeTempFile(t, dir, "last-session", "222|app|/p/app|claude|ghostty\n")
	rec := filepath.Join(dir, "rec")
	_, code := runMaybeRestore(t, dir, "222", rec)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(rec); err == nil {
		t.Error("expected no spawns when all lines are current-boot")
	}
	// marker must NOT be written when nothing was restored
	if _, err := os.Stat(filepath.Join(dir, "last-restore-boot")); err == nil {
		t.Error("marker should not be written when nothing restored")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./test/bash/... -run TestMaybeRestore -v`
Expected: FAIL — `maybe_restore_session` not found.

- [ ] **Step 3: Write minimal implementation**

Append to `lib/session-restore.sh`:

```bash
# Once-per-boot restore gate. Call only on interactive launch, before the
# picker, and never in --restore mode.
# Usage: maybe_restore_session <config_dir> <current_boot_id> <wrapper_path>
# Spawns one window per snapshot line whose boot_id predates this boot, then
# stamps last-restore-boot. No-op if already restored this boot, snapshot
# missing, or no prior-boot lines exist.
maybe_restore_session() {
  local config_dir="$1" cur_boot="$2" wrapper="$3"
  local snap="$config_dir/last-session"
  local marker="$config_dir/last-restore-boot"

  [ -n "$cur_boot" ] || return 0
  [ -f "$snap" ] || return 0

  local last_boot=""
  [ -f "$marker" ] && last_boot="$(tr -d '[:space:]' < "$marker" 2>/dev/null)"
  [ "$cur_boot" = "$last_boot" ] && return 0

  local restored=0 b proj path tool term
  while IFS='|' read -r b proj path tool term; do
    [ -n "$b" ] || continue
    [ "$b" = "$cur_boot" ] && continue
    if [ "$restored" -eq 0 ]; then
      echo "$cur_boot" > "$marker"
      restored=1
    fi
    launch_restore_window "$term" "$wrapper" "$path" "$tool"
  done < "$snap"
  return 0
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./test/bash/... -run TestMaybeRestore -v`
Expected: PASS (all four cases).

- [ ] **Step 5: Commit**

```bash
git add lib/session-restore.sh test/bash/session_restore_test.go
git commit -m "feat: add once-per-boot restore gate"
```

---

## Task 5: `parse_restore_flag`

**Files:**
- Modify: `lib/session-restore.sh`
- Test: `test/bash/session_restore_test.go`

- [ ] **Step 1: Write the failing test**

Append to `test/bash/session_restore_test.go`:

```go
func TestParseRestoreFlag_emits_path_and_tool(t *testing.T) {
	out, code := runBashFunc(t, "lib/session-restore.sh", "parse_restore_flag",
		[]string{"--restore", "/p/app", "claude"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "/p/app|claude" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "/p/app|claude")
	}
}

func TestParseRestoreFlag_empty_when_not_restore(t *testing.T) {
	out, code := runBashFunc(t, "lib/session-restore.sh", "parse_restore_flag",
		[]string{"/some/dir"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty, got %q", strings.TrimSpace(out))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./test/bash/... -run TestParseRestoreFlag -v`
Expected: FAIL — `parse_restore_flag` not found.

- [ ] **Step 3: Write minimal implementation**

Append to `lib/session-restore.sh`:

```bash
# If args start with --restore, echo "path|tool"; otherwise echo nothing.
# Usage: parse_restore_flag "$@"
parse_restore_flag() {
  if [ "$1" = "--restore" ]; then
    echo "$2|$3"
  fi
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./test/bash/... -run TestParseRestoreFlag -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add lib/session-restore.sh test/bash/session_restore_test.go
git commit -m "feat: add parse_restore_flag helper"
```

---

## Task 6: Resume-aware `build_ai_launch_cmd`

**Files:**
- Modify: `lib/tmux-session.sh:7-30`
- Test: `test/bash/tmux_session_test.go` (create if absent)

- [ ] **Step 1: Write the failing test**

Create (or append to) `test/bash/tmux_session_test.go`:

```go
package bash_test

import (
	"strings"
	"testing"
)

func aiCmd(t *testing.T, tool string, resume bool) string {
	t.Helper()
	var env []string
	if resume {
		env = buildEnv(t, nil, "GHOST_TAB_RESUME=1")
	}
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{tool, "claude", "codex", "copilot", "npx opencode-ai@latest", "/p/app"}, env)
	assertExitCode(t, code, 0)
	return strings.TrimSpace(out)
}

func TestBuildAiLaunchCmd_resume_flags(t *testing.T) {
	cases := []struct {
		tool string
		want string
	}{
		{"claude", "claude -c"},
		{"codex", "codex resume --last"},
		{"copilot", "copilot --continue"},
		{"opencode", "npx opencode-ai@latest --continue"},
	}
	for _, c := range cases {
		if got := aiCmd(t, c.tool, true); got != c.want {
			t.Errorf("resume %s: got %q, want %q", c.tool, got, c.want)
		}
	}
}

func TestBuildAiLaunchCmd_normal_unaffected(t *testing.T) {
	if got := aiCmd(t, "codex", false); got != `codex --cd "/p/app"` {
		t.Errorf("normal codex: got %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./test/bash/... -run TestBuildAiLaunchCmd -v`
Expected: FAIL — resume cases produce the non-resume command.

- [ ] **Step 3: Write minimal implementation**

Replace the body of `build_ai_launch_cmd` in `lib/tmux-session.sh` (lines 7-30) with:

```bash
build_ai_launch_cmd() {
  local tool="$1" claude_cmd="$2" codex_cmd="$3" copilot_cmd="$4" opencode_cmd="$5"
  shift 5
  local extra="$*"

  # Resume mode: relaunch into the most recent (cwd-scoped) conversation.
  if [ "${GHOST_TAB_RESUME:-0}" = "1" ]; then
    case "$tool" in
      codex)    echo "$codex_cmd resume --last" ;;
      copilot)  echo "$copilot_cmd --continue" ;;
      opencode) echo "$opencode_cmd --continue" ;;
      *)        echo "$claude_cmd -c" ;;
    esac
    return 0
  fi

  case "$tool" in
    codex)
      echo "$codex_cmd --cd \"$extra\""
      ;;
    copilot)
      echo "$copilot_cmd"
      ;;
    opencode)
      echo "$opencode_cmd \"$extra\""
      ;;
    *)
      if [ -n "$extra" ]; then
        echo "$claude_cmd $extra"
      else
        echo "$claude_cmd"
      fi
      ;;
  esac
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./test/bash/... -run TestBuildAiLaunchCmd -v`
Expected: PASS (resume + normal).

- [ ] **Step 5: Commit**

```bash
git add lib/tmux-session.sh test/bash/tmux_session_test.go
git commit -m "feat: resume-aware AI launch command"
```

---

## Task 7: `terminal_launch_restore` — Ghostty

**Files:**
- Modify: `lib/terminals/ghostty.sh`
- Test: `test/bash/terminal_ghostty_test.go`

- [ ] **Step 1: Write the failing test**

Append to `test/bash/terminal_ghostty_test.go`:

```go
func TestGhosttyAdapter_launch_restore(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "open", `echo "$@" > `+fmt.Sprintf("%q", rec))
	env := buildEnv(t, []string{binDir})
	snippet := ghosttyAdapterSnippet(t,
		`terminal_launch_restore "/w/wrapper.sh" "/p/app" "claude"`)
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("open not invoked: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "-na Ghostty --args -e /bin/bash -l /w/wrapper.sh --restore /p/app claude"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./test/bash/... -run TestGhosttyAdapter_launch_restore -v`
Expected: FAIL — `terminal_launch_restore` not found.

- [ ] **Step 3: Write minimal implementation**

Append to `lib/terminals/ghostty.sh` (before the final newline, after `terminal_cleanup_config`):

```bash
# Open a new Ghostty window running the wrapper in restore mode.
# Args: wrapper_path project_path ai_tool
terminal_launch_restore() {
  local wrapper="$1" path="$2" tool="$3"
  open -na Ghostty --args -e /bin/bash -l "$wrapper" --restore "$path" "$tool"
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./test/bash/... -run TestGhosttyAdapter_launch_restore -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add lib/terminals/ghostty.sh test/bash/terminal_ghostty_test.go
git commit -m "feat: ghostty launch_restore hook"
```

---

## Task 8: `terminal_launch_restore` — kitty

**Files:**
- Modify: `lib/terminals/kitty.sh`
- Test: `test/bash/terminal_kitty_test.go`

- [ ] **Step 1: Write the failing test**

Append to `test/bash/terminal_kitty_test.go` (use that file's existing adapter-snippet helper; it mirrors `ghosttyAdapterSnippet` but sources `kitty.sh` — confirm the helper name by reading the file's top, e.g. `kittyAdapterSnippet`):

```go
func TestKittyAdapter_launch_restore(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "open", `echo "$@" > `+fmt.Sprintf("%q", rec))
	env := buildEnv(t, []string{binDir})
	snippet := kittyAdapterSnippet(t,
		`terminal_launch_restore "/w/wrapper.sh" "/p/app" "claude"`)
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("open not invoked: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "-na kitty --args /bin/bash -l /w/wrapper.sh --restore /p/app claude"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
```

If `kittyAdapterSnippet` does not exist, add it mirroring `ghosttyAdapterSnippet` but with `adapterPath := filepath.Join(root, "lib", "terminals", "kitty.sh")`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./test/bash/... -run TestKittyAdapter_launch_restore -v`
Expected: FAIL — `terminal_launch_restore` not found.

- [ ] **Step 3: Write minimal implementation**

Append to `lib/terminals/kitty.sh`:

```bash
# Open a new kitty window running the wrapper in restore mode.
# Args: wrapper_path project_path ai_tool
terminal_launch_restore() {
  local wrapper="$1" path="$2" tool="$3"
  open -na kitty --args /bin/bash -l "$wrapper" --restore "$path" "$tool"
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./test/bash/... -run TestKittyAdapter_launch_restore -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add lib/terminals/kitty.sh test/bash/terminal_kitty_test.go
git commit -m "feat: kitty launch_restore hook"
```

---

## Task 9: `terminal_launch_restore` — WezTerm

**Files:**
- Modify: `lib/terminals/wezterm.sh`
- Test: `test/bash/terminal_wezterm_test.go`

- [ ] **Step 1: Write the failing test**

Append to `test/bash/terminal_wezterm_test.go` (use the file's adapter-snippet helper, likely `weztermAdapterSnippet`; if absent, add it mirroring `ghosttyAdapterSnippet` with `wezterm.sh`):

```go
func TestWeztermAdapter_launch_restore(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "open", `echo "$@" > `+fmt.Sprintf("%q", rec))
	env := buildEnv(t, []string{binDir})
	snippet := weztermAdapterSnippet(t,
		`terminal_launch_restore "/w/wrapper.sh" "/p/app" "claude"`)
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("open not invoked: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "-na WezTerm --args start -- /bin/bash -l /w/wrapper.sh --restore /p/app claude"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./test/bash/... -run TestWeztermAdapter_launch_restore -v`
Expected: FAIL — `terminal_launch_restore` not found.

- [ ] **Step 3: Write minimal implementation**

Append to `lib/terminals/wezterm.sh`:

```bash
# Open a new WezTerm window running the wrapper in restore mode.
# Args: wrapper_path project_path ai_tool
terminal_launch_restore() {
  local wrapper="$1" path="$2" tool="$3"
  open -na WezTerm --args start -- /bin/bash -l "$wrapper" --restore "$path" "$tool"
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./test/bash/... -run TestWeztermAdapter_launch_restore -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add lib/terminals/wezterm.sh test/bash/terminal_wezterm_test.go
git commit -m "feat: wezterm launch_restore hook"
```

---

## Task 10: `terminal_launch_restore` — iTerm2

**Files:**
- Modify: `lib/terminals/iterm2.sh`
- Test: `test/bash/terminal_iterm2_test.go`

- [ ] **Step 1: Write the failing test**

Append to `test/bash/terminal_iterm2_test.go` (use the file's adapter-snippet helper, likely `iterm2AdapterSnippet`; if absent, add it mirroring `ghosttyAdapterSnippet` with `iterm2.sh`):

```go
func TestIterm2Adapter_launch_restore(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "osascript", `printf '%s\n' "$*" > `+fmt.Sprintf("%q", rec))
	env := buildEnv(t, []string{binDir})
	snippet := iterm2AdapterSnippet(t,
		`terminal_launch_restore "/w/wrapper.sh" "/p/app" "claude"`)
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("osascript not invoked: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := `-e tell application "iTerm" to create window with default profile command "/bin/bash -l '/w/wrapper.sh' --restore '/p/app' 'claude'"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./test/bash/... -run TestIterm2Adapter_launch_restore -v`
Expected: FAIL — `terminal_launch_restore` not found.

- [ ] **Step 3: Write minimal implementation**

Append to `lib/terminals/iterm2.sh`:

```bash
# Open a new iTerm2 window running the wrapper in restore mode.
# Args: wrapper_path project_path ai_tool
# Note: paths containing a single quote are not supported (accepted limitation).
terminal_launch_restore() {
  local wrapper="$1" path="$2" tool="$3"
  osascript -e "tell application \"iTerm\" to create window with default profile command \"/bin/bash -l '$wrapper' --restore '$path' '$tool'\""
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./test/bash/... -run TestIterm2Adapter_launch_restore -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add lib/terminals/iterm2.sh test/bash/terminal_iterm2_test.go
git commit -m "feat: iterm2 launch_restore hook"
```

---

## Task 11: Wire it all into `wrapper.sh`

This task is integration glue (the testable logic was unit-tested in Tasks 1-10).
Verify via shellcheck + a manual smoke test.

**Files:**
- Modify: `wrapper.sh`

- [ ] **Step 1: Source the new modules**

In `wrapper.sh`, change the `_gt_libs` array (line 42) to add `session-restore` and the terminal-adapter loader so `load_terminal_adapter`/registry are available:

```bash
_gt_libs=(ai-tools projects process input tui menu-tui project-actions tmux-session settings-json notification-setup tab-title-watcher terminals/registry terminals/adapter session-restore)
```

- [ ] **Step 2: Detect restore mode + compute boot id (before the project-selection block, around line 86)**

Insert immediately after the libs loop (after line 54, before `TMUX_CMD=` is fine; place it just before the "Select working directory" block at line 86):

```bash
# Boot id (stable per uptime) for once-per-boot restore.
GHOST_TAB_BOOT_ID="$(current_boot_id)"

# Restore mode: wrapper.sh --restore <project_path> <ai_tool>
RESTORE_MODE=0
_restore_parsed="$(parse_restore_flag "$@")"
if [ -n "$_restore_parsed" ]; then
  RESTORE_MODE=1
  RESTORE_PATH="${_restore_parsed%%|*}"
  RESTORE_TOOL="${_restore_parsed##*|}"
fi
```

- [ ] **Step 3: Branch the working-directory selection**

Replace the working-directory selection block (lines 86-128, the `if [ -n "$1" ] ... fi`) so restore mode skips the picker, and interactive mode first triggers restore. New block:

```bash
# Select working directory
if [ "$RESTORE_MODE" -eq 1 ]; then
  cd "$RESTORE_PATH" || exit 1
  PROJECT_NAME="$(basename "$RESTORE_PATH")"
  SELECTED_AI_TOOL="$RESTORE_TOOL"
elif [ -n "$1" ] && [ -d "$1" ]; then
  cd "$1" || exit 1
  shift
elif [ -z "$1" ]; then
  # First interactive launch after a reboot: reopen previous tabs.
  _gt_terminal_pref="$(load_terminal_preference "$SHARE_DIR/terminal")"
  maybe_restore_session "$SHARE_DIR" "$GHOST_TAB_BOOT_ID" "$0"

  # Use TUI for project selection
  printf '\033]0;👻 Ghost Tab\007'
  type stop_loading_screen &>/dev/null && stop_loading_screen

  while true; do
    if select_project_interactive "$PROJECTS_FILE"; then
      if [[ -n "${_selected_ai_tool:-}" ]]; then
        SELECTED_AI_TOOL="$_selected_ai_tool"
      fi
      # shellcheck disable=SC2154
      case "$_selected_project_action" in
        select-project|open-once)
          PROJECT_NAME="$_selected_project_name"
          # shellcheck disable=SC2154
          cd "$_selected_project_path" || exit 1
          break
          ;;
        plain-terminal)
          exec "$SHELL"
          ;;
        add-worktree)
          continue
          ;;
        *)
          continue
          ;;
      esac
    else
      exit 0
    fi
  done
fi
```

Note: `_gt_terminal_pref` is unused for now (the snapshot stores each tab's own terminal); it is fine to drop it. Remove that line if shellcheck flags SC2034.

- [ ] **Step 4: Resume flag for restore mode (before building the AI command, around line 201)**

Immediately before the `# Build the AI tool launch command` block (line 201), add:

```bash
if [ "$RESTORE_MODE" -eq 1 ]; then
  export GHOST_TAB_RESUME=1
fi
```

- [ ] **Step 5: Stamp session env + start heartbeat (around line 211, before the tmux new-session call)**

Read the current terminal preference and define the snapshot path; start the heartbeat loop. Insert after the `start_tab_title_watcher ...` call (line 212):

```bash
# Session-restore snapshot: stamp metadata into the tmux session env via -e
# flags on new-session (below), and run a heartbeat that re-derives the
# snapshot from all alive Ghost Tab sessions.
GHOST_TAB_TERMINAL="$(load_terminal_preference "$SHARE_DIR/terminal")"
GHOST_TAB_SNAPSHOT="$SHARE_DIR/last-session"
(
  while true; do
    write_session_snapshot "$TMUX_CMD" "$GHOST_TAB_SNAPSHOT"
    sleep 10
  done
) &
HEARTBEAT_PID=$!
```

- [ ] **Step 6: Add `-e` metadata flags to the `new-session` call (line 214)**

Edit the `"$TMUX_CMD" new-session ...` line to add the Ghost Tab env flags right after the existing `-e` flags (before `-c "$PROJECT_DIR"`):

```bash
"$TMUX_CMD" new-session -s "$SESSION_NAME" -e "PATH=$PATH" -e "GHOST_TAB_BASELINE_FILE=$GHOST_TAB_BASELINE_FILE" -e "GHOST_TAB_MARKER_FILE=$GHOST_TAB_MARKER_FILE" -e "GHOST_TAB=1" -e "GHOST_TAB_BOOT=$GHOST_TAB_BOOT_ID" -e "GHOST_TAB_PROJECT=$PROJECT_NAME" -e "GHOST_TAB_PATH=$PROJECT_DIR" -e "GHOST_TAB_TOOL=$SELECTED_AI_TOOL" -e "GHOST_TAB_TERMINAL=$GHOST_TAB_TERMINAL" -c "$PROJECT_DIR" \
```

- [ ] **Step 7: Kill the heartbeat in `cleanup()` (do NOT touch the snapshot file)**

In `cleanup()` (line 177), add a kill for the heartbeat near the top (after `stop_tab_title_watcher`):

```bash
  [ -n "${HEARTBEAT_PID:-}" ] && kill "$HEARTBEAT_PID" 2>/dev/null || true
```

Do not delete `$GHOST_TAB_SNAPSHOT` — leaving it is what keeps the file frozen across a reboot. Surviving tabs' heartbeats re-derive it without this session.

- [ ] **Step 8: shellcheck**

Run: `shellcheck wrapper.sh lib/session-restore.sh lib/terminals/ghostty.sh lib/terminals/kitty.sh lib/terminals/wezterm.sh lib/terminals/iterm2.sh lib/tmux-session.sh`
Expected: no warnings. Fix any (e.g. quote variables, `# shellcheck disable=SC2034` with a reason for an intentionally-unused var).

- [ ] **Step 9: Manual smoke test**

```bash
# Sanity: wrapper parses --restore without launching the picker.
# (Run in a scratch terminal; Ctrl-C after the tmux session appears.)
bash wrapper.sh --restore "$PWD" claude
```
Expected: a tmux session opens directly in `$PWD` with the AI pane launching
`claude -c` (no project picker). Confirm `~/.config/ghost-tab/last-session`
gains a `…|<project>|<path>|claude|<terminal>` line within ~10s
(`cat ~/.config/ghost-tab/last-session`).

- [ ] **Step 10: Commit**

```bash
git add wrapper.sh
git commit -m "feat: wire session restore into wrapper"
```

---

## Task 12: Final verification

- [ ] **Step 1: Full shellcheck**

Run: `find lib bin -name '*.sh' -exec shellcheck {} + && shellcheck wrapper.sh`
Expected: clean.

- [ ] **Step 2: Full test suite**

Run: `./run-tests.sh`
Expected: all tests pass.

- [ ] **Step 3: Push**

```bash
git pull --rebase
git push
git status   # MUST show "up to date with origin"
```

---

## Self-Review notes

- **Spec coverage:** boot id (T1), live snapshot + GHOST_TAB marker (T2, T11 step 6), launch dispatcher (T3), once-per-boot gate + opened-window-stays-picker (T4, T11 step 3), restore mode (T5, T11 steps 2-4), resume flags per tool (T6), per-terminal launch hooks all four (T7-T10), heartbeat that freezes at reboot + cleanup not touching snapshot (T11 steps 5,7). All spec sections mapped.
- **Documented edge** (closing last tab then rebooting reopens it): inherent to the frozen-snapshot model; no task needed, behavior is acceptable per spec.
- **Type/name consistency:** `terminal_launch_restore(wrapper, path, tool)`, `launch_restore_window(terminal, wrapper, path, tool)`, `maybe_restore_session(config_dir, boot_id, wrapper)`, `write_session_snapshot(tmux_cmd, snap_file)`, env keys `GHOST_TAB`/`GHOST_TAB_BOOT`/`GHOST_TAB_PROJECT`/`GHOST_TAB_PATH`/`GHOST_TAB_TOOL`/`GHOST_TAB_TERMINAL`, and `GHOST_TAB_RESUME=1` are used identically across all tasks.
- **Real-tool caveat:** `open -na … --args -e` (Ghostty), `codex resume --last`, `copilot --continue` semantics are pinned by mocks here; verify against installed tool versions during T11 step 9 smoke test and adjust the single adapter/command line if a tool differs.
```
