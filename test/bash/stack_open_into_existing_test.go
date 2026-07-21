package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Opening an already-open project must ADD a session to the existing tab's
// stack, never close that tab. The old consolidation flipped roles (the new
// tab adopted the old tabs' sessions and the old tabs closed); the new flow
// keeps every existing tab where it is: the picker tab builds the fresh
// session in-place into the existing owner's stack (exactly like prefix+S),
// switches the owner tab's client to it, and closes ITSELF.

// liveOwnerTmuxMock answers the queries stack_live_owner_for_project makes:
// the project's sessions, their env stamps, and the owner tab's client.
func liveOwnerTmuxMock(t *testing.T, dir, projDir string, ownerPid int) string {
	t.Helper()
	body := fmt.Sprintf(`
case "$1" in
  list-sessions) printf '100 dev-app-111\n' ;;
  show-environment)
    case "${4:-}" in
      WISP_DECK_PATH) echo "WISP_DECK_PATH=%s" ;;
      WISP_DECK_OWNER_PID) echo "WISP_DECK_OWNER_PID=%d" ;;
      *) exit 1 ;;
    esac ;;
  list-clients) echo "client7" ;;
esac
exit 0
`, projDir, ownerPid)
	return mockCommand(t, dir, "tmux", body)
}

func TestStackLiveOwnerForProject_prints_owner_and_pid(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir()
	writeTempFile(t, cfg, "stacks/dev-app-111", "dev-app-111\n")
	bin := liveOwnerTmuxMock(t, dir, "/tmp/app", os.Getpid())

	out, code := runBashFunc(t, "lib/session-stack.sh", "stack_live_owner_for_project",
		[]string{filepath.Join(bin, "tmux"), cfg, "/tmp/app"}, nil)
	assertExitCode(t, code, 0)
	want := fmt.Sprintf("dev-app-111\t%d", os.Getpid())
	if got := strings.TrimSpace(out); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A dead owner pid means the tab is gone (its sessions are reaper fodder);
// nothing must be built into its stack.
func TestStackLiveOwnerForProject_dead_owner_prints_nothing(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir()
	writeTempFile(t, cfg, "stacks/dev-app-111", "dev-app-111\n")
	bin := liveOwnerTmuxMock(t, dir, "/tmp/app", 999999)

	out, code := runBashFunc(t, "lib/session-stack.sh", "stack_live_owner_for_project",
		[]string{filepath.Join(bin, "tmux"), cfg, "/tmp/app"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected nothing for a dead owner pid, got %q", out)
	}
}

// A session no registry file lists has no owner tab to build into (crash
// window between new-session and registration) — fall back to a fresh tab.
func TestStackLiveOwnerForProject_unregistered_session_prints_nothing(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir() // no stacks/ files at all
	bin := liveOwnerTmuxMock(t, dir, "/tmp/app", os.Getpid())

	out, code := runBashFunc(t, "lib/session-stack.sh", "stack_live_owner_for_project",
		[]string{filepath.Join(bin, "tmux"), cfg, "/tmp/app"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected nothing for an unregistered session, got %q", out)
	}
}

// Pre-stacking sessions (no WISP_DECK_OWNER_PID stamp) must not be treated as
// a live stack owner: their tab cannot host or clean up a stacked session.
func TestStackLiveOwnerForProject_skips_unstamped_sessions(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir()
	writeTempFile(t, cfg, "stacks/dev-app-111", "dev-app-111\n")
	body := `
case "$1" in
  list-sessions) printf '100 dev-app-111\n' ;;
  show-environment)
    case "${4:-}" in
      WISP_DECK_PATH) echo "WISP_DECK_PATH=/tmp/app" ;;
      *) exit 1 ;;
    esac ;;
esac
exit 0
`
	bin := mockCommand(t, dir, "tmux", body)
	out, code := runBashFunc(t, "lib/session-stack.sh", "stack_live_owner_for_project",
		[]string{filepath.Join(bin, "tmux"), cfg, "/tmp/app"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected nothing for an unstamped session, got %q", out)
	}
}

// End-to-end: an interactive pick of a project that is already open in a live
// tab builds the fresh session INTO that tab's stack — registered in the
// existing owner's registry file, owner-pid restamped to the existing owner's
// wrapper — and exits 0 without attaching. The existing tab is left exactly
// as the user sees it: its client is NOT switched (the new session appears as
// a background chip on the session bar; yanking the view to the fresh
// conversation read as "my session was closed and replaced"), no
// detach-client, no kill-session.
func TestWrapperInteractivePick_builds_into_existing_tab_stack(t *testing.T) {
	projParent := t.TempDir()
	projDir := filepath.Join(projParent, "app")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}

	home := t.TempDir()
	cfg := filepath.Join(home, ".config", "wisp-deck")
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	writeTempFile(t, cfg, "ai-tool", "claude\n")
	writeTempFile(t, cfg, "projects", "app:"+projDir+"\n")
	// The existing tab: session dev-app-111 for this project, owned by a LIVE
	// pid (this test process), registered in its own stack file.
	writeTempFile(t, cfg, "stacks/dev-app-111", "dev-app-111\n")

	ownerPid := os.Getpid()
	tmuxMock := fmt.Sprintf(`#!/bin/bash
log="$HOME/tmux.log"
printf '%%s\n' "$*" >> "$log"
case "$1" in
  new-session) printf '%%s\n' '%%0' '%%1' ;;
  list-sessions) printf '100 dev-app-111\n' ;;
  show-environment)
    case "${4:-}" in
      WISP_DECK_PATH) echo "WISP_DECK_PATH=%s" ;;
      WISP_DECK_OWNER_PID) echo "WISP_DECK_OWNER_PID=%d" ;;
      *) exit 1 ;;
    esac ;;
  list-clients)
    case "$*" in
      *client_width*) echo "173 47" ;;
      *) echo "client7" ;;
    esac ;;
  capture-pane) echo ">" ;;
esac
exit 0
`, projDir, ownerPid)
	tuiMock := fmt.Sprintf(`#!/bin/bash
if [ "$1" = "main-menu" ]; then
  printf '{"action":"select-project","name":"app","path":"%s"}\n'
fi
exit 0
`, projDir)
	mocks := map[string]string{
		"tmux":          tmuxMock,
		"wisp-deck-tui": tuiMock,
		"claude":        "#!/bin/bash\nexit 0\n",
		"sysctl":        "#!/bin/bash\necho \"{ sec = 12345, usec = 1 } Thu Jul  2 01:01:01 2026\"\n",
	}
	for name, body := range mocks {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0755); err != nil {
			t.Fatalf("write mock %s: %v", name, err)
		}
	}

	env := buildEnv(t, nil, "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"))
	out, code := runBashScript(t, "wrapper.sh", nil, env)
	log, _ := os.ReadFile(filepath.Join(home, "tmux.log"))

	if code != 0 {
		t.Fatalf("wrapper exited %d\noutput:\n%s\ntmux log:\n%s", code, out, log)
	}
	logs := string(log)
	if !strings.Contains(logs, "new-session -d") {
		t.Fatalf("no session was built; tmux log:\n%s", logs)
	}
	if !strings.Contains(logs, fmt.Sprintf("WISP_DECK_OWNER_PID %d", ownerPid)) {
		t.Errorf("ownership must be restamped to the EXISTING tab's wrapper pid; tmux log:\n%s", logs)
	}
	if strings.Contains(logs, "switch-client") {
		t.Errorf("the existing tab's client must NOT be switched (the current session stays on screen; the new one is a background chip); tmux log:\n%s", logs)
	}
	if strings.Contains(logs, "attach-session") {
		t.Errorf("the picker tab must close itself, never attach; tmux log:\n%s", logs)
	}
	if strings.Contains(logs, "detach-client") {
		t.Errorf("no existing tab's client may be detached (that closed tabs); tmux log:\n%s", logs)
	}
	if strings.Contains(logs, "kill-session") {
		t.Errorf("no existing session may be killed; tmux log:\n%s", logs)
	}

	data, err := os.ReadFile(filepath.Join(cfg, "stacks", "dev-app-111"))
	if err != nil {
		t.Fatalf("read owner stack file: %v", err)
	}
	lines := strings.Fields(string(data))
	if len(lines) != 2 || lines[0] != "dev-app-111" || !strings.HasPrefix(lines[1], "dev-app-") {
		t.Errorf("the new session must be registered in the EXISTING owner's stack file; got %q", string(data))
	}
}

// A pick of a project with NO live stack owner must launch normally: build,
// register its own stack file, and attach in this tab.
func TestWrapperInteractivePick_no_existing_tab_attaches_normally(t *testing.T) {
	projParent := t.TempDir()
	projDir := filepath.Join(projParent, "app")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}

	home := t.TempDir()
	cfg := filepath.Join(home, ".config", "wisp-deck")
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	writeTempFile(t, cfg, "ai-tool", "claude\n")
	writeTempFile(t, cfg, "projects", "app:"+projDir+"\n")

	tmuxMock := `#!/bin/bash
log="$HOME/tmux.log"
printf '%s\n' "$*" >> "$log"
case "$1" in
  new-session) printf '%s\n' '%0' '%1' ;;
  list-sessions) : ;;
  show-environment) exit 1 ;;
  attach-session) exit 0 ;;
  capture-pane) echo ">" ;;
esac
exit 0
`
	tuiMock := fmt.Sprintf(`#!/bin/bash
if [ "$1" = "main-menu" ]; then
  printf '{"action":"select-project","name":"app","path":"%s"}\n'
fi
exit 0
`, projDir)
	mocks := map[string]string{
		"tmux":          tmuxMock,
		"wisp-deck-tui": tuiMock,
		"claude":        "#!/bin/bash\nexit 0\n",
		"sysctl":        "#!/bin/bash\necho \"{ sec = 12345, usec = 1 } Thu Jul  2 01:01:01 2026\"\n",
	}
	for name, body := range mocks {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0755); err != nil {
			t.Fatalf("write mock %s: %v", name, err)
		}
	}

	env := buildEnv(t, nil, "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"))
	_, _ = runBashScript(t, "wrapper.sh", nil, env)
	log, _ := os.ReadFile(filepath.Join(home, "tmux.log"))
	logs := string(log)

	if !strings.Contains(logs, "new-session -d") {
		t.Fatalf("no session was built; tmux log:\n%s", logs)
	}
	if !strings.Contains(logs, "attach-session") {
		t.Errorf("a fresh project pick must attach in this tab; tmux log:\n%s", logs)
	}
	// (The own-stack-file registration is not asserted post-exit: the mock
	// attach returns immediately, so the wrapper's cleanup has already torn the
	// session — and its registry file — down by the time we could look.)
}
