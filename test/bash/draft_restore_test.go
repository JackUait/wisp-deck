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
