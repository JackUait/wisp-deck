package bash_test

import (
	"fmt"
	"os"
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
    printf '%%s\n' "$@" >> "$log"
    printf '%%b' %q | grep -qF " $3" ;;
  *)
    printf '%%s\n' "$*" >> "$log" ;;
esac
`, dir, listSessions, envCase, listSessions)
	return mockCommand(t, dir, "tmux", body)
}

const stackEnvTwoApps = `
      "dev-app-111") all='WISP_DECK=1\nWISP_DECK_PATH=/tmp/app\nWISP_DECK_PROJECT=app\nWISP_DECK_TOOL=claude\n' ;;
      "dev-web-222") all='WISP_DECK=1\nWISP_DECK_PATH=/tmp/web\nWISP_DECK_PROJECT=web\nWISP_DECK_TOOL=claude\n' ;;
      "dev-app-333") all='WISP_DECK=1\nWISP_DECK_PATH=/tmp/app\nWISP_DECK_PROJECT=app\nWISP_DECK_TOOL=codex\n' ;;
`

// stackSnippet builds a bash snippet that sources session-stack.sh then runs body.
func stackSnippet(t *testing.T, body string) string {
	t.Helper()
	root := projectRoot(t)
	stackPath := filepath.Join(root, "lib", "session-stack.sh")
	return fmt.Sprintf("source %q && %s", stackPath, body)
}

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

func TestStackRegistry_add_list_remove_roundtrip(t *testing.T) {
	cfg := t.TempDir()
	body := fmt.Sprintf(`
stack_add %q "dev-app-1" "dev-app-1"
stack_add %q "dev-app-1" "dev-app-2"
stack_add %q "dev-app-1" "dev-app-2"   # idempotent
stack_list %q "dev-app-1"
echo ---
stack_remove_entry %q "dev-app-1" "dev-app-2"
stack_list %q "dev-app-1"
`, cfg, cfg, cfg, cfg, cfg, cfg)
	script := stackSnippet(t, body)
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
	body := fmt.Sprintf(`
mkdir -p %q/spare-zdotdir-%s
stack_session_files_cleanup %q %q
ls %q
`, cfg, s, cfg, s, cfg)
	script := stackSnippet(t, body)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	for _, f := range []string{"spare-", "relaunch-", "proxy-"} {
		assertNotContains(t, out, f)
	}
}

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
	body := fmt.Sprintf(`
stack_repaint %q %q "app" "/tmp/app"
cat %q/tmux.log
`, filepath.Join(bin, "tmux"), cfg, dir)
	script := stackSnippet(t, body)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "set-option -t dev-app-111 status-left")
	assertContains(t, out, "set-option -t dev-app-333 status-left")
	// dev-app-111's own bar must accent chip 1; dev-app-333's chip 2.
	for _, want := range []string{"bold] 1 ", "bold] 2 "} {
		assertContains(t, out, want)
	}
}

func TestStackCycle_switches_to_next_and_wraps(t *testing.T) {
	dir := t.TempDir()
	bin := mockTmux(t, dir, "100 dev-app-111\n300 dev-app-333\n", stackEnvTwoApps)
	body := fmt.Sprintf(`
stack_cycle %q "dev-app-333" next
cat %q/tmux.log
`, filepath.Join(bin, "tmux"), dir)
	script := stackSnippet(t, body)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "switch-client -t dev-app-111") // wraps from last to first
}

func TestStackCycle_single_session_is_noop(t *testing.T) {
	dir := t.TempDir()
	bin := mockTmux(t, dir, "200 dev-web-222\n", stackEnvTwoApps)
	body := fmt.Sprintf(`
stack_cycle %q "dev-web-222" next
cat %q/tmux.log 2>/dev/null || true
`, filepath.Join(bin, "tmux"), dir)
	script := stackSnippet(t, body)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "switch-client")
}

func TestStackCloseCurrent_switches_then_kills_and_deregisters(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir()
	bin := mockTmux(t, dir, "100 dev-app-111\n300 dev-app-333\n", stackEnvTwoApps)
	body := fmt.Sprintf(`
cleanup_tmux_session() { echo "CLEANUP:$1" >> %q/tmux.log ; }   # stub the heavy teardown, same log as tmux
stack_add %q "dev-app-111" "dev-app-111"
stack_add %q "dev-app-111" "dev-app-333"
stack_close_current %q %q "dev-app-333"
echo "STACK:$(stack_list %q dev-app-111 | tr '\n' ',')"
cat %q/tmux.log
`, dir, cfg, cfg, filepath.Join(bin, "tmux"), cfg, cfg, dir)
	script := stackSnippet(t, body)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "switch-client -t dev-app-111")
	assertContains(t, out, "CLEANUP:dev-app-333")
	assertContains(t, out, "STACK:dev-app-111,")
	assertNotContains(t, out, "dev-app-333,")

	logBytes, err := os.ReadFile(filepath.Join(dir, "tmux.log"))
	log := string(logBytes)
	if err != nil {
		t.Fatalf("reading tmux.log: %v", err)
	}
	switchIdx := strings.Index(log, "switch-client -t dev-app-111")
	cleanupIdx := strings.Index(log, "CLEANUP:dev-app-333")
	if switchIdx < 0 || cleanupIdx < 0 {
		t.Fatalf("expected both markers in tmux.log, got %q", log)
	}
	if switchIdx >= cleanupIdx {
		t.Errorf("expected switch-client BEFORE kill (session must not die out from under the client): switch@%d, cleanup@%d, log=%q", switchIdx, cleanupIdx, log)
	}
}

// TestStackCloseCurrent_last_session_skips_switch_and_repaint covers the
// spec's headline invariant: with zero neighbours the client dies with the
// session, so stack_close_current must NOT switch-client and must NOT
// repaint (there is nothing left to repaint) — but teardown must still run.
func TestStackCloseCurrent_last_session_skips_switch_and_repaint(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir()
	bin := mockTmux(t, dir, "200 dev-web-222\n", stackEnvTwoApps)
	body := fmt.Sprintf(`
cleanup_tmux_session() { echo "CLEANUP:$1" ; }   # stub the heavy teardown
stack_repaint() { echo "REPAINT-CALLED" ; }       # must never be invoked
stack_add %q "dev-web-222" "dev-web-222"
stack_close_current %q %q "dev-web-222"
echo "STACK:$(stack_list %q dev-web-222 | tr '\n' ',')"
cat %q/tmux.log 2>/dev/null || true
`, cfg, filepath.Join(bin, "tmux"), cfg, cfg, dir)
	script := stackSnippet(t, body)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "CLEANUP:dev-web-222")
	assertNotContains(t, out, "switch-client")
	assertNotContains(t, out, "REPAINT-CALLED")
	assertContains(t, out, "STACK:")
	assertNotContains(t, out, "STACK:dev-web-222") // deregistered: entry removed, list now empty
}

func TestStackRequest_write_then_claim_roundtrip(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir()
	proj := t.TempDir()
	env := fmt.Sprintf(`
      "dev-app-111") all='WISP_DECK=1\nWISP_DECK_PATH=%s\n' ;;
`, proj)
	bin := mockTmux(t, dir, "100 dev-app-111\n", env)
	body := fmt.Sprintf(`
restore_trigger_tab() { return 0; }   # stub the osascript Cmd+T
stack_request_new %q %q "dev-app-111" || echo "WRITE-FAILED"
stack_request_claim %q
stack_request_claim %q || echo "SECOND-CLAIM-FAILS"
`, filepath.Join(bin, "tmux"), cfg, cfg, cfg)
	script := stackSnippet(t, body)
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
	body := fmt.Sprintf(`
stack_request_claim %q || echo "STALE-REJECTED"
`, cfg)
	script := stackSnippet(t, body)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "STALE-REJECTED")
	assertNotContains(t, out, proj)
}

func TestStackRequestNew_failed_trigger_removes_request(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir()
	bin := mockTmux(t, dir, "100 dev-app-111\n", stackEnvTwoApps)
	body := fmt.Sprintf(`
restore_trigger_tab() { return 1; }
stack_request_new %q %q "dev-app-111" && echo "UNEXPECTED-OK"
[ -f %q/stack-request ] && echo "REQUEST-LEFT-BEHIND"
true
`, filepath.Join(bin, "tmux"), cfg, cfg)
	script := stackSnippet(t, body)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "UNEXPECTED-OK")
	assertNotContains(t, out, "REQUEST-LEFT-BEHIND")
}

func TestStackAdoptAll_registers_before_marking(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir()
	bin := mockTmux(t, dir, "100 dev-app-111\n", stackEnvTwoApps)
	// Wrap stack_add so it logs to the SAME tmux.log the mockTmux
	// set-environment lines land in, then still performs the real
	// registration. This lets the assertions below prove ORDER, not just
	// presence: an order-inverting regression (mark-then-register) would
	// still pass a presence-only check but fails the index comparison.
	script := fmt.Sprintf(`
cd %q
source lib/session-stack.sh
orig_stack_add="$(declare -f stack_add)"
eval "orig_${orig_stack_add}"
stack_add() { echo "STACK-ADD:$3" >> %q/tmux.log; orig_stack_add "$@"; }
stack_adopt_all %q %q "dev-app-999" "4242" "dev-app-111"
echo "STACK:$(stack_list %q dev-app-999 | tr '\n' ',')"
cat %q/tmux.log
`, projectRoot(t), dir, filepath.Join(bin, "tmux"), cfg, cfg, dir)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "STACK:dev-app-111,")
	assertContains(t, out, "set-environment -t dev-app-111 WISP_DECK_ADOPTED_BY dev-app-999")
	assertContains(t, out, "set-environment -t dev-app-111 WISP_DECK_OWNER_PID 4242")

	logBytes, err := os.ReadFile(filepath.Join(dir, "tmux.log"))
	if err != nil {
		t.Fatalf("reading tmux.log: %v", err)
	}
	log := string(logBytes)
	addIdx := strings.Index(log, "STACK-ADD:dev-app-111")
	markIdx := strings.Index(log, "set-environment -t dev-app-111 WISP_DECK_ADOPTED_BY")
	if addIdx < 0 || markIdx < 0 {
		t.Fatalf("expected both markers in tmux.log, got %q", log)
	}
	if addIdx >= markIdx {
		t.Errorf("expected stack_add BEFORE the adopted-by marker (no-zombie invariant): add@%d, mark@%d, log=%q", addIdx, markIdx, log)
	}
}

func TestStackAdoptedAway_true_only_when_marker_set(t *testing.T) {
	dir := t.TempDir()
	envCase := `
      "dev-app-111") all='WISP_DECK=1\nWISP_DECK_PATH=/tmp/app\nWISP_DECK_ADOPTED_BY=dev-app-999\n' ;;
      "dev-app-333") all='WISP_DECK=1\nWISP_DECK_PATH=/tmp/app\n' ;;
`
	bin := mockTmux(t, dir, "100 dev-app-111\n300 dev-app-333\n", envCase)
	script := fmt.Sprintf(`
cd %q
source lib/session-stack.sh
stack_adopted_away %q "dev-app-111" && echo "111-ADOPTED"
stack_adopted_away %q "dev-app-333" || echo "333-OWNED"
`, projectRoot(t), filepath.Join(bin, "tmux"), filepath.Join(bin, "tmux"))
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
cd %q
source lib/session-stack.sh
cleanup_tmux_session() { echo "CLEANUP:$1"; }
attention_cleanup() { echo "ATTENTION:$1"; }
stack_add %q "dev-owner-9" "dev-owner-9"
stack_add %q "dev-owner-9" "dev-app-111"
stack_add %q "dev-owner-9" "dev-app-333"
stack_owner_teardown %q %q "dev-owner-9"
[ -f %q/stacks/dev-owner-9 ] && echo "STACKFILE-LEFT"
true
`, projectRoot(t), cfg, cfg, cfg, filepath.Join(bin, "tmux"), cfg, cfg)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "CLEANUP:dev-app-111")     // owned → killed
	assertContains(t, out, "ATTENTION:/tmp/att-111")  // root read from env before kill
	assertNotContains(t, out, "CLEANUP:dev-app-333")  // adopted by dev-app-777 → skipped
	assertNotContains(t, out, "CLEANUP:dev-owner-9")  // own session left to wrapper
	assertNotContains(t, out, "STACKFILE-LEFT")
}

func TestStackReapOrphans_two_strikes_then_kill(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir()
	envCase := `
      "dev-app-111") all='WISP_DECK=1\nWISP_DECK_PATH=/tmp/app\nWISP_DECK_OWNER_PID=999999\n' ;;
`
	bin := mockTmux(t, dir, "100 dev-app-111\n", envCase) // pid 999999: guaranteed dead
	script := fmt.Sprintf(`
cd %q
source lib/session-stack.sh
cleanup_tmux_session() { echo "CLEANUP:$1"; }
stack_reap_orphans %q %q
echo "AFTER-FIRST"
stack_reap_orphans %q %q
`, projectRoot(t), filepath.Join(bin, "tmux"), cfg, filepath.Join(bin, "tmux"), cfg)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	first := out[:strings.Index(out, "AFTER-FIRST")]
	assertNotContains(t, first, "CLEANUP:")            // strike one: marked only
	assertContains(t, out, "CLEANUP:dev-app-111")      // strike two: reaped
}

func TestStackReapOrphans_live_owner_never_reaped(t *testing.T) {
	cfg := t.TempDir()
	// Spawn a long-lived process so we have a live PID to embed
	script := fmt.Sprintf(`
sleep 300 &
live_pid=$!
trap "kill $live_pid 2>/dev/null || true" EXIT

cd %q
source lib/session-stack.sh

# Mock tmux function that returns env with the live PID
tmux_mock() {
  case "$1" in
    list-sessions)
      printf '100 dev-app-111\n' ;;
    show-environment)
      case "$3" in
        dev-app-111)
          printf '%%b' "WISP_DECK=1\nWISP_DECK_PATH=/tmp/app\nWISP_DECK_OWNER_PID=$live_pid\n" ;;
      esac ;;
    has-session)
      printf '100 dev-app-111\n' | grep -qF " $3" ;;
  esac
}

cleanup_tmux_session() { echo "CLEANUP:$1"; }
stack_reap_orphans tmux_mock %q
stack_reap_orphans tmux_mock %q
`, projectRoot(t), cfg, cfg)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "CLEANUP:")
}

func TestStackReapOrphans_ignores_sessions_without_owner_pid(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir()
	bin := mockTmux(t, dir, "100 dev-app-111\n", stackEnvTwoApps) // no OWNER_PID in env
	script := fmt.Sprintf(`
cd %q
source lib/session-stack.sh
cleanup_tmux_session() { echo "CLEANUP:$1"; }
stack_reap_orphans %q %q
stack_reap_orphans %q %q
`, projectRoot(t), filepath.Join(bin, "tmux"), cfg, filepath.Join(bin, "tmux"), cfg)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "CLEANUP:")
}
