package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// In-place stack sessions: prefix+S runs `wrapper.sh --stack-new <owner>
// <client>` (backgrounded by run-shell -b). The builder constructs a full
// session INSIDE the current tab's stack — registered to the owner wrapper,
// switched to via switch-client — with no new Ghostty tab, no adoption
// handoff, and no attach. The old Cmd+T request/claim dance is gone: it
// detached pre-stacking wrappers' clients, whose in-memory cleanup killed
// their own just-adopted sessions ("stacking closes same-project sessions").

// stackNewTmuxMock records every invocation and answers the queries the
// --stack-new path needs: the owner session's env, the owner client's size,
// and the new-session -P pane-id prints.
func stackNewTmuxMock(projDir string) string {
	return fmt.Sprintf(`#!/bin/bash
log="$HOME/tmux.log"
printf '%%s\n' "$*" >> "$log"
case "$1" in
  new-session) printf '%%s\n' '%%0' '%%1' ;;
  show-environment)
    # $2=-t $3=<session> $4=<var>
    case "${4:-}" in
      WISP_DECK_PATH) echo "WISP_DECK_PATH=%s" ;;
      WISP_DECK_OWNER_PID) echo "WISP_DECK_OWNER_PID=4242" ;;
      *) exit 1 ;;
    esac ;;
  list-clients) echo "173 47" ;;
  list-sessions) : ;;
  capture-pane) echo ">" ;;
esac
exit 0
`, projDir)
}

func runStackNewWrapper(t *testing.T, tmuxMock string, args []string) (home string, log string, code int) {
	t.Helper()
	home = t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	mocks := map[string]string{
		"tmux":          tmuxMock,
		"claude":        "#!/bin/bash\nexit 0\n",
		"wisp-deck-tui": "#!/bin/bash\nexit 0\n",
		"sysctl":        "#!/bin/bash\necho \"{ sec = 12345, usec = 1 } Thu Jul  2 01:01:01 2026\"\n",
	}
	for name, body := range mocks {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0755); err != nil {
			t.Fatalf("write mock %s: %v", name, err)
		}
	}
	env := buildEnv(t, nil, "HOME="+home)
	_, code = runBashScript(t, "wrapper.sh", args, env)
	data, _ := os.ReadFile(filepath.Join(home, "tmux.log"))
	return home, string(data), code
}

// The success path: a full session is built for the owner's project, handed to
// the owner (registered in its stack file, owner-pid restamped), the stack is
// switched to it, and the builder exits 0 without attaching.
func TestWrapperStackNew_builds_inplace_session_registered_to_owner(t *testing.T) {
	projParent := t.TempDir()
	projDir := filepath.Join(projParent, "app")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}

	home, log, code := runStackNewWrapper(t, stackNewTmuxMock(projDir),
		[]string{"--stack-new", "dev-app-111", "client0"})
	assertExitCode(t, code, 0)

	if !strings.Contains(log, "new-session -d") {
		t.Fatalf("no session was built; tmux log:\n%s", log)
	}
	if !strings.Contains(log, "-e WISP_DECK_PATH="+projDir) {
		t.Errorf("session not stamped with the owner's project dir; tmux log:\n%s", log)
	}
	if strings.Contains(log, "attach-session") {
		t.Errorf("--stack-new must never attach (it runs from run-shell, no tty); tmux log:\n%s", log)
	}
	if !strings.Contains(log, "switch-client -c client0") {
		t.Errorf("the pressing client must be switched to the new session; tmux log:\n%s", log)
	}
	if !strings.Contains(log, "WISP_DECK_OWNER_PID 4242") {
		t.Errorf("ownership must be restamped to the owner wrapper's pid; tmux log:\n%s", log)
	}

	stackFile := filepath.Join(home, ".config", "wisp-deck", "stacks", "dev-app-111")
	data, err := os.ReadFile(stackFile)
	if err != nil {
		t.Fatalf("new session was not registered in the owner's stack file: %v", err)
	}
	if !strings.Contains(string(data), "dev-app-") {
		t.Errorf("owner stack file does not list the new session: %q", string(data))
	}
}

// Ownership handoff ordering mirrors stack_adopt_all: the session is
// registered in the owner's stack file BEFORE the owner-pid restamp, so a
// crash between the two leaves it doubly covered (builder cleanup + owner),
// never orphaned. Observable as: the restamp call happens, and by then the
// stack file exists — so a log that shows the restamp but no stack file is
// the ordering violation.
func TestWrapperStackNew_missing_owner_env_fails_without_side_effects(t *testing.T) {
	// show-environment answers nothing: the owner session is gone (or
	// pre-dates the env stamps). The builder must exit non-zero, build no
	// session and register nothing.
	deadOwnerMock := `#!/bin/bash
log="$HOME/tmux.log"
printf '%s\n' "$*" >> "$log"
case "$1" in
  show-environment) exit 1 ;;
esac
exit 0
`
	home, log, code := runStackNewWrapper(t, deadOwnerMock,
		[]string{"--stack-new", "dev-gone-999", "client0"})
	if code == 0 {
		t.Fatalf("--stack-new with a dead owner must fail, got exit 0; tmux log:\n%s", log)
	}
	if strings.Contains(log, "new-session") {
		t.Errorf("no session may be built for a dead owner; tmux log:\n%s", log)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "wisp-deck", "stacks", "dev-gone-999")); err == nil {
		t.Errorf("nothing may be registered for a dead owner")
	}
}

// Consolidation (picking an already-open project) must only adopt sessions
// whose wrappers speak the stacking protocol. Sessions launched by
// pre-stacking wrappers lack WISP_DECK_OWNER_PID in session env, and their
// live wrappers kill their own session unconditionally on detach — adopting
// one turns "stack it" into "close it". They stay in their own tabs instead.
func TestStackAdoptableSessionsForProject_skips_sessions_without_owner_pid(t *testing.T) {
	dir := t.TempDir()
	envCase := `
      "dev-app-111") all='WISP_DECK=1\nWISP_DECK_PATH=/tmp/app\nWISP_DECK_PROJECT=app\nWISP_DECK_TOOL=claude\n' ;;
      "dev-app-333") all='WISP_DECK=1\nWISP_DECK_PATH=/tmp/app\nWISP_DECK_OWNER_PID=4242\nWISP_DECK_PROJECT=app\nWISP_DECK_TOOL=claude\n' ;;
`
	bin := mockTmux(t, dir, "100 dev-app-111\n300 dev-app-333\n", envCase)
	out, code := runBashFunc(t, "lib/session-stack.sh", "stack_adoptable_sessions_for_project",
		[]string{filepath.Join(bin, "tmux"), "/tmp/app"}, nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "dev-app-111")
	assertContains(t, out, "dev-app-333")
}
