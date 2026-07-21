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

// TestStackSessionsForProject_handles_spaces_in_session_and_project_names
// guards the invariant documented at the top of the file: session names (and
// the WISP_DECK_PATH values they carry) may contain spaces, so the matcher
// must iterate line-wise, never word-split. A regression that switched to
// word-splitting would silently drop this session from the stack.
func TestStackSessionsForProject_handles_spaces_in_session_and_project_names(t *testing.T) {
	dir := t.TempDir()
	proj := "/tmp/my app"
	envCase := fmt.Sprintf(`
      "dev-my app-42") all='WISP_DECK=1\nWISP_DECK_PATH=%s\nWISP_DECK_PROJECT=my app\nWISP_DECK_TOOL=claude\n' ;;
`, proj)
	bin := mockTmux(t, dir, "100 dev-my app-42\n", envCase)
	out, code := runBashFunc(t, "lib/session-stack.sh", "stack_sessions_for_project",
		[]string{filepath.Join(bin, "tmux"), proj}, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "dev-my app-42" {
		t.Errorf("got %q, want the space-containing session name intact", got)
	}
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

// TestStackCycle_prev_wraps_from_first_to_last covers the "prev" direction:
// pressing prev on the FIRST session of the stack must wrap around to the
// LAST one, mirroring the "next" wraparound already covered above.
func TestStackCycle_prev_wraps_from_first_to_last(t *testing.T) {
	dir := t.TempDir()
	// Three same-project sessions so next vs. prev from the first session
	// disagree (with only two, both directions land on the same neighbour
	// and the test can't tell a swapped direction from a correct one).
	envCase := `
      "dev-app-111") all='WISP_DECK=1\nWISP_DECK_PATH=/tmp/app\n' ;;
      "dev-app-222") all='WISP_DECK=1\nWISP_DECK_PATH=/tmp/app\n' ;;
      "dev-app-333") all='WISP_DECK=1\nWISP_DECK_PATH=/tmp/app\n' ;;
`
	bin := mockTmux(t, dir, "100 dev-app-111\n200 dev-app-222\n300 dev-app-333\n", envCase)
	body := fmt.Sprintf(`
stack_cycle %q "dev-app-111" prev
cat %q/tmux.log
`, filepath.Join(bin, "tmux"), dir)
	script := stackSnippet(t, body)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "switch-client -t dev-app-333") // wraps from first to last
	assertNotContains(t, out, "switch-client -t dev-app-222")
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

// stack_adopted_away survives the adoption-handoff removal as an
// upgrade-boundary defense: a still-running picker wrapper from the adoption
// era can adopt a NEW wrapper's session (its in-memory code, not this repo's),
// and the new wrapper's cleanup must then not kill the taken-over session.
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

// TestStackOwnerTeardown_kills_entry_adopted_by_self covers a value the
// adopted-away skip must NOT trip on: an entry whose WISP_DECK_ADOPTED_BY
// equals the OWNER doing the teardown (its own most-recent adoption, or a
// session that was adopted back into the same tab). Only a DIFFERENT owner's
// mark should protect a session; self-marks must still be killed, or the
// session leaks forever once its stack file is removed at the end of
// teardown.
func TestStackOwnerTeardown_kills_entry_adopted_by_self(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir()
	envCase := `
      "dev-app-111") all='WISP_DECK=1\nWISP_DECK_PATH=/tmp/app\nWISP_DECK_ADOPTED_BY=dev-owner-9\n' ;;
`
	bin := mockTmux(t, dir, "100 dev-app-111\n", envCase)
	script := fmt.Sprintf(`
cd %q
source lib/session-stack.sh
cleanup_tmux_session() { echo "CLEANUP:$1"; }
stack_add %q "dev-owner-9" "dev-owner-9"
stack_add %q "dev-owner-9" "dev-app-111"
stack_owner_teardown %q %q "dev-owner-9"
`, projectRoot(t), cfg, cfg, filepath.Join(bin, "tmux"), cfg)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "CLEANUP:dev-app-111") // adopted by SELF must still be killed
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

func TestStackReapOrphans_live_owner_clears_stale_mark(t *testing.T) {
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

# Simulate a prior strike from a launch race: session already marked.
mkdir -p %q/stacks
printf 'dev-app-111\n' > %q/stacks/.reap-marks

stack_reap_orphans tmux_mock %q

if grep -qxF "dev-app-111" %q/stacks/.reap-marks 2>/dev/null; then
  echo "MARKS:still-present"
else
  echo "MARKS:cleared"
fi
`, projectRoot(t), cfg, cfg, cfg, cfg)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "CLEANUP:")
	assertContains(t, out, "MARKS:cleared")
}

// TestStackReapOrphans_mixed_table_only_reaps_the_valid_dead_owner exercises
// three sessions side by side: one reapable dead-owner (numeric PID,
// guaranteed dead), one with a garbage WISP_DECK_OWNER_PID that must never
// crash `kill -0` or be treated as reapable, and one non-wisp session that
// must be skipped before either check runs. Only the first gets CLEANUP,
// and only after two strikes.
func TestStackReapOrphans_mixed_table_only_reaps_the_valid_dead_owner(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir()
	envCase := `
      "dev-app-dead") all='WISP_DECK=1\nWISP_DECK_PATH=/tmp/app\nWISP_DECK_OWNER_PID=999999\n' ;;
      "dev-app-garbage") all='WISP_DECK=1\nWISP_DECK_PATH=/tmp/app\nWISP_DECK_OWNER_PID=abc\n' ;;
      "dev-not-wisp") all='SOME_OTHER_VAR=1\n' ;;
`
	bin := mockTmux(t, dir,
		"100 dev-app-dead\n200 dev-app-garbage\n300 dev-not-wisp\n", envCase)
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
	assertNotContains(t, first, "CLEANUP:") // strike one on the dead owner: marked only
	assertContains(t, out, "CLEANUP:dev-app-dead")
	assertNotContains(t, out, "CLEANUP:dev-app-garbage")
	assertNotContains(t, out, "CLEANUP:dev-not-wisp")
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

// TestStackReapOrphans_prunes_dead_owner_registry_files covers the reaper's
// second job: the per-owner stack registry file itself is a leak if the
// owning tab's session is gone (crashed wrapper, killed session) — nothing
// else ever cleans up $cfg/stacks/<owner>. A live owner's file must survive.
func TestStackReapOrphans_prunes_dead_owner_registry_files(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir()
	// dev-app-111 is the only live session; dev-owner-dead and dev-owner-live
	// are stack *registry files* (owner session names), not live sessions.
	bin := mockTmux(t, dir, "100 dev-app-111\n300 dev-owner-live\n", stackEnvTwoApps)
	if err := os.MkdirAll(filepath.Join(cfg, "stacks"), 0755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, filepath.Join(cfg, "stacks"), "dev-owner-dead", "dev-app-111\n")
	writeTempFile(t, filepath.Join(cfg, "stacks"), "dev-owner-live", "dev-app-111\n")
	writeTempFile(t, filepath.Join(cfg, "stacks"), ".reap-marks", "dev-app-111\n")
	script := fmt.Sprintf(`
cd %q
source lib/session-stack.sh
stack_reap_orphans %q %q
ls -a %q/stacks
`, projectRoot(t), filepath.Join(bin, "tmux"), cfg, cfg)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "dev-owner-dead")
	assertContains(t, out, "dev-owner-live")
	assertContains(t, out, ".reap-marks") // never treated as an owner file
}

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
