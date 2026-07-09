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
    # The real claude pads the empty prompt with a NO-BREAK space (U+00A0),
    # not an ASCII space — the ready match must accept both.
    printf 'some banner\n❯\302\240\nstatusline\n'
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

// The mock tmux records each paste's exact bytes: load-buffer receives the
// segment on stdin (logged with a PASTE: prefix and % for embedded
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
	// Real newline inside bash single quotes (Go %q would deliver a literal \n).
	draft := "line one\nline two [Image #2] tail [Pasted text #3 +300 lines] [Image #9]"
	env := buildEnv(t, []string{bin})
	_, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		"replay_ai_draft tmux %%1 '%s' %q %q", draft, filepath.Join(dir, "root"), sid)), env)
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
if [ "$1" = "claude-account-switch" ] && [ "$2" = "--help" ]; then
  echo "claude-account-switch --result-file"; exit 0
fi
out=""
prev=""
for a in "$@"; do
  [ "$prev" = "--result-file" ] && out="$a"
  prev="$a"
done
[ -n "$out" ] && printf 'work\n' > "$out"`)
	respawned := filepath.Join(dir, "respawned")
	tmuxBin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
case "$1" in
load-buffer) printf 'PASTE:%%s\n' "$(cat)" >> %q; exit 0 ;;
*) printf '%%s\n' "$*" >> %q ;;
esac
if [ "$1" = "send-keys" ] && [ "$4" = "Escape" ] && [ "$5" = "Escape" ]; then
  printf '%%s\n' '{"display":"draft [Image #1]","pastedContents":{},"project":"/proj"}' >> %q
fi
if [ "$1" = "list-panes" ]; then printf '%%s\n' "%%1 1"; fi
if [ "$1" = "respawn-pane" ]; then touch %q; fi
# Before the respawn the OLD claude shows the draft in its input line; after
# it the NEW claude shows the empty ready prompt the replay waits for.
if [ "$1" = "capture-pane" ]; then
  if [ -e %q ]; then printf '%%s\n' "❯ "; else printf '%%s\n' "❯ draft [Image #1]"; fi
fi
if [ "$1" = "show-environment" ]; then
  case "$*" in
  *WISP_DECK_CLAUDE_SESSION*) echo "WISP_DECK_CLAUDE_SESSION=%s" ;;
  *WISP_DECK_CLAUDE_ACCOUNT*) echo "WISP_DECK_CLAUDE_ACCOUNT=" ;;
  esac
fi
if [ "$1" = "display-popup" ]; then
  # -E runs the popup command in a shell: this triggers the result-file write.
  eval "${@: -1}" >/dev/null 2>&1 || true
fi`, rec, rec, hist, respawned, respawned, sid))
	accountsDir := filepath.Join(dir, "claude-accounts")
	if err := os.MkdirAll(filepath.Join(accountsDir, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	relaunch := writeTempFile(t, dir, "relaunch", strings.Join([]string{
		"tool=claude", "tool_cmd=claude",
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
		if strings.Contains(string(logOut), "PASTE:"+filepath.Join(home, ".claude", "image-cache", sid, "1.png")) {
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
if [ "$1" = "claude-account-switch" ] && [ "$2" = "--help" ]; then
  echo "claude-account-switch --result-file"; exit 0
fi
out=""
prev=""
for a in "$@"; do
  [ "$prev" = "--result-file" ] && out="$a"
  prev="$a"
done
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
		"tool=opencode", "tool_cmd=npx opencode-ai@latest",
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

// history.jsonl is shared by every live claude session, so another session's
// entry can land AFTER our stash before we read the tail. The stash must pick
// the newest appended entry whose project matches THIS pane's project dir —
// never a foreign session's prompt (live e2e nearly pasted another session's
// input into the relaunched pane).
func TestStashAIDraft_skips_foreign_project_entries(t *testing.T) {
	dir := t.TempDir()
	hist := writeTempFile(t, dir, "history.jsonl", "")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "send-keys" ] && [ "$4" = "Escape" ] && [ "$5" = "Escape" ]; then
  printf '%%s\n' '{"display":"our draft","pastedContents":{},"project":"/proj"}' >> %q
  printf '%%s\n' '{"display":"foreign secret prompt","pastedContents":{},"project":"/elsewhere"}' >> %q
fi`, hist, hist))
	env := buildEnv(t, []string{bin})
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("stash_ai_draft tmux %%1 %q /proj", hist)), env)
	assertExitCode(t, code, 0)
	if out != "our draft" {
		t.Fatalf("expected our project's entry, got %q", out)
	}
}

// Growth made only of foreign-project entries is NOT our stash (our Esc-Esc
// appended nothing — the input was empty): rc 1, nothing to replay.
func TestStashAIDraft_only_foreign_growth_is_no_draft(t *testing.T) {
	dir := t.TempDir()
	hist := writeTempFile(t, dir, "history.jsonl", "")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "send-keys" ] && [ "$4" = "Escape" ] && [ "$5" = "Escape" ]; then
  printf '%%s\n' '{"display":"foreign secret prompt","pastedContents":{},"project":"/elsewhere"}' >> %q
fi`, hist))
	env := buildEnv(t, []string{bin})
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("stash_ai_draft tmux %%1 %q /proj", hist)), env)
	if code == 0 {
		t.Fatalf("expected rc 1 when only foreign entries landed, got 0: %q", out)
	}
}

// An EMPTY input box means there is nothing to stash — the switch must not
// pay the Esc-Esc history-poll timeout (~1.7s of every empty-input switch).
// The pane frame is captured first: when the input line (the LAST ❯-prefixed
// row — the input box renders below the transcript) is empty, stash_ai_draft
// returns "no draft" immediately, sending NO keys at all.
func TestStashAIDraft_empty_input_fast_fails_without_keys(t *testing.T) {
	dir := t.TempDir()
	hist := writeTempFile(t, dir, "history.jsonl", "")
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
printf '%%s\n' "$*" >> %q
if [ "$1" = "capture-pane" ]; then printf 'banner\n\xe2\x9d\xaf\302\240\nstatusline\n'; fi`, rec))
	env := buildEnv(t, []string{bin})
	start := time.Now()
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("stash_ai_draft tmux %%1 %q", hist)), env)
	if code == 0 {
		t.Fatalf("expected nonzero exit for an empty input box, got 0: %q", out)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected no output, got %q", out)
	}
	// Pre-fix this path took ~6s (the full no-growth poll); post-fix it is one
	// capture-pane (~0.3s incl. bash startup). 3s stays firmly between the two
	// even when the suite runs under parallel load.
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("empty input did not fast-fail (took %v — paid the history poll?)", elapsed)
	}
	logOut, _ := os.ReadFile(rec)
	assertNotContains(t, string(logOut), "Escape")
}

// A stray bare "❯" line in the TRANSCRIPT above a non-empty input line must
// not be mistaken for an empty input box: only the LAST ❯-prefixed row is the
// input line. The draft below it must still be stashed.
func TestStashAIDraft_stray_prompt_above_draft_still_stashes(t *testing.T) {
	dir := t.TempDir()
	hist := writeTempFile(t, dir, "history.jsonl", "")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "capture-pane" ]; then
  printf 'transcript\n\xe2\x9d\xaf\n\xe2\x9d\xaf my draft\n'
fi
if [ "$1" = "send-keys" ] && [ "$4" = "Escape" ] && [ "$5" = "Escape" ]; then
  printf '%%s\n' '{"display":"my draft","pastedContents":{}}' >> %q
fi`, hist))
	env := buildEnv(t, []string{bin})
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("stash_ai_draft tmux %%1 %q", hist)), env)
	assertExitCode(t, code, 0)
	if out != "my draft" {
		t.Fatalf("expected the draft to be stashed despite a stray bare ❯ above, got %q", out)
	}
}

// A dialog frame ("❯ 2. No, exit" is the last ❯ row, non-empty) must NOT
// fast-skip — it falls through to the Esc-Esc path exactly as today.
func TestStashAIDraft_dialog_frame_falls_through_to_esc_path(t *testing.T) {
	dir := t.TempDir()
	hist := writeTempFile(t, dir, "history.jsonl", "")
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
printf '%%s\n' "$*" >> %q
if [ "$1" = "capture-pane" ]; then printf 'Do you trust this folder?\n\xe2\x9d\xaf 2. No, exit\n'; fi`, rec))
	env := buildEnv(t, []string{bin})
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("stash_ai_draft tmux %%1 %q", hist)), env)
	if code == 0 {
		t.Fatal("expected rc 1 (no history growth) on a dialog frame")
	}
	logOut, _ := os.ReadFile(rec)
	assertContains(t, string(logOut), "Escape")
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
