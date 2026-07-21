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
