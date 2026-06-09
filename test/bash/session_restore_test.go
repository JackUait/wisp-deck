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

// Helper: run maybe_restore_session with a stub launch_restore_window that
// records every call (one line per spawn) to recFile.
func runMaybeRestore(t *testing.T, configDir, curBoot, recFile string) (string, int) {
	t.Helper()
	root := projectRoot(t)
	mod := filepath.Join(root, "lib", "session-restore.sh")
	script := `
source ` + quote(mod) + `
launch_restore_window() { echo "$1|$2|$3|$4" >> ` + quote(recFile) + `; }
maybe_restore_session ` + quote(configDir) + ` ` + quote(curBoot) + ` "/w/wrapper.sh"
`
	return runBashSnippet(t, script, nil)
}

func TestMaybeRestore_spawns_prior_boot_lines_and_writes_marker(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty\n111|web|/p/web|codex|ghostty\n")
	rec := filepath.Join(dir, "rec")
	_, code := runMaybeRestore(t, dir, "222", rec)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("no spawns recorded: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "ghostty|/w/wrapper.sh|/p/app|claude\nghostty|/w/wrapper.sh|/p/web|codex"
	if got != want {
		t.Errorf("spawns:\n got %q\nwant %q", got, want)
	}
	marker, _ := os.ReadFile(filepath.Join(dir, "last-restore-boot"))
	if strings.TrimSpace(string(marker)) != "222" {
		t.Errorf("marker = %q, want 222", string(marker))
	}
}

func TestMaybeRestore_noop_when_already_restored_this_boot(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "last-session", "111|app|/p/app|claude|ghostty\n")
	writeTempFile(t, dir, "last-restore-boot", "222\n")
	rec := filepath.Join(dir, "rec")
	_, code := runMaybeRestore(t, dir, "222", rec)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(rec); err == nil {
		t.Error("expected no spawns when boot already restored")
	}
}

func TestMaybeRestore_noop_when_no_snapshot(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	_, code := runMaybeRestore(t, dir, "222", rec)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(rec); err == nil {
		t.Error("expected no spawns when snapshot missing")
	}
}

func TestMaybeRestore_skips_current_boot_lines(t *testing.T) {
	dir := t.TempDir()
	// All lines are from the current boot -> nothing to restore.
	writeTempFile(t, dir, "last-session", "222|app|/p/app|claude|ghostty\n")
	rec := filepath.Join(dir, "rec")
	_, code := runMaybeRestore(t, dir, "222", rec)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(rec); err == nil {
		t.Error("expected no spawns when all lines are current-boot")
	}
	// marker must NOT be written when nothing was restored
	if _, err := os.Stat(filepath.Join(dir, "last-restore-boot")); err == nil {
		t.Error("marker should not be written when nothing restored")
	}
}

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

func TestParseRestoreFlag_emits_path_and_tool(t *testing.T) {
	out, code := runBashFunc(t, "lib/session-restore.sh", "parse_restore_flag",
		[]string{"--restore", "/p/app", "claude"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "/p/app|claude" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "/p/app|claude")
	}
}

func TestParseRestoreFlag_empty_when_not_restore(t *testing.T) {
	out, code := runBashFunc(t, "lib/session-restore.sh", "parse_restore_flag",
		[]string{"/some/dir"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty, got %q", strings.TrimSpace(out))
	}
}

func TestLaunchRestoreWindow_loads_adapter_and_calls_hook(t *testing.T) {
	// Stub load_terminal_adapter + terminal_launch_restore so we can record args.
	root := projectRoot(t)
	mod := filepath.Join(root, "lib", "session-restore.sh")
	rec := filepath.Join(t.TempDir(), "rec")
	script := `
source ` + quote(mod) + `
load_terminal_adapter() { :; }                 # stub: pretend adapter loaded
terminal_launch_restore() { echo "$1|$2|$3" > ` + quote(rec) + `; }
launch_restore_window "ghostty" "/w/wrapper.sh" "/p/app" "claude"
`
	_, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("hook not called: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "/w/wrapper.sh|/p/app|claude"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteSessionSnapshot_handles_session_name_with_spaces(t *testing.T) {
	dir := t.TempDir()
	// Session name contains a space: "dev-My Project-1".
	// The word-splitting bug in the old for loop would split this into two tokens.
	tmuxBody := `
case "$1" in
  list-sessions) echo "dev-My Project-1" ;;
  show-environment)
    if [ "$3" = "dev-My Project-1" ]; then
      printf 'GHOST_TAB=1\nGHOST_TAB_BOOT=111\nGHOST_TAB_PROJECT=My Project\nGHOST_TAB_PATH=/p/app\nGHOST_TAB_TOOL=claude\nGHOST_TAB_TERMINAL=ghostty\n'
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
	want := "111|My Project|/p/app|claude|ghostty"
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
