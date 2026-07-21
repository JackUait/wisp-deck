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
