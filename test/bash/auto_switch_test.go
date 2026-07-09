package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The auto-switch setting turns on in-place account rotation: when a session's
// quota usage reaches the threshold, the AI pane is relaunched under the next
// pooled account (the same mid-session switch the ledger pill drives) and the
// conversation is continued automatically. Stored as a single-value flag file
// (on/off) and only eligible when the account list has at least two accounts
// to rotate between.

func TestAutoSwitch_defaults_off(t *testing.T) {
	dir := t.TempDir()
	out, code := runBashFunc(t, "lib/auto-switch.sh", "get_auto_switch",
		[]string{filepath.Join(dir, "auto-switch-accounts")}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "off" {
		t.Fatalf("default = %q, want off", strings.TrimSpace(out))
	}
}

func TestAutoSwitch_set_and_get(t *testing.T) {
	dir := t.TempDir()
	flag := filepath.Join(dir, "auto-switch-accounts")
	if _, code := runBashFunc(t, "lib/auto-switch.sh", "set_auto_switch", []string{flag, "on"}, nil); code != 0 {
		t.Fatal("set on failed")
	}
	out, _ := runBashFunc(t, "lib/auto-switch.sh", "get_auto_switch", []string{flag}, nil)
	if strings.TrimSpace(out) != "on" {
		t.Fatalf("got %q, want on", strings.TrimSpace(out))
	}
	// is_auto_switch_enabled returns 0 when on.
	if _, code := runBashFunc(t, "lib/auto-switch.sh", "is_auto_switch_enabled", []string{flag}, nil); code != 0 {
		t.Fatal("is_auto_switch_enabled should exit 0 when on")
	}
}

func TestAutoSwitch_invalid_value_normalizes_to_off(t *testing.T) {
	dir := t.TempDir()
	flag := filepath.Join(dir, "auto-switch-accounts")
	if _, code := runBashFunc(t, "lib/auto-switch.sh", "set_auto_switch", []string{flag, "garbage"}, nil); code != 0 {
		t.Fatal("set failed")
	}
	out, _ := runBashFunc(t, "lib/auto-switch.sh", "get_auto_switch", []string{flag}, nil)
	if strings.TrimSpace(out) != "off" {
		t.Fatalf("invalid value should read as off, got %q", strings.TrimSpace(out))
	}
}

func TestProxyStartupParse_extracts_port_and_key(t *testing.T) {
	line := `{"port":54321,"key":"wd-abc123"}`
	out, code := runBashFunc(t, "lib/auto-switch.sh", "proxy_startup_port", []string{line}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "54321" {
		t.Errorf("port = %q, want 54321", strings.TrimSpace(out))
	}
	out, code = runBashFunc(t, "lib/auto-switch.sh", "proxy_startup_key", []string{line}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "wd-abc123" {
		t.Errorf("key = %q, want wd-abc123", strings.TrimSpace(out))
	}
}

func TestProxyStartupParse_empty_on_garbage(t *testing.T) {
	out, _ := runBashFunc(t, "lib/auto-switch.sh", "proxy_startup_port", []string{"not json"}, nil)
	if strings.TrimSpace(out) != "" {
		t.Errorf("garbage should yield empty port, got %q", strings.TrimSpace(out))
	}
}

func TestProxyStartupParse_extracts_ca_path(t *testing.T) {
	line := `{"port":54321,"key":"wd-abc","ca":"/cfg/wisp-deck/wisp-deck-ca.pem"}`
	out, code := runBashFunc(t, "lib/auto-switch.sh", "proxy_startup_ca", []string{line}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "/cfg/wisp-deck/wisp-deck-ca.pem" {
		t.Errorf("ca = %q", strings.TrimSpace(out))
	}
	// base-URL mode omits ca → empty.
	out, _ = runBashFunc(t, "lib/auto-switch.sh", "proxy_startup_ca", []string{`{"port":1,"key":"k"}`}, nil)
	if strings.TrimSpace(out) != "" {
		t.Errorf("missing ca should be empty, got %q", strings.TrimSpace(out))
	}
}

func TestAutoSwitchEligible_needs_two_accounts(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")

	// Zero managed accounts → not eligible: only the implicit Default exists,
	// nothing to rotate to.
	writeTempFile(t, dir, "claude-accounts.list", "# just a comment\n\n")
	if _, code := runBashFunc(t, "lib/auto-switch.sh", "auto_switch_eligible", []string{list}, nil); code == 0 {
		t.Fatal("0 managed accounts should not be eligible")
	}

	// One managed account → eligible: it plus the implicit Default (Keychain)
	// login are two accounts the in-place switch can rotate between.
	writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	if _, code := runBashFunc(t, "lib/auto-switch.sh", "auto_switch_eligible", []string{list}, nil); code != 0 {
		t.Fatal("1 managed account (+ implicit Default) should be eligible")
	}

	// Missing list → not eligible.
	if _, code := runBashFunc(t, "lib/auto-switch.sh", "auto_switch_eligible",
		[]string{filepath.Join(dir, "missing.list")}, nil); code == 0 {
		t.Fatal("missing list should not be eligible")
	}
}

// --- In-place auto-switch: threshold ---

func TestAutoSwitchThresholdReached_at_98(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int // exit code: 0 = reached
	}{
		{"below", []string{"97"}, 1},
		{"at threshold", []string{"98"}, 0},
		{"above", []string{"100"}, 0},
		{"empty", []string{""}, 1},
		{"non-numeric", []string{"abc"}, 1},
		{"second window reaches", []string{"50", "99"}, 0},
		{"no args", nil, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, code := runBashFunc(t, "lib/auto-switch.sh", "auto_switch_threshold_reached", tt.args, nil)
			if (code == 0) != (tt.want == 0) {
				t.Errorf("args %v: exit = %d, want %d", tt.args, code, tt.want)
			}
		})
	}
}

// --- In-place auto-switch: rotation order ---

func TestAutoSwitchNextAccount_cycles_list(t *testing.T) {
	dir := t.TempDir()
	list := writeTempFile(t, dir, "claude-accounts.list",
		"# comment\nWork:work\nPersonal:personal\nPlay:play\n")
	tests := []struct {
		current string
		want    string
	}{
		{"work", "personal"},
		{"personal", "play"},
		{"play", "default"}, // last managed wraps to the Default login
		{"", "work"},        // Default login -> first managed
		{"default", "work"}, // ...same, spelled out
		{"unknown", "work"}, // dir gone from the list -> restart at first managed
	}
	for _, tt := range tests {
		out, code := runBashFunc(t, "lib/auto-switch.sh", "auto_switch_next_account",
			[]string{list, tt.current}, nil)
		assertExitCode(t, code, 0)
		if strings.TrimSpace(out) != tt.want {
			t.Errorf("next after %q = %q, want %q", tt.current, strings.TrimSpace(out), tt.want)
		}
	}
}

func TestAutoSwitchNextAccount_single_managed_rotates_with_default(t *testing.T) {
	dir := t.TempDir()
	list := writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	// One managed login still rotates: work <-> Default.
	out, code := runBashFunc(t, "lib/auto-switch.sh", "auto_switch_next_account",
		[]string{list, "work"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "default" {
		t.Fatalf("next after the only managed login = %q, want default", strings.TrimSpace(out))
	}
	out, code = runBashFunc(t, "lib/auto-switch.sh", "auto_switch_next_account",
		[]string{list, ""}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "work" {
		t.Fatalf("next after Default = %q, want work", strings.TrimSpace(out))
	}
}

func TestAutoSwitchNextAccount_fails_without_alternative(t *testing.T) {
	dir := t.TempDir()
	list := writeTempFile(t, dir, "claude-accounts.list", "# empty\n")
	out, code := runBashFunc(t, "lib/auto-switch.sh", "auto_switch_next_account",
		[]string{list, ""}, nil)
	if code == 0 {
		t.Fatalf("empty list should fail, got %q", out)
	}
}

// --- In-place auto-switch: once-per-window guard ---

func TestAutoSwitchGuard_fires_once_per_window(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "auto-switch-state")

	// First trip for an account passes and records it.
	if _, code := runBashFunc(t, "lib/auto-switch.sh", "auto_switch_guard",
		[]string{state, "work", "1000000"}, nil); code != 0 {
		t.Fatal("first guard pass should exit 0")
	}
	// A repeat inside the 5h window is suppressed.
	if _, code := runBashFunc(t, "lib/auto-switch.sh", "auto_switch_guard",
		[]string{state, "work", "1000100"}, nil); code == 0 {
		t.Fatal("second guard pass within the window should be suppressed")
	}
	// A different account is independent.
	if _, code := runBashFunc(t, "lib/auto-switch.sh", "auto_switch_guard",
		[]string{state, "personal", "1000100"}, nil); code != 0 {
		t.Fatal("another account should pass")
	}
	// After the 5h window rolls, the account may fire again.
	if _, code := runBashFunc(t, "lib/auto-switch.sh", "auto_switch_guard",
		[]string{state, "work", "1018001"}, nil); code != 0 {
		t.Fatal("expired entry should pass again")
	}
}

// --- In-place auto-switch: statusline trigger ---

// autoSwitchTriggerEnv builds a config root + relaunch file + mocked tmux for
// auto_switch_maybe_trigger and returns (env, tmux log path, cfg root).
func autoSwitchTriggerEnv(t *testing.T, dir, flagValue string) ([]string, string, string) {
	t.Helper()
	root := projectRoot(t)
	cfg := filepath.Join(dir, "cfg")
	writeTempFile(t, filepath.Join(cfg, "wisp-deck"), "auto-switch-accounts", flagValue+"\n")
	writeTempFile(t, filepath.Join(cfg, "wisp-deck"), "claude-accounts.list",
		"Work:work\nPersonal:personal\n")
	relaunch := writeTempFile(t, dir, "relaunch", "tool=claude\n")
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`printf '%%s\n' "$*" >> %q`, rec))
	env := buildEnv(t, []string{bin},
		"HOME="+dir,
		"TMUX=/tmp/fake,1,0",
		"XDG_CONFIG_HOME="+cfg,
		"WISP_DECK_RELAUNCH_FILE="+relaunch,
		"WISP_DECK_LIB_DIR="+filepath.Join(root, "lib"),
		"CLAUDE_CONFIG_DIR="+filepath.Join(cfg, "wisp-deck", "claude-accounts", "work"),
	)
	return env, rec, cfg
}

func TestAutoSwitchMaybeTrigger_fires_in_place_switch(t *testing.T) {
	dir := t.TempDir()
	env, rec, _ := autoSwitchTriggerEnv(t, dir, "on")
	_, code := runBashFunc(t, "lib/auto-switch.sh", "auto_switch_maybe_trigger",
		[]string{"98", "40"}, env)
	assertExitCode(t, code, 0)
	logOut, _ := os.ReadFile(rec)
	s := string(logOut)
	assertContains(t, s, "run-shell -b")
	assertContains(t, s, "auto_switch_relaunch")
	assertContains(t, s, "personal")
}

func TestAutoSwitchMaybeTrigger_noop_below_threshold(t *testing.T) {
	dir := t.TempDir()
	env, rec, _ := autoSwitchTriggerEnv(t, dir, "on")
	_, code := runBashFunc(t, "lib/auto-switch.sh", "auto_switch_maybe_trigger",
		[]string{"97", "42"}, env)
	assertExitCode(t, code, 0)
	if data, _ := os.ReadFile(rec); len(data) > 0 {
		t.Fatalf("below threshold must not touch tmux, got:\n%s", data)
	}
}

func TestAutoSwitchMaybeTrigger_noop_when_flag_off(t *testing.T) {
	dir := t.TempDir()
	env, rec, _ := autoSwitchTriggerEnv(t, dir, "off")
	_, code := runBashFunc(t, "lib/auto-switch.sh", "auto_switch_maybe_trigger",
		[]string{"99", ""}, env)
	assertExitCode(t, code, 0)
	if data, _ := os.ReadFile(rec); len(data) > 0 {
		t.Fatalf("flag off must not touch tmux, got:\n%s", data)
	}
}

func TestAutoSwitchMaybeTrigger_fires_only_once_per_account(t *testing.T) {
	dir := t.TempDir()
	env, rec, _ := autoSwitchTriggerEnv(t, dir, "on")
	snippet := fmt.Sprintf(
		"source %q && auto_switch_maybe_trigger 99 40 && auto_switch_maybe_trigger 99 40",
		filepath.Join(projectRoot(t), "lib", "auto-switch.sh"))
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	logOut, _ := os.ReadFile(rec)
	if n := strings.Count(string(logOut), "run-shell"); n != 1 {
		t.Fatalf("expected exactly 1 run-shell, got %d:\n%s", n, logOut)
	}
}

// --- In-place auto-switch: the relaunch + auto-continue ---

func TestSendContinueMessage_types_text_then_enter(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`printf '%%s\n' "$*" >> %q`, rec))
	env := buildEnv(t, []string{bin})
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		"send_continue_message tmux %1"), env)
	assertExitCode(t, code, 0)
	logOut, _ := os.ReadFile(rec)
	s := string(logOut)
	textIdx := strings.Index(s, "send-keys -t %1 -l continue")
	enterIdx := strings.Index(s, "send-keys -t %1 Enter")
	if textIdx == -1 || enterIdx == -1 || textIdx > enterIdx {
		t.Fatalf("expected literal continue text then Enter, log:\n%s", s)
	}
}

// autoSwitchRelaunchCtx writes a relaunch context + accounts dir and returns
// the relaunch file path.
func autoSwitchRelaunchCtx(t *testing.T, dir string) string {
	t.Helper()
	accountsDir := filepath.Join(dir, "claude-accounts")
	writeTempFile(t, filepath.Join(accountsDir, "personal"), ".keep", "")
	writeTempFile(t, dir, "claude-accounts.list", "Work:work\nPersonal:personal\n")
	return writeTempFile(t, dir, "relaunch", strings.Join([]string{
		"tool=claude", "tool_cmd=claude",
		"settings=", "filter=", "project_dir=/proj",
		"accounts_dir=" + accountsDir,
		"pointer=" + filepath.Join(dir, "claude-account"),
		"list=" + filepath.Join(dir, "claude-accounts.list"),
		"colors=" + filepath.Join(dir, "claude-account-colors"),
		"default_label=" + filepath.Join(dir, "claude-account-default-label"),
		"",
	}, "\n"))
}

func TestAutoSwitchRelaunch_respawns_under_target_then_continues(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	relaunch := autoSwitchRelaunchCtx(t, dir)
	// The pane's input line is empty ("❯ ") throughout: the stash fast-skips,
	// and after the respawn the same frame reads as the ready prompt.
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "list-panes" ]; then printf '%%s\n' "%%1 1"; exit 0; fi
if [ "$1" = "capture-pane" ]; then printf '%%s\n' "❯ "; exit 0; fi
if [ "$1" = "show-environment" ]; then exit 1; fi
printf '%%s\n' "$*" >> %q`, rec))
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("auto_switch_relaunch tmux %q personal; wait", relaunch)), env)
	assertExitCode(t, code, 0)

	var s string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		logOut, _ := os.ReadFile(rec)
		s = string(logOut)
		if strings.Contains(s, "send-keys -t %1 Enter") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	respawn := strings.Index(s, "respawn-pane")
	cont := strings.Index(s, "send-keys -t %1 -l continue")
	enter := strings.Index(s, "send-keys -t %1 Enter")
	if respawn == -1 {
		t.Fatalf("expected respawn-pane, log:\n%s", s)
	}
	assertContains(t, s, `CLAUDE_CONFIG_DIR="`+filepath.Join(dir, "claude-accounts", "personal")+`"`)
	if cont == -1 || enter == -1 || cont < respawn || enter < cont {
		t.Fatalf("expected continue message after respawn, log:\n%s", s)
	}
}

func TestAutoSwitchRelaunch_skips_when_target_is_current(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	relaunch := autoSwitchRelaunchCtx(t, dir)
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "list-panes" ]; then printf '%%s\n' "%%1 1"; exit 0; fi
if [ "$1" = "show-environment" ]; then echo "WISP_DECK_CLAUDE_ACCOUNT=personal"; exit 0; fi
printf '%%s\n' "$*" >> %q`, rec))
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("auto_switch_relaunch tmux %q personal; wait", relaunch)), env)
	assertExitCode(t, code, 0)
	if logOut, _ := os.ReadFile(rec); strings.Contains(string(logOut), "respawn-pane") {
		t.Fatalf("must not respawn when already on target, log:\n%s", logOut)
	}
}

func TestAutoSwitchRelaunch_skips_when_default_target_and_already_default(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	relaunch := autoSwitchRelaunchCtx(t, dir)
	// The session env stamps an EMPTY account = the Default login.
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "list-panes" ]; then printf '%%s\n' "%%1 1"; exit 0; fi
if [ "$1" = "show-environment" ]; then echo "WISP_DECK_CLAUDE_ACCOUNT="; exit 0; fi
printf '%%s\n' "$*" >> %q`, rec))
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("auto_switch_relaunch tmux %q default; wait", relaunch)), env)
	assertExitCode(t, code, 0)
	if logOut, _ := os.ReadFile(rec); strings.Contains(string(logOut), "respawn-pane") {
		t.Fatalf("must not respawn when already on the Default login, log:\n%s", logOut)
	}
}

// The single-managed-account setup (one login + the implicit Default) must
// trigger too — that IS the common case the in-place switch exists for.
func TestAutoSwitchMaybeTrigger_single_managed_account_rotates_to_default(t *testing.T) {
	dir := t.TempDir()
	env, rec, cfg := autoSwitchTriggerEnv(t, dir, "on")
	if err := os.WriteFile(filepath.Join(cfg, "wisp-deck", "claude-accounts.list"),
		[]byte("Personal:personal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The pane runs the managed login; rotation lands on the Default.
	for i, e := range env {
		if strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") {
			env[i] = "CLAUDE_CONFIG_DIR=" + filepath.Join(cfg, "wisp-deck", "claude-accounts", "personal")
		}
	}
	_, code := runBashFunc(t, "lib/auto-switch.sh", "auto_switch_maybe_trigger",
		[]string{"99", ""}, env)
	assertExitCode(t, code, 0)
	logOut, _ := os.ReadFile(rec)
	s := string(logOut)
	assertContains(t, s, "run-shell -b")
	assertContains(t, s, "auto_switch_relaunch")
	assertContains(t, s, "default")
}

// --- Rework guards: the wrapper no longer launches the rotation proxy, and the
// statusline hook drives the in-place switch instead. ---

func TestWrapper_does_not_launch_rotation_proxy(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "wisp-deck-tui proxy") {
		t.Fatal("wrapper.sh must not launch the rotation proxy — auto-switch is in-place now")
	}
	if strings.Contains(string(data), `-z "$WISP_DECK_PROXY_PORT"`) {
		t.Fatal("relaunch context must be written for every claude session, not gated on the proxy")
	}
}

func TestStatuslineWrapper_hooks_auto_switch_trigger(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "templates", "statusline-wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(data), "auto_switch_maybe_trigger")
}
