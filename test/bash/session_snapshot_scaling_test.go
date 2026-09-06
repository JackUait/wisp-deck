package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scalingSnapshotTmuxMock answers list-sessions with `sessions` wisp sessions and
// show-environment with a full, realistic env block for each.
func scalingSnapshotTmuxMock(t *testing.T, dir string, sessions int) string {
	t.Helper()
	var created strings.Builder
	for i := 0; i < sessions; i++ {
		fmt.Fprintf(&created, "%d dev-proj-%d\\n", 1700000000+i, i)
	}
	var windows strings.Builder
	for i := 0; i < sessions; i++ {
		fmt.Fprintf(&windows, "0 layout-abc dev-proj-%d\\n", i)
	}
	body := fmt.Sprintf(`
echo "$1" >> "${SNAP_TMUX_LOG:-/dev/null}"
case "$1" in
  list-sessions) printf '%s' ;;
  list-windows) printf '%s' ;;
  show-environment)
    printf 'WISP_DECK=1\nWISP_DECK_BOOT=BOOT-1\nWISP_DECK_PROJECT=proj\nWISP_DECK_PATH=/tmp/proj\nWISP_DECK_TOOL=claude\nWISP_DECK_TERMINAL=ghostty\nWISP_DECK_CLAUDE_SESSION=sid-1\nWISP_DECK_CODEX_SESSION=\nWISP_DECK_CODEX_SESSION_FILE=\nWISP_DECK_CLAUDE_ACCOUNT=personal\nWISP_DECK_SEQ=100\nPATH=/usr/bin\n' ;;
  display-message) printf 'layout-abc\n' ;;
  *) : ;;
esac
exit 0
`, created.String(), windows.String())
	return mockCommand(t, dir, "tmux", body)
}

// Every wisp session runs its own snapshot heartbeat, and each tick used to
// enumerate EVERY session on the machine and parse each one's environment with
// a separate `sed` per field. That makes the machine-wide cost quadratic in
// sessions: N sessions x O(N) parsing, every 10 seconds, all producing the
// identical file. The per-tick text-tool spawn count must not grow with the
// size of the deck.
func TestWriteSessionSnapshot_parsing_does_not_spawn_per_session(t *testing.T) {
	countFor := func(t *testing.T, sessions int) int {
		t.Helper()
		dir := t.TempDir()
		sedLog := filepath.Join(dir, "sed.log")
		binDir := scalingSnapshotTmuxMock(t, dir, sessions)
		mockCommand(t, dir, "sed", `echo "sed" >> "$SNAP_SED_LOG"; exec /usr/bin/sed "$@"`)
		mockCommand(t, dir, "grep", `echo "grep" >> "$SNAP_SED_LOG"; exec /usr/bin/grep "$@"`)
		env := buildEnv(t, []string{binDir}, "SNAP_SED_LOG="+sedLog)

		snapshot := filepath.Join(dir, "last-session")
		_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
			[]string{"tmux", snapshot}, env)
		assertExitCode(t, code, 0)

		raw, err := os.ReadFile(sedLog)
		if err != nil {
			if os.IsNotExist(err) {
				return 0
			}
			t.Fatalf("read spawn log: %v", err)
		}
		return len(strings.Split(strings.TrimSpace(string(raw)), "\n"))
	}

	one := countFor(t, 1)
	ten := countFor(t, 10)
	if ten > one {
		t.Errorf("snapshot spawned %d text-tool processes for 10 sessions but %d for 1; the per-tick cost must not grow with the deck", ten, one)
	}
}

// The snapshot's content must be unchanged by however it is parsed.
func TestWriteSessionSnapshot_still_records_every_session(t *testing.T) {
	dir := t.TempDir()
	binDir := scalingSnapshotTmuxMock(t, dir, 3)
	env := buildEnv(t, []string{binDir})
	snapshot := filepath.Join(dir, "last-session")

	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snapshot}, env)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("snapshot has %d lines, want 3:\n%s", len(lines), data)
	}
	want := "BOOT-1|proj|/tmp/proj|claude|ghostty|sid-1|layout-abc|personal|"
	for _, line := range lines {
		if line != want {
			t.Errorf("snapshot line = %q, want %q", line, want)
		}
	}
}

// Every wisp session runs its own snapshot heartbeat, and each tick asked tmux
// for the pane geometry of EVERY session on the machine, one client per
// session. That is O(sessions) tmux clients per tick, in a loop every session
// runs, so the deck pays it squared -- and all of those ticks write the same
// file from the same input. tmux can report every window's layout in one query,
// measured at 7.4ms against 114.6ms for sixteen separate ones.
func TestWriteSessionSnapshot_asks_for_every_layout_at_once(t *testing.T) {
	layoutQueriesFor := func(t *testing.T, sessions int) int {
		t.Helper()
		dir := t.TempDir()
		tmuxLog := filepath.Join(dir, "tmux.log")
		binDir := scalingSnapshotTmuxMock(t, dir, sessions)
		env := buildEnv(t, []string{binDir}, "SNAP_TMUX_LOG="+tmuxLog)

		snapshot := filepath.Join(dir, "last-session")
		_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
			[]string{"tmux", snapshot}, env)
		assertExitCode(t, code, 0)

		raw, err := os.ReadFile(tmuxLog)
		if err != nil {
			t.Fatalf("read tmux log: %v", err)
		}
		queries := 0
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			switch strings.TrimSpace(line) {
			case "display-message", "list-windows":
				queries++
			}
		}
		return queries
	}

	one := layoutQueriesFor(t, 1)
	twenty := layoutQueriesFor(t, 20)

	if twenty > one {
		t.Errorf(
			"snapshot made %d layout queries for 20 sessions against %d for 1;\n"+
				"one query reports every window's layout, and this tick runs in every session",
			twenty, one,
		)
	}
}
