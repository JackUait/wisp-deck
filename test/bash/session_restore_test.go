package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func quote(s string) string { return "\"" + s + "\"" }

func TestCurrentBootId_parses_sec_value(t *testing.T) {
	dir := t.TempDir()
	// macOS sysctl prints: { sec = 1700000000, usec = 123456 } Thu ...
	binDir := mockCommand(t, dir, "sysctl", `echo "{ sec = 1700000000, usec = 123456 } Thu Jan  1 00:00:00 2024"`)
	env := buildEnv(t, []string{binDir})
	out, code := runBashFunc(t, "lib/session-restore.sh", "current_boot_id", nil, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "1700000000" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "1700000000")
	}
}

func TestCurrentBootId_empty_when_sysctl_fails(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "sysctl", `exit 1`)
	env := buildEnv(t, []string{binDir})
	out, _ := runBashFunc(t, "lib/session-restore.sh", "current_boot_id", nil, env)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty, got %q", strings.TrimSpace(out))
	}
}

// referenced by later tasks; keep import of filepath/os used
var _ = filepath.Join
var _ = os.Environ

func TestWriteSessionSnapshot_writes_ghost_sessions_only(t *testing.T) {
	dir := t.TempDir()
	// Mock tmux: list two sessions; only dev-app-1 carries GHOST_TAB=1.
	tmuxBody := `
case "$1" in
  list-sessions) echo "dev-app-1"; echo "other-sess" ;;
  show-environment)
    if [ "$3" = "dev-app-1" ]; then
      printf 'GHOST_TAB=1\nGHOST_TAB_BOOT=111\nGHOST_TAB_PROJECT=app\nGHOST_TAB_PATH=/p/app\nGHOST_TAB_TOOL=claude\nGHOST_TAB_TERMINAL=ghostty\n'
    else
      printf 'SOMEVAR=1\n'
    fi ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	env := buildEnv(t, []string{binDir})
	snap := filepath.Join(dir, "last-session")
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "111|app|/p/app|claude|ghostty"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteSessionSnapshot_empty_when_no_ghost_sessions(t *testing.T) {
	dir := t.TempDir()
	tmuxBody := `
case "$1" in
  list-sessions) echo "other-sess" ;;
  show-environment) printf 'SOMEVAR=1\n' ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	env := buildEnv(t, []string{binDir})
	snap := filepath.Join(dir, "last-session")
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	data, _ := os.ReadFile(snap)
	if strings.TrimSpace(string(data)) != "" {
		t.Errorf("expected empty snapshot, got %q", string(data))
	}
}
