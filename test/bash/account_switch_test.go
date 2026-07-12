package bash_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCompactViewLoadsAccountDeps_underZsh reproduces the pane's real runtime: the
// compact-view ledger is launched via `zsh -c 'source compact-view.sh && ...'`
// (the user's $SHELL), where BASH_SOURCE is empty. compact-view.sh must still load
// its account-switch deps so the switcher pill can render. wrapper.sh exports
// WISP_DECK_LIB_DIR for the pane; the script uses it to locate its siblings.
func TestCompactViewLoadsAccountDeps_underZsh(t *testing.T) {
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not available")
	}
	root := projectRoot(t)
	lib := filepath.Join(root, "lib")
	script := fmt.Sprintf(
		`source %q; type account_pill_enabled >/dev/null 2>&1 && echo DEFINED || echo MISSING`,
		filepath.Join(lib, "compact-view.sh"))
	cmd := exec.Command("zsh", "-c", script)
	cmd.Env = append(os.Environ(), "WISP_DECK_LIB_DIR="+lib)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("zsh source failed: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "DEFINED") {
		t.Fatalf("compact-view.sh did not load account-switch deps under zsh (pill helpers missing): %s", out)
	}
}

// sourceAccountSwitch builds a bash snippet that sources account-switch.sh and
// its sibling deps from the repo's lib/ before running body. account-switch.sh
// leans on statusline.sh (gt_* helpers), claude-accounts.sh (pointer helpers),
// and tmux-session.sh (build_ai_launch_cmd).
func accountSwitchSnippet(t *testing.T, body string) string {
	t.Helper()
	root := projectRoot(t)
	lib := filepath.Join(root, "lib")
	return fmt.Sprintf(
		"source %q && source %q && source %q && source %q && %s",
		filepath.Join(lib, "statusline.sh"),
		filepath.Join(lib, "claude-accounts.sh"),
		filepath.Join(lib, "tmux-session.sh"),
		filepath.Join(lib, "account-switch.sh"),
		body,
	)
}

func TestAccountPillEnabled_shows_when_relaunch_file_and_two_accounts(t *testing.T) {
	dir := t.TempDir()
	list := writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	relaunch := writeTempFile(t, dir, "relaunch", "tool=claude\n")
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("account_pill_enabled %q %q", relaunch, list)), nil)
	if code != 0 {
		t.Fatalf("expected pill enabled (exit 0), got %d: %s", code, out)
	}
}

func TestAccountPillEnabled_hidden_without_relaunch_file(t *testing.T) {
	dir := t.TempDir()
	list := writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("account_pill_enabled %q %q", "", list)), nil)
	if code == 0 {
		t.Fatalf("expected pill hidden (nonzero exit) with empty relaunch file: %s", out)
	}
}

func TestAccountPillEnabled_hidden_with_single_account(t *testing.T) {
	dir := t.TempDir()
	list := writeTempFile(t, dir, "claude-accounts.list", "# only comments\n")
	relaunch := writeTempFile(t, dir, "relaunch", "tool=claude\n")
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("account_pill_enabled %q %q", relaunch, list)), nil)
	if code == 0 {
		t.Fatalf("expected pill hidden with no managed accounts: %s", out)
	}
}

func TestAccountPill_renders_label_color_and_width(t *testing.T) {
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		`account_pill "Personal" "208"`), nil)
	assertExitCode(t, code, 0)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (pill + width), got %d: %q", len(lines), out)
	}
	assertContains(t, lines[0], "Personal")
	assertContains(t, lines[0], "38;5;208") // account color escape
	assertContains(t, lines[0], "󰀄")        // account glyph 󰀄
	// visible width = leading pad space + glyph + space + len("Personal") + trailing
	// pad space = 4 + 8 = 12. The left/right pad spaces give the hover highlight a
	// little breathing room and are always reserved so the width never changes.
	if strings.TrimSpace(lines[1]) != "12" {
		t.Fatalf("expected width 12, got %q", lines[1])
	}
}

// Hovering the account pill (the mid-session "switch account" button) must make
// it visibly highlight so the pointer target reads as pressable. The hovered
// variant adds a background bar (48;5;238) that the plain pill lacks, while
// keeping the same visible click width so the hit region is unchanged. The
// highlight carries a small pad SPACE on each side of the text — a highlighted
// space before the glyph and after the label — so the button has breathing room
// inside the bar. Both pad spaces are reserved in the plain pill too, so the
// width is identical and the bottom bar never shifts on hover.
func TestAccountPill_hover_highlights_and_keeps_width(t *testing.T) {
	plainOut, code := runBashSnippet(t, accountSwitchSnippet(t,
		`account_pill "Personal" "208"`), nil)
	assertExitCode(t, code, 0)
	hoverOut, code := runBashSnippet(t, accountSwitchSnippet(t,
		`account_pill "Personal" "208" 1`), nil)
	assertExitCode(t, code, 0)

	plain := strings.Split(strings.TrimRight(plainOut, "\n"), "\n")
	hover := strings.Split(strings.TrimRight(hoverOut, "\n"), "\n")
	if len(hover) != 2 {
		t.Fatalf("expected 2 lines (pill + width), got %d: %q", len(hover), hoverOut)
	}
	// The hovered pill must differ from the plain pill (a visible highlight)...
	if hover[0] == plain[0] {
		t.Fatalf("hovered pill should differ from plain pill, both were %q", hover[0])
	}
	// ...specifically by carrying a background-color SGR the plain pill lacks.
	assertContains(t, hover[0], "48;5;238")
	assertNotContains(t, plain[0], "48;5;238")
	assertContains(t, hover[0], "Personal")

	// LEFT pad: the background SGR opens, then a highlighted space, then the glyph —
	// so the highlight has a space of breathing room before the text ("...238m 󰀄").
	glyph := "\xF3\xB0\x80\x84" // 󰀄
	if !strings.Contains(hover[0], "m "+glyph) {
		t.Fatalf("highlight must open with a pad space before the glyph (\"m %s\"): %q",
			glyph, hover[0])
	}
	// RIGHT pad: a highlighted space follows the label, then the reset closes the
	// run ("Personal \x1b[0m") — breathing room after the text, still inside the bar.
	if !strings.Contains(hover[0], "Personal \x1b[0m") {
		t.Fatalf("highlight must carry a pad space after the label before the reset "+
			"(\"Personal \\x1b[0m\"): %q", hover[0])
	}

	// The click width must be identical so the hit region does not move on hover.
	if strings.TrimSpace(hover[1]) != strings.TrimSpace(plain[1]) {
		t.Fatalf("hover width %q must equal plain width %q", hover[1], plain[1])
	}
}

func TestAccountCurrent_active_managed_account(t *testing.T) {
	dir := t.TempDir()
	pointer := writeTempFile(t, dir, "claude-account", "work\n")
	list := writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	colors := writeTempFile(t, dir, "claude-account-colors", "work:170\n")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("account_current %q %q %q %q", pointer, list, defLabel, colors)), nil)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	if got != "Work\t170" {
		t.Fatalf("expected 'Work\\t170', got %q", got)
	}
}

func TestAccountCurrent_default_when_no_pointer(t *testing.T) {
	dir := t.TempDir()
	pointer := filepath.Join(dir, "claude-account") // absent = Default
	list := writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	colors := writeTempFile(t, dir, "claude-account-colors", "default:39\n")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("account_current %q %q %q %q", pointer, list, defLabel, colors)), nil)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	if got != "Default\t39" {
		t.Fatalf("expected 'Default\\t39', got %q", got)
	}
}

// The switcher popup is drawn as a self-styled rounded card by the Go TUI. tmux
// only delivers clicks that land inside a popup, so to close on a click OUTSIDE
// the card the popup must be full-screen (-w/-h 100%) and borderless (-B); the Go
// side then treats any click off the card as cancel. A dimmed capture of the
// screen behind is passed via --backdrop-file so the full-screen popup isn't a
// blank void. This guards the flags the popup is launched with.
func TestOpenAccountSwitcher_launches_fullscreen_borderless_popup(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	// Mock tmux: log every invocation; the popup itself is a no-op so the pointer
	// is unchanged (before == after) and no relaunch is attempted.
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`printf '%%s\n' "$*" >> %q`, rec))
	relaunch := writeTempFile(t, dir, "relaunch", strings.Join([]string{
		"tool=claude", "tool_cmd=claude",
		"settings=/cfg/settings.json", "filter=", "project_dir=/proj",
		"accounts_dir=" + filepath.Join(dir, "claude-accounts"),
		"pointer=" + filepath.Join(dir, "claude-account"),
		"list=" + filepath.Join(dir, "claude-accounts.list"),
		"colors=" + filepath.Join(dir, "claude-account-colors"),
		"default_label=" + filepath.Join(dir, "claude-account-default-label"),
		"",
	}, "\n"))
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("open_account_switcher tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "display-popup")
	assertContains(t, logOut, "-B")   // borderless: the Go card draws its own rounded border
	assertContains(t, logOut, "100%") // full-screen so tmux delivers clicks outside the card
	assertContains(t, logOut, "--backdrop-file")
	assertContains(t, logOut, "claude-account-switch")
}

// The ledger is a long-running process that sources account-switch.sh once at
// startup, so edits to open_account_switcher (e.g. the popup dimensions) don't
// take effect until the whole pane restarts. reload_switcher_lib re-sources the
// module from disk so a single restart is the last one ever needed: after it,
// every open re-reads the current file. This guards that re-source behavior.
func TestReloadSwitcherLib_picks_up_ondisk_edits(t *testing.T) {
	dir := t.TempDir()
	root := projectRoot(t)
	src, err := os.ReadFile(filepath.Join(root, "lib", "account-switch.sh"))
	if err != nil {
		t.Fatal(err)
	}
	libcopy := filepath.Join(dir, "account-switch.sh")
	if err := os.WriteFile(libcopy, src, 0644); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`
source %q
type probe_marker >/dev/null 2>&1 && echo BEFORE-DEFINED || echo BEFORE-MISSING
printf '\nprobe_marker() { echo RELOADED; }\n' >> %q
reload_switcher_lib %q
probe_marker
`, libcopy, libcopy, dir)
	out, code := runBashSnippet(t, body, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "BEFORE-MISSING") // the edit isn't visible until reload
	assertContains(t, out, "RELOADED")       // reload_switcher_lib re-read the file
}

func TestFindAIPane_picks_marked_pane(t *testing.T) {
	dir := t.TempDir()
	// Mock tmux: list-panes emits "pane_id gt_ai" lines; the AI pane carries 1.
	bin := mockCommand(t, dir, "tmux", `
if [ "$1" = "list-panes" ]; then
  printf '%s\n' "%0 0" "%1 1" "%2 0"
fi`)
	env := buildEnv(t, []string{bin})
	out, code := runBashSnippet(t, accountSwitchSnippet(t, "find_ai_pane tmux"), env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "%1" {
		t.Fatalf("expected AI pane '%%1', got %q", out)
	}
}

func TestBuildSwitchLaunchCmd_managed_account_sets_config_dir_and_launches_fresh(t *testing.T) {
	// No stamped session id (8th arg omitted): the previous account had no active
	// session, so the switch must NOT resume the cwd's most-recent conversation
	// (`claude -c`) — it launches a fresh claude under the new login instead,
	// still with the account's config dir and settings.
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		`build_switch_launch_cmd claude claude "/cfg/settings.json" "" "/proj" "/cfg/claude-accounts/work"`), nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, `CLAUDE_CONFIG_DIR="/cfg/claude-accounts/work"`)
	assertContains(t, out, `--settings "/cfg/settings.json"`)
	assertNotContains(t, out, "claude -c")
	assertNotContains(t, out, "--resume")
}

func TestBuildSwitchLaunchCmd_default_account_launches_fresh_without_config_dir(t *testing.T) {
	// No session and the Default (Keychain) login: fresh plain `claude`, no
	// config dir, no `-c` resume.
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		`build_switch_launch_cmd claude claude "/cfg/settings.json" "" "/proj" ""`), nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "CLAUDE_CONFIG_DIR=")
	assertNotContains(t, out, "claude -c")
	assertNotContains(t, out, "--resume")
	assertContains(t, out, "claude")
}

func TestBuildSwitchLaunchCmd_resumes_exact_session_when_stamped(t *testing.T) {
	// A stamped session id must produce `--resume <id>` (the exact conversation),
	// with `-c` only as the fallback step — not the other way round. This keeps
	// the switched pane on ITS conversation in a multi-tab/window project, where
	// bare `-c` (most-recent-in-cwd) could grab a sibling tab's conversation.
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		`build_switch_launch_cmd claude claude "/cfg/settings.json" "" "/proj" "/cfg/claude-accounts/work" "sess-xyz-1"`), nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "--resume sess-xyz-1")
}

func TestCurrentAISession_reads_stamped_env(t *testing.T) {
	dir := t.TempDir()
	bin := mockCommand(t, dir, "tmux", `
if [ "$1" = "show-environment" ]; then printf 'WISP_DECK_CLAUDE_SESSION=abc-42\n'; exit 0; fi`)
	env := buildEnv(t, []string{bin})
	out, code := runBashSnippet(t, accountSwitchSnippet(t, "current_ai_session tmux"), env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "abc-42" {
		t.Fatalf("expected stamped session 'abc-42', got %q", out)
	}
}

func TestCurrentAISession_empty_when_unset(t *testing.T) {
	dir := t.TempDir()
	// tmux prints `-NAME` (with a leading dash) for an unset variable.
	bin := mockCommand(t, dir, "tmux", `
if [ "$1" = "show-environment" ]; then printf -- '-WISP_DECK_CLAUDE_SESSION\n'; exit 0; fi`)
	env := buildEnv(t, []string{bin})
	out, code := runBashSnippet(t, accountSwitchSnippet(t, "current_ai_session tmux"), env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected empty session when unset, got %q", out)
	}
}

func TestCurrentAISession_suppressed_when_live_session_differs(t *testing.T) {
	// The durable stamp (WISP_DECK_CLAUDE_SESSION) deliberately lags: after the
	// user runs /new (or /clear), claude's LIVE session_id changes immediately
	// but the fresh session has no model turn yet, so it stays non-resumable and
	// the durable stamp still names the OLD (now closed) conversation. Resuming
	// that durable id on an account switch would resurrect the conversation the
	// user just closed. When the live id differs from the durable one, refuse it.
	dir := t.TempDir()
	bin := mockCommand(t, dir, "tmux", `
if [ "$1" = "show-environment" ]; then
  case "$2" in
    WISP_DECK_CLAUDE_LIVE_SESSION) printf 'WISP_DECK_CLAUDE_LIVE_SESSION=new-fresh\n' ;;
    *) printf 'WISP_DECK_CLAUDE_SESSION=old-durable\n' ;;
  esac
  exit 0
fi`)
	env := buildEnv(t, []string{bin})
	out, code := runBashSnippet(t, accountSwitchSnippet(t, "current_ai_session tmux"), env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected empty (suppressed) when live session differs, got %q", out)
	}
}

func TestCurrentAISession_returns_id_when_live_session_matches(t *testing.T) {
	// Live id equals the durable id — the pane is still on its stamped
	// conversation, so resuming it is correct.
	dir := t.TempDir()
	bin := mockCommand(t, dir, "tmux", `
if [ "$1" = "show-environment" ]; then
  case "$2" in
    WISP_DECK_CLAUDE_LIVE_SESSION) printf 'WISP_DECK_CLAUDE_LIVE_SESSION=abc-42\n' ;;
    *) printf 'WISP_DECK_CLAUDE_SESSION=abc-42\n' ;;
  esac
  exit 0
fi`)
	env := buildEnv(t, []string{bin})
	out, code := runBashSnippet(t, accountSwitchSnippet(t, "current_ai_session tmux"), env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "abc-42" {
		t.Fatalf("expected 'abc-42' when live matches durable, got %q", out)
	}
}

func TestCurrentAISession_returns_id_when_live_session_unset(t *testing.T) {
	// An older statusline (or a pane before its first statusline render) never
	// stamps the live var — tmux prints `-NAME` for it. The durable id must
	// still be returned unchanged (backward compatible).
	dir := t.TempDir()
	bin := mockCommand(t, dir, "tmux", `
if [ "$1" = "show-environment" ]; then
  case "$2" in
    WISP_DECK_CLAUDE_LIVE_SESSION) printf -- '-WISP_DECK_CLAUDE_LIVE_SESSION\n' ;;
    *) printf 'WISP_DECK_CLAUDE_SESSION=abc-42\n' ;;
  esac
  exit 0
fi`)
	env := buildEnv(t, []string{bin})
	out, code := runBashSnippet(t, accountSwitchSnippet(t, "current_ai_session tmux"), env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "abc-42" {
		t.Fatalf("expected 'abc-42' when live var unset, got %q", out)
	}
}

func TestRelaunchAIPane_resumes_exact_stamped_conversation(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "claude-account", "work\n")
	acctDir := filepath.Join(dir, "claude-accounts", "work")
	writeTempFile(t, acctDir, ".keep", "")
	relaunch := writeTempFile(t, dir, "relaunch", strings.Join([]string{
		"tool=claude",
		"tool_cmd=claude",
		"settings=/cfg/settings.json",
		"filter=",
		"project_dir=/proj",
		"accounts_dir=" + filepath.Join(dir, "claude-accounts"),
		"pointer=" + filepath.Join(dir, "claude-account"),
		"list=" + filepath.Join(dir, "claude-accounts.list"),
		"colors=" + filepath.Join(dir, "claude-account-colors"),
		"default_label=" + filepath.Join(dir, "claude-account-default-label"),
		"",
	}, "\n"))
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "list-panes" ]; then printf '%%s\n' "%%1 1"; exit 0; fi
if [ "$1" = "show-environment" ]; then printf 'WISP_DECK_CLAUDE_SESSION=sess-abc-123\n'; exit 0; fi
printf '%%s\n' "$*" >> %q`, rec))
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("relaunch_ai_pane tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "--resume sess-abc-123")
}

// End-to-end regression guard for the "account switch after /new resurrected
// the closed conversation" bug: the durable stamp still names the old session
// (sess-old) while claude's LIVE session moved to a fresh one (sess-new). The
// relaunch must NOT `--resume sess-old` — it respawns a fresh claude instead.
// This locks the guarantee at the relaunch boundary, so a future refactor that
// bypasses current_ai_session (or drops its live-awareness) is caught here even
// if the unit test on current_ai_session is not.
func TestRelaunchAIPane_does_not_resume_closed_session_after_new(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "claude-account", "work\n")
	acctDir := filepath.Join(dir, "claude-accounts", "work")
	writeTempFile(t, acctDir, ".keep", "")
	relaunch := writeTempFile(t, dir, "relaunch", strings.Join([]string{
		"tool=claude",
		"tool_cmd=claude",
		"settings=/cfg/settings.json",
		"filter=",
		"project_dir=/proj",
		"accounts_dir=" + filepath.Join(dir, "claude-accounts"),
		"pointer=" + filepath.Join(dir, "claude-account"),
		"list=" + filepath.Join(dir, "claude-accounts.list"),
		"colors=" + filepath.Join(dir, "claude-account-colors"),
		"default_label=" + filepath.Join(dir, "claude-account-default-label"),
		"",
	}, "\n"))
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "list-panes" ]; then printf '%%s\n' "%%1 1"; exit 0; fi
if [ "$1" = "show-environment" ]; then
  case "$2" in
    WISP_DECK_CLAUDE_LIVE_SESSION) printf 'WISP_DECK_CLAUDE_LIVE_SESSION=sess-new\n' ;;
    *) printf 'WISP_DECK_CLAUDE_SESSION=sess-old\n' ;;
  esac
  exit 0
fi
printf '%%s\n' "$*" >> %q`, rec))
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("relaunch_ai_pane tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "respawn-pane")         // the switch still happens
	assertNotContains(t, logOut, "--resume sess-old") // never the closed session
	assertNotContains(t, logOut, "--resume")          // fresh launch, no resume at all
}

func TestWriteRelaunchContext_writes_all_keys(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "relaunch")
	cfg := filepath.Join(dir, "cfg")
	_, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		`write_relaunch_context %q claude claude "/cfg/settings.json" "flt -- " "/proj" %q`,
		out, cfg)), nil)
	assertExitCode(t, code, 0)
	body, _ := runBashSnippet(t, fmt.Sprintf("cat %q", out), nil)
	assertContains(t, body, "tool=claude")
	assertContains(t, body, "tool_cmd=claude")
	assertContains(t, body, "settings=/cfg/settings.json")
	assertContains(t, body, "filter=flt -- ") // trailing space preserved
	assertContains(t, body, "project_dir=/proj")
	assertContains(t, body, "accounts_dir="+filepath.Join(cfg, "claude-accounts"))
	assertContains(t, body, "pointer="+filepath.Join(cfg, "claude-account"))
	assertContains(t, body, "list="+filepath.Join(cfg, "claude-accounts.list"))
	assertContains(t, body, "colors="+filepath.Join(cfg, "claude-account-colors"))
	assertContains(t, body, "default_label="+filepath.Join(cfg, "claude-account-default-label"))
}

// A round-trip: write the context, then relaunch reads it back and respawns the
// AI pane under the account the pointer names.
func TestWriteRelaunchContext_round_trips_through_relaunch(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "cfg")
	writeTempFile(t, cfg, "claude-account", "work\n")
	writeTempFile(t, filepath.Join(cfg, "claude-accounts", "work"), ".keep", "")
	out := filepath.Join(dir, "relaunch")
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "list-panes" ]; then printf '%%s\n' "%%1 1"; exit 0; fi
printf '%%s\n' "$*" >> %q`, rec))
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		`write_relaunch_context %q claude claude "/cfg/settings.json" "" "/proj" %q && relaunch_ai_pane tmux %q`,
		out, cfg, out)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "respawn-pane")
	assertContains(t, logOut, `CLAUDE_CONFIG_DIR="`+filepath.Join(cfg, "claude-accounts", "work")+`"`)
}

// The tmux mock here never stamps WISP_DECK_CLAUDE_SESSION, so this pane had no
// active session when the switch fired. The relaunch must respawn under the new
// account but launch a FRESH claude — not resume the cwd's most-recent
// conversation (`claude -c`), which was never this pane's.
func TestRelaunchAIPane_respawns_fresh_with_new_account_when_no_active_session(t *testing.T) {
	dir := t.TempDir()
	// The switcher already wrote the pointer to "work"; its config dir exists.
	writeTempFile(t, dir, "claude-account", "work\n")
	acctDir := filepath.Join(dir, "claude-accounts", "work")
	writeTempFile(t, acctDir, ".keep", "")
	relaunch := writeTempFile(t, dir, "relaunch", strings.Join([]string{
		"tool=claude",
		"tool_cmd=claude",
		"settings=/cfg/settings.json",
		"filter=",
		"project_dir=/proj",
		"accounts_dir=" + filepath.Join(dir, "claude-accounts"),
		"pointer=" + filepath.Join(dir, "claude-account"),
		"list=" + filepath.Join(dir, "claude-accounts.list"),
		"colors=" + filepath.Join(dir, "claude-account-colors"),
		"default_label=" + filepath.Join(dir, "claude-account-default-label"),
		"",
	}, "\n"))
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "list-panes" ]; then
  printf '%%s\n' "%%0 0" "%%1 1"
  exit 0
fi
printf '%%s\n' "$*" >> %q`, rec))
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("relaunch_ai_pane tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "respawn-pane")
	assertContains(t, logOut, "-k")
	assertContains(t, logOut, "%1")
	assertContains(t, logOut, `CLAUDE_CONFIG_DIR="`+filepath.Join(dir, "claude-accounts", "work")+`"`)
	assertNotContains(t, logOut, "claude -c")
	assertNotContains(t, logOut, "--resume")
	_ = out
}

// Top-level regression guard for the mid-session account switch: relaunch_ai_pane
// runs FROM the compact-view pane, whose shell is ZSH (the user's $SHELL). The
// original bug was a bash-glob idiom in a helper it calls (sync_claude_shared_state's
// `"$dest".migrating.*`) that, under zsh's default `nomatch`, aborted the whole pane
// — so the file-list view was killed instead of restoring, and the AI pane never
// even respawned. This drives the ENTIRE relaunch path under zsh with a real (empty)
// HOME/.claude and account dir, so ANY fatal unmatched glob anywhere in the flow
// (now or added later) fails this test. Success = the pane survived to run the tmux
// respawn.
func TestRelaunchAIPane_survives_full_path_under_zsh(t *testing.T) {
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not available")
	}
	root := projectRoot(t)
	lib := filepath.Join(root, "lib")
	dir := t.TempDir()

	// The switcher already wrote the pointer to "work"; its config dir exists.
	writeTempFile(t, dir, "claude-account", "work\n")
	writeTempFile(t, filepath.Join(dir, "claude-accounts", "work"), ".keep", "")
	// A real HOME/.claude so the shared-state/settings sync actually runs its
	// glob loops (the abort site) rather than short-circuiting on a missing dir.
	home := filepath.Join(dir, "home")
	writeTempFile(t, filepath.Join(home, ".claude"), ".keep", "")

	relaunch := writeTempFile(t, dir, "relaunch", strings.Join([]string{
		"tool=claude", "tool_cmd=claude",
		"settings=/cfg/settings.json", "filter=", "project_dir=/proj",
		"accounts_dir=" + filepath.Join(dir, "claude-accounts"),
		"pointer=" + filepath.Join(dir, "claude-account"),
		"list=" + filepath.Join(dir, "claude-accounts.list"),
		"colors=" + filepath.Join(dir, "claude-account-colors"),
		"default_label=" + filepath.Join(dir, "claude-account-default-label"),
		"",
	}, "\n"))
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "list-panes" ]; then printf '%%s\n' "%%1 1"; exit 0; fi
if [ "$1" = "show-environment" ]; then printf 'WISP_DECK_CLAUDE_SESSION=sess-1\n'; exit 0; fi
printf '%%s\n' "$*" >> %q`, rec))

	// Mirror the pane's real runtime: zsh sourcing the deps (account-switch.sh
	// last — it defines relaunch_ai_pane and leans on the others), then the switch.
	script := fmt.Sprintf(
		"source %q && source %q && source %q && source %q && source %q && "+
			"relaunch_ai_pane tmux %q && print DONE-RELAUNCH-SURVIVED",
		filepath.Join(lib, "statusline.sh"),
		filepath.Join(lib, "claude-accounts.sh"),
		filepath.Join(lib, "tmux-session.sh"),
		filepath.Join(lib, "claude-shared-settings.sh"),
		filepath.Join(lib, "account-switch.sh"),
		relaunch)
	cmd := exec.Command("zsh", "-c", script)
	cmd.Env = append(buildEnv(t, []string{bin}), "HOME="+home)
	out, err := cmd.CombinedOutput()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	}
	assertExitCode(t, code, 0)
	if !strings.Contains(string(out), "DONE-RELAUNCH-SURVIVED") {
		t.Fatalf("the mid-session switch aborted the zsh pane (file-list view would vanish); output:\n%s", out)
	}
	// And the pane actually got respawned under the new account — the switch's point.
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "respawn-pane")
	assertContains(t, logOut, filepath.Join(dir, "claude-accounts", "work"))
}

// TestAccountColor_assigns_palette_member_under_zsh reproduces the compact-view
// pill's real runtime: account_current -> gt_account_color runs under the pane's
// $SHELL (zsh). zsh does not word-split an unquoted $GT_ACCOUNT_PALETTE and its
// arrays are 1-indexed, so a bash-only impl assigns an EMPTY color and persists a
// poisoned "work:" entry into the SHARED colors file. The bash statusline then
// reads that empty value back and loses the account's profile color — so the
// status line can no longer signal which account is active. Assigning a new
// account's color under zsh must yield a real palette member and never persist an
// empty value.
func TestAccountColor_assigns_palette_member_under_zsh(t *testing.T) {
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not available")
	}
	root := projectRoot(t)
	dir := t.TempDir()
	colors := filepath.Join(dir, "claude-account-colors")
	script := fmt.Sprintf(`source %q; gt_account_color %q work`,
		filepath.Join(root, "lib", "statusline.sh"), colors)
	cmd := exec.Command("zsh", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("zsh run failed: %v: %s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if !accountPalette[got] {
		t.Fatalf("under zsh, gt_account_color returned %q, not a palette member (poisons the shared colors file the statusline reads)", got)
	}
	data, _ := os.ReadFile(colors)
	if !strings.Contains(string(data), "work:"+got) {
		t.Fatalf("under zsh, gt_account_color must persist work:%s, file was:\n%s", got, string(data))
	}
}

// The active-account POINTER is global, but a mid-session switch changes only THIS
// pane's running claude. The session's own running account is therefore stamped
// into the tmux session env (WISP_DECK_CLAUDE_ACCOUNT) and read back here.
func TestCurrentSessionAccount_reads_stamped_env(t *testing.T) {
	dir := t.TempDir()
	bin := mockCommand(t, dir, "tmux", `
if [ "$1" = "show-environment" ] && [ "$2" = "WISP_DECK_CLAUDE_ACCOUNT" ]; then
  printf 'WISP_DECK_CLAUDE_ACCOUNT=personal\n'; exit 0
fi`)
	env := buildEnv(t, []string{bin})
	pointer := writeTempFile(t, dir, "claude-account", "work\n") // must NOT win
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("current_session_account tmux %q", pointer)), env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "personal" {
		t.Fatalf("expected session account 'personal', got %q", out)
	}
}

// A session launched before the stamp existed has no WISP_DECK_CLAUDE_ACCOUNT in
// its tmux env (tmux prints `-NAME`); the pointer is the best remaining guess.
func TestCurrentSessionAccount_falls_back_to_pointer_when_unstamped(t *testing.T) {
	dir := t.TempDir()
	bin := mockCommand(t, dir, "tmux", `
if [ "$1" = "show-environment" ]; then printf -- '-WISP_DECK_CLAUDE_ACCOUNT\n'; exit 0; fi`)
	env := buildEnv(t, []string{bin})
	pointer := writeTempFile(t, dir, "claude-account", "work\n")
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("current_session_account tmux %q", pointer)), env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "work" {
		t.Fatalf("expected pointer fallback 'work', got %q", out)
	}
}

// A stamped Default (empty value) must NOT fall back to the pointer: the pane
// really runs the Default login even if the global pointer names another account.
func TestCurrentSessionAccount_stamped_default_beats_pointer(t *testing.T) {
	dir := t.TempDir()
	bin := mockCommand(t, dir, "tmux", `
if [ "$1" = "show-environment" ] && [ "$2" = "WISP_DECK_CLAUDE_ACCOUNT" ]; then
  printf 'WISP_DECK_CLAUDE_ACCOUNT=\n'; exit 0
fi`)
	env := buildEnv(t, []string{bin})
	pointer := writeTempFile(t, dir, "claude-account", "work\n")
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("current_session_account tmux %q", pointer)), env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected stamped Default (empty), got %q", out)
	}
}

// switcherRelaunchCtx writes a relaunch-context file for the switcher tests.
func switcherRelaunchCtx(t *testing.T, dir string) string {
	t.Helper()
	return writeTempFile(t, dir, "relaunch", strings.Join([]string{
		"tool=claude", "tool_cmd=claude",
		"settings=", "filter=", "project_dir=/proj",
		"accounts_dir=" + filepath.Join(dir, "claude-accounts"),
		"pointer=" + filepath.Join(dir, "claude-account"),
		"list=" + filepath.Join(dir, "claude-accounts.list"),
		"colors=" + filepath.Join(dir, "claude-account-colors"),
		"default_label=" + filepath.Join(dir, "claude-account-default-label"),
		"",
	}, "\n"))
}

// switcherMockTmux builds a tmux mock for the full open_account_switcher flow.
// display-popup simulates the user's popup interaction: chosen is the dir the
// user picks ("" = Default) — written to the --result-file the popup command
// carries and to the pointer (what the real Go popup does); chosen == "CANCEL"
// simulates esc/outside-click (no result file, no pointer write). sessionAcct is
// what the tmux session env says this pane is running.
func switcherMockTmux(t *testing.T, dir, pointer, sessionAcct, chosen, rec string) string {
	t.Helper()
	return mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "show-environment" ] && [ "$2" = "WISP_DECK_CLAUDE_ACCOUNT" ]; then
  printf 'WISP_DECK_CLAUDE_ACCOUNT=%s\n'; exit 0
fi
if [ "$1" = "show-environment" ]; then printf -- '-WISP_DECK_CLAUDE_SESSION\n'; exit 0; fi
if [ "$1" = "list-panes" ]; then printf '%%s\n' "%%1 1"; exit 0; fi
if [ "$1" = "display-popup" ]; then
  [ "%s" = "CANCEL" ] && exit 0
  rf=$(printf '%%s' "$*" | sed -n 's/.*--result-file \([^ ]*\).*/\1/p')
  [ -n "$rf" ] && printf '%s\n' > "$rf"
  if [ -z "%s" ]; then rm -f %q; else printf '%s\n' > %q; fi
  exit 0
fi
printf '%%s\n' "$*" >> %q`,
		sessionAcct, chosen, chosen, chosen, pointer, chosen, pointer, rec))
}

// THE mid-session revert bug: the pane runs "personal", but the GLOBAL pointer was
// already flipped to Default by a switch in another session (or the launcher). The
// user opens the switcher here and picks Default — the account this pane should
// now run. The old code compared the pointer before/after the popup (unchanged:
// Default == Default) and silently skipped the relaunch, so the pane kept running
// "personal" and the account appeared to "switch back". The relaunch decision must
// compare the popup's choice against the SESSION's running account instead.
func TestOpenAccountSwitcher_relaunches_when_pointer_already_matches_choice(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "claude-accounts.list", "Personal:personal\n")
	writeTempFile(t, filepath.Join(dir, "claude-accounts", "personal"), ".keep", "")
	pointer := filepath.Join(dir, "claude-account") // absent = Default already
	rec := filepath.Join(dir, "tmux.log")
	bin := switcherMockTmux(t, dir, pointer, "personal", "", rec)
	relaunch := switcherRelaunchCtx(t, dir)
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("open_account_switcher tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "respawn-pane")
}

// Picking the account the pane ALREADY runs must not relaunch — even when that
// choice rewrites a stale global pointer. Relaunching would kill the running
// claude for nothing.
func TestOpenAccountSwitcher_skips_relaunch_when_choice_matches_running_account(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "claude-accounts.list", "Personal:personal\n")
	writeTempFile(t, filepath.Join(dir, "claude-accounts", "personal"), ".keep", "")
	pointer := filepath.Join(dir, "claude-account") // absent: global pointer says Default
	rec := filepath.Join(dir, "tmux.log")
	bin := switcherMockTmux(t, dir, pointer, "personal", "personal", rec)
	relaunch := switcherRelaunchCtx(t, dir)
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("open_account_switcher tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q 2>/dev/null || true", rec), nil)
	assertNotContains(t, logOut, "respawn-pane")
}

// Cancelling the popup (esc / click outside) must never relaunch, even when the
// global pointer disagrees with the pane's running account.
func TestOpenAccountSwitcher_skips_relaunch_on_cancel(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "claude-accounts.list", "Personal:personal\n")
	pointer := filepath.Join(dir, "claude-account") // absent while the pane runs personal
	rec := filepath.Join(dir, "tmux.log")
	bin := switcherMockTmux(t, dir, pointer, "personal", "CANCEL", rec)
	relaunch := switcherRelaunchCtx(t, dir)
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("open_account_switcher tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q 2>/dev/null || true", rec), nil)
	assertNotContains(t, logOut, "respawn-pane")
}

// The switcher popup must be told which account THIS pane runs (--active) and
// where to report the user's choice (--result-file) — the pointer can't carry
// either: it is global and display-popup swallows the popup's stdout.
func TestOpenAccountSwitcher_passes_active_and_result_file(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "claude-accounts.list", "Personal:personal\n")
	pointer := filepath.Join(dir, "claude-account")
	rec := filepath.Join(dir, "popup.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "show-environment" ] && [ "$2" = "WISP_DECK_CLAUDE_ACCOUNT" ]; then
  printf 'WISP_DECK_CLAUDE_ACCOUNT=personal\n'; exit 0
fi
if [ "$1" = "display-popup" ]; then printf '%%s\n' "$*" >> %q; exit 0; fi
exit 0`, rec))
	relaunch := switcherRelaunchCtx(t, dir)
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("open_account_switcher tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "--active personal")
	assertContains(t, logOut, "--result-file")
	_ = pointer
}

// After a relaunch the pane runs a new account — the stamp in the tmux session
// env must follow, or the next pill render / switch decision reads a stale value.
func TestRelaunchAIPane_stamps_session_account_env(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "claude-account", "work\n")
	writeTempFile(t, filepath.Join(dir, "claude-accounts", "work"), ".keep", "")
	relaunch := switcherRelaunchCtx(t, dir)
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "list-panes" ]; then printf '%%s\n' "%%1 1"; exit 0; fi
if [ "$1" = "show-environment" ]; then printf -- '-WISP_DECK_CLAUDE_SESSION\n'; exit 0; fi
printf '%%s\n' "$*" >> %q`, rec))
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("relaunch_ai_pane tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "set-environment WISP_DECK_CLAUDE_ACCOUNT work")
}

// Relaunching under the Default login stamps an EMPTY value (set, not unset), so
// readers see "this pane runs Default" instead of falling back to the pointer.
func TestRelaunchAIPane_stamps_empty_for_default(t *testing.T) {
	dir := t.TempDir()
	// pointer absent = Default chosen
	relaunch := switcherRelaunchCtx(t, dir)
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "list-panes" ]; then printf '%%s\n' "%%1 1"; exit 0; fi
if [ "$1" = "show-environment" ]; then printf -- '-WISP_DECK_CLAUDE_SESSION\n'; exit 0; fi
printf '%%s\n' "$*" >> %q`, rec))
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("relaunch_ai_pane tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "set-environment WISP_DECK_CLAUDE_ACCOUNT \n")
}

// The ledger pill must show the account THIS pane runs, not the global pointer:
// after a switch in another session flips the pointer, this pane's pill would
// otherwise "switch back" to an account this pane never changed to.
func TestAccountCurrent_prefers_session_account_over_pointer(t *testing.T) {
	dir := t.TempDir()
	pointer := writeTempFile(t, dir, "claude-account", "work\n") // global: work
	list := writeTempFile(t, dir, "claude-accounts.list", "Work:work\nPersonal:personal\n")
	colors := writeTempFile(t, dir, "claude-account-colors", "work:170\npersonal:39\n")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	bin := mockCommand(t, dir, "tmux", `
if [ "$1" = "show-environment" ] && [ "$2" = "WISP_DECK_CLAUDE_ACCOUNT" ]; then
  printf 'WISP_DECK_CLAUDE_ACCOUNT=personal\n'; exit 0
fi`)
	env := buildEnv(t, []string{bin})
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("account_current %q %q %q %q tmux", pointer, list, defLabel, colors)), env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "Personal\t39" {
		t.Fatalf("expected the SESSION's account 'Personal\\t39', got %q", out)
	}
}

// Skew guard: the lib is installed as a live symlink while the wisp-deck-tui
// binary is a copy, so a newer lib can face an older binary that rejects
// --active/--result-file (cobra errors out before the UI runs) — the switcher
// would silently stop switching at all. When the binary's help shows the
// claude-account-switch command but not --result-file, the switcher must omit
// the new flags and fall back to the legacy pointer-diff relaunch decision.
func TestOpenAccountSwitcher_legacy_binary_falls_back_to_pointer_diff(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "claude-accounts.list", "Personal:personal\n")
	writeTempFile(t, filepath.Join(dir, "claude-accounts", "personal"), ".keep", "")
	pointer := filepath.Join(dir, "claude-account") // absent: Default active
	rec := filepath.Join(dir, "tmux.log")
	popupLog := filepath.Join(dir, "popup.log")
	// An OLD binary: its help lists the command's flags without --result-file.
	tuiBin := mockCommand(t, dir, "wisp-deck-tui", `
printf 'Usage:\n  wisp-deck-tui claude-account-switch [flags]\n\nFlags:\n      --backdrop-file string\n      --pointer string\n'`)
	// tmux: the popup "runs the old binary" — the user picks personal, so the
	// pointer changes; no result file exists (the old binary can't write one).
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "show-environment" ]; then printf -- '-WISP_DECK_CLAUDE_ACCOUNT\n'; exit 0; fi
if [ "$1" = "list-panes" ]; then printf '%%s\n' "%%1 1"; exit 0; fi
if [ "$1" = "display-popup" ]; then
  printf '%%s\n' "$*" >> %q
  printf 'personal\n' > %q
  exit 0
fi
printf '%%s\n' "$*" >> %q`, popupLog, pointer, rec))
	relaunch := switcherRelaunchCtx(t, dir)
	env := buildEnv(t, []string{bin, tuiBin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("open_account_switcher tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)
	popupOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", popupLog), nil)
	assertNotContains(t, popupOut, "--result-file") // old binary would reject it
	assertNotContains(t, popupOut, "--active")
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "respawn-pane") // legacy pointer-diff still switches
}

// A missing binary (or unparseable help) must NOT trigger the legacy path: the
// current binary is the norm, and the new flags are what make the switch stick.
func TestSwitcherSupportsSessionFlags_missing_binary_counts_as_supported(t *testing.T) {
	dir := t.TempDir()
	// PATH holds only an empty dir: wisp-deck-tui is nowhere to be found.
	empty := filepath.Join(dir, "bin")
	if err := os.MkdirAll(empty, 0755); err != nil {
		t.Fatal(err)
	}
	env := buildEnv(t, []string{empty})
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		"switcher_supports_session_flags"), env)
	assertExitCode(t, code, 0)
}

// wrapper.sh must stamp the launch account into the tmux session env so the
// pill/switcher can know what this session runs (WISP_DECK_CLAUDE_ACCOUNT).
func TestWrapper_stamps_session_account_on_new_session(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "new-session") && strings.Contains(line, "WISP_DECK_CLAUDE_ACCOUNT=") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("wrapper.sh must pass WISP_DECK_CLAUDE_ACCOUNT to tmux new-session via -e")
	}
}

// The mid-session agent switch needs to know every available tool and its
// binary, plus where the ai-tool preference lives — the context must carry
// them (extra trailing args keep older call sites valid).
func TestWriteRelaunchContext_writes_tool_switch_keys(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "relaunch")
	cfg := filepath.Join(dir, "cfg")
	_, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		`write_relaunch_context %q claude claude "" "" "/proj" %q "claude opencode codex" claude /opt/opencode /opt/codex`,
		out, cfg)), nil)
	assertExitCode(t, code, 0)
	body, _ := runBashSnippet(t, fmt.Sprintf("cat %q", out), nil)
	assertContains(t, body, "tools=claude opencode codex")
	assertContains(t, body, "claude_cmd=claude")
	assertContains(t, body, "opencode_cmd=/opt/opencode")
	assertContains(t, body, "codex_cmd=/opt/codex")
	assertContains(t, body, "tool_pref="+filepath.Join(cfg, "ai-tool"))
}

// _read_relaunch_ctx must surface the new keys to callers.
func TestReadRelaunchCtx_reads_tool_switch_keys(t *testing.T) {
	dir := t.TempDir()
	ctx := writeTempFile(t, dir, "relaunch", strings.Join([]string{
		"tool=codex", "tool_cmd=codex", "tools=claude codex",
		"claude_cmd=/opt/claude", "opencode_cmd=", "codex_cmd=codex",
		"tool_pref=" + filepath.Join(dir, "ai-tool"), "",
	}, "\n"))
	out, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		`_rc_tool="" _rc_tools="" _rc_claude_cmd="" _rc_opencode_cmd="" _rc_codex_cmd="" _rc_tool_pref=""
_read_relaunch_ctx %q
printf '%%s|%%s|%%s|%%s\n' "$_rc_tools" "$_rc_claude_cmd" "$_rc_codex_cmd" "$_rc_tool_pref"`,
		ctx)), nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "claude codex|/opt/claude|codex|"+filepath.Join(dir, "ai-tool"))
}

// The relaunch context must be written for EVERY tool — opencode/codex
// sessions need it so their ledger pill can switch agents too. The old
// claude-only gate around the WISP_DECK_RELAUNCH_FILE block must be gone, and
// the call must hand the available tools + per-tool commands to the context.
func TestWrapper_writes_relaunch_context_for_all_tools(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(data), `WISP_DECK_RELAUNCH_FILE="$SHARE_DIR/relaunch-`)
	end := strings.Index(string(data), "export WISP_DECK_RELAUNCH_FILE")
	if start < 0 || end < 0 || end < start {
		t.Fatal("wrapper.sh must still build WISP_DECK_RELAUNCH_FILE before exporting it")
	}
	block := string(data[start:end])
	if strings.Contains(block, `= "claude"`) {
		t.Fatal("relaunch-context block must not be gated on the claude tool")
	}
	if !strings.Contains(block, "AI_TOOLS_AVAILABLE") {
		t.Fatal("write_relaunch_context must receive the available tools")
	}
	if !strings.Contains(block, "OPENCODE_CMD") || !strings.Contains(block, "CODEX_CMD") {
		t.Fatal("write_relaunch_context must receive the per-tool commands")
	}
}

// switcherToolCtx writes a relaunch context for the agent-switch tests: the
// running tool, the available tools, and per-tool binaries.
func switcherToolCtx(t *testing.T, dir, tool, tools string) string {
	t.Helper()
	return writeTempFile(t, dir, "relaunch", strings.Join([]string{
		"tool=" + tool, "tool_cmd=" + tool,
		"settings=", "filter=", "project_dir=/proj",
		"accounts_dir=" + filepath.Join(dir, "claude-accounts"),
		"pointer=" + filepath.Join(dir, "claude-account"),
		"list=" + filepath.Join(dir, "claude-accounts.list"),
		"colors=" + filepath.Join(dir, "claude-account-colors"),
		"default_label=" + filepath.Join(dir, "claude-account-default-label"),
		"tools=" + tools,
		"claude_cmd=/opt/claude", "opencode_cmd=/opt/opencode", "codex_cmd=/opt/codex",
		"tool_pref=" + filepath.Join(dir, "ai-tool"),
		"",
	}, "\n"))
}

// mockSwitcherBinary mocks wisp-deck-tui so the capability probes see a binary
// supporting the session AND agent-row flags — the developer machine's real
// (possibly older) binary must not decide what these tests exercise.
func mockSwitcherBinary(t *testing.T, dir string) string {
	t.Helper()
	return mockCommand(t, dir, "wisp-deck-tui",
		`printf 'claude-account-switch\n --result-file\n --tools\n'`)
}

// switcherToolMockTmux mocks tmux for the agent-switch flow: the popup writes
// chosen (e.g. "tool:codex" or an account dir) to the result file; the pane's
// input line reads empty (so the draft stash fast-path skips its slow poll);
// everything else is logged.
func switcherToolMockTmux(t *testing.T, dir, sessionAcctLine, chosen, rec string) string {
	t.Helper()
	return mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "show-environment" ] && [ "$2" = "WISP_DECK_CLAUDE_ACCOUNT" ]; then
  printf '%%s\n' %q; exit 0
fi
if [ "$1" = "show-environment" ]; then printf -- '-WISP_DECK_CLAUDE_SESSION\n'; exit 0; fi
if [ "$1" = "list-panes" ]; then printf '%%s\n' "%%1 1"; exit 0; fi
if [ "$1" = "capture-pane" ]; then printf '❯\n'; exit 0; fi
if [ "$1" = "display-popup" ]; then
  rf=$(printf '%%s' "$*" | sed -n 's/.*--result-file \([^ ]*\).*/\1/p')
  [ -n "$rf" ] && printf '%%s\n' %q > "$rf"
  exit 0
fi
printf '%%s\n' "$*" >> %q`, sessionAcctLine, chosen, rec))
}

// The popup must be told which OTHER agents exist (--tools) and which tool the
// pane runs (--active-tool), or it can only ever offer claude logins.
func TestOpenAccountSwitcher_passes_tools_and_active_tool(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "popup.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "show-environment" ]; then printf -- '-WISP_DECK_CLAUDE_ACCOUNT\n'; exit 0; fi
if [ "$1" = "display-popup" ]; then printf '%%s\n' "$*" >> %q; exit 0; fi
exit 0`, rec))
	mockSwitcherBinary(t, dir)
	relaunch := switcherToolCtx(t, dir, "codex", "claude opencode codex")
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("open_account_switcher tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "--tools claude,opencode,codex")
	assertContains(t, logOut, "--active-tool codex")
}

// Picking an agent row relaunches the AI pane under that tool's binary, stamps
// the session's tool, persists the launcher preference, and rewrites the
// relaunch context so the NEXT switch knows what the pane now runs.
func TestOpenAccountSwitcher_tool_result_switches_agent(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	bin := switcherToolMockTmux(t, dir, "WISP_DECK_CLAUDE_ACCOUNT=", "tool:codex", rec)
	mockSwitcherBinary(t, dir)
	// The user's saved launcher preference before the switch.
	writeTempFile(t, dir, "ai-tool", "claude\n")
	relaunch := switcherToolCtx(t, dir, "claude", "claude codex")
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("open_account_switcher tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "respawn-pane")
	assertContains(t, logOut, "/opt/codex")
	assertContains(t, logOut, "set-environment WISP_DECK_TOOL codex")
	// The switch is session-scoped: the launcher's ai-tool preference (read for
	// the NEXT launch and by OTHER sessions) must NOT be steered to codex.
	pref, _ := runBashSnippet(t, fmt.Sprintf("cat %q", filepath.Join(dir, "ai-tool")), nil)
	assertContains(t, pref, "claude")
	assertNotContains(t, pref, "codex")
	ctx, _ := runBashSnippet(t, fmt.Sprintf("cat %q", relaunch), nil)
	assertContains(t, ctx, "tool=codex")
	assertContains(t, ctx, "tool_cmd=/opt/codex")
}

// Picking the agent the pane already runs is a no-op — relaunching would kill
// the running tool for nothing.
func TestOpenAccountSwitcher_tool_result_same_agent_skips(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	bin := switcherToolMockTmux(t, dir, "-WISP_DECK_CLAUDE_ACCOUNT", "tool:codex", rec)
	mockSwitcherBinary(t, dir)
	relaunch := switcherToolCtx(t, dir, "codex", "claude codex")
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("open_account_switcher tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q 2>/dev/null || true", rec), nil)
	assertNotContains(t, logOut, "respawn-pane")
}

// Picking a claude LOGIN while the pane runs another agent must switch back to
// claude under that login — the account rows are claude, whatever runs now.
func TestOpenAccountSwitcher_account_pick_on_other_agent_switches_to_claude(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, filepath.Join(dir, "claude-accounts", "personal"), ".keep", "")
	rec := filepath.Join(dir, "tmux.log")
	bin := switcherToolMockTmux(t, dir, "-WISP_DECK_CLAUDE_ACCOUNT", "personal", rec)
	mockSwitcherBinary(t, dir)
	relaunch := switcherToolCtx(t, dir, "codex", "claude codex")
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("open_account_switcher tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "respawn-pane")
	assertContains(t, logOut, "/opt/claude")
	assertContains(t, logOut, `CLAUDE_CONFIG_DIR="`+filepath.Join(dir, "claude-accounts", "personal")+`"`)
	assertContains(t, logOut, "set-environment WISP_DECK_TOOL claude")
	assertContains(t, logOut, "set-environment WISP_DECK_CLAUDE_ACCOUNT personal")
}

// One claude login but several agents: the pill must still show — it is the
// only way into the agent switcher.
func TestAccountPillEnabled_shows_with_one_account_but_two_tools(t *testing.T) {
	dir := t.TempDir()
	list := writeTempFile(t, dir, "claude-accounts.list", "")
	relaunch := writeTempFile(t, dir, "relaunch", "tool=claude\ntools=claude codex\n")
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("account_pill_enabled %q %q", relaunch, list)), nil)
	if code != 0 {
		t.Fatalf("expected pill enabled (exit 0), got %d: %s", code, out)
	}
}

// One login AND one tool: nothing to switch to — no pill.
func TestAccountPillEnabled_hidden_with_one_account_and_one_tool(t *testing.T) {
	dir := t.TempDir()
	list := writeTempFile(t, dir, "claude-accounts.list", "")
	relaunch := writeTempFile(t, dir, "relaunch", "tool=claude\ntools=claude\n")
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("account_pill_enabled %q %q", relaunch, list)), nil)
	if code == 0 {
		t.Fatalf("expected pill disabled (non-zero), got 0: %s", out)
	}
}

// The pill shows the running AGENT when it is not claude — its display name in
// the tool's accent color; for claude it keeps showing the account.
func TestPillCurrent_shows_agent_name_for_non_claude_tool(t *testing.T) {
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		`pill_current codex "" "" "" "" ""`), nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Codex\t36")
}

func TestPillCurrent_delegates_to_account_for_claude(t *testing.T) {
	dir := t.TempDir()
	list := writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	pointer := writeTempFile(t, dir, "claude-account", "work\n")
	colors := writeTempFile(t, dir, "claude-account-colors", "work:1\n")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	out, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		`pill_current claude %q %q %q %q`, pointer, list, defLabel, colors)), nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Work\t")
}

// The ledger must render the pill through the agent-aware helper, or an
// opencode/codex session's pill would claim a claude login.
func TestCompactView_renders_pill_via_pill_current(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "lib", "compact-view.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `pill_current "${_rc_tool`) {
		t.Fatal("compact-view.sh must build the pill via pill_current with the running tool")
	}
}
