package bash_test

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// The end-to-end guard for the whole class, on a REAL terminal.
//
// The unit tests each pin one piece — stderr muted before the jobs start, no
// backgrounded job keeping stdout, the tab title routed to /dev/tty. This one
// composes them exactly as wrapper.sh does and then reproduces the original bug
// on a pty: the keep-awake reaper globbing a holders directory that another
// session is unlinking from underneath it, twice a second, forever.
//
// That race is what put
//
//	keep-awake.sh: line 79: .../keep-awake.d/<session>: No such file or directory
//
// inside Claude's input box. Nothing may reach this terminal now — not from the
// reaper, not from any lib the watcher loop calls, not from the shell itself —
// with two exceptions, both asserted below because both are the point: the tab
// title still gets through (via /dev/tty), and a genuine tmux launch failure
// still gets through (via the saved fd 3), because after that there is no UI
// left to protect.
func TestNothingReachesTheSessionTerminalOnceTheAIToolOwnsIt(t *testing.T) {
	root := projectRoot(t)
	dir := t.TempDir()
	holders := filepath.Join(dir, "cfg", "keep-awake.d")
	if err := os.MkdirAll(holders, 0o755); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(dir, "cfg", "logs", "sess.log")

	binDir := mockCommand(t, dir, "tmux", `
case "$1" in
  list-panes)   echo "0 0" ;;
  capture-pane) echo "> " ;;
  new-session)  echo "tmux: a REAL launch failure" >&2 ;;
esac
exit 0
`)

	// Everything below the mute mirrors wrapper.sh's plumbing verbatim.
	//
	// The leak is injected into play_notification_sound, a real lib on the
	// watcher's tick path, and it misbehaves in all three ways the wrapper has
	// actually been bitten by: a plain echo, a write to stderr, and the shell's
	// own open failure — the last being the literal keep-awake bug. It is
	// redefined before the watcher starts, so the loop inherits it, exactly as a
	// genuinely buggy lib would be inherited.
	//
	// Note what is NOT asserted: the foreground shell's own stdout. In the real
	// wrapper the foreground blocks inside `tmux new-session` for the entire
	// session, so it cannot paint over the AI tool's UI; only the jobs that
	// outlive the handoff can, and those are what this pins.
	script := fmt.Sprintf(`
cd %q
source lib/tui.sh; source lib/keep-awake.sh; source lib/notification-setup.sh
source lib/settings-json.sh; source lib/tab-title-watcher.sh

play_notification_sound() {
  echo "STRAY STDOUT"
  echo "STRAY STDERR" >&2
  cat /definitely/not/a/file
}

# The Stop-hook marker: makes the watcher see the agent as idle, which is the
# tick that fires the tab title AND the notification sound (our leaky lib).
touch %q

exec 3>&2
gt_mute_terminal_stderr %q
export WISP_DECK_ERROR_LOG=%q

start_tab_title_watcher "sess" "claude" "myproj" "full" "tmux" %q %q

# Meanwhile, the original race: other sessions unlinking holders out from under
# this one's reaper, all day long.
for i in 1 2 3 4 5 6 7 8; do
  echo 999999 > %q/ghost-$i
  rm -f %q/ghost-$i
done

sleep 3
tmux new-session 2>&3
stop_tab_title_watcher
`, root, filepath.Join(dir, "marker"), logFile, logFile,
		filepath.Join(dir, "marker"), filepath.Join(dir, "cfg"),
		holders, holders)

	cmd := exec.Command("bash", "-c", script)
	cmd.Env = buildEnv(t, []string{binDir})
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("failed to open pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()

	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(ptmx)
		done <- string(data)
	}()

	var screen string
	select {
	case screen = <-done:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("timed out")
	}
	_ = cmd.Wait()

	// Nothing that is not addressed to the user may appear on their terminal.
	for _, forbidden := range []string{
		"No such file or directory", // the shell's own redirect/open failures
		"keep-awake.sh",             // any line naming the lib that started this
		"STRAY STDOUT",
		"STRAY STDERR",
	} {
		if strings.Contains(screen, forbidden) {
			t.Errorf("%q was painted onto the session terminal, where the AI tool is "+
				"drawing its UI.\nfull terminal contents:\n%q", forbidden, screen)
		}
	}

	// The two things that MUST still get through.
	if !strings.Contains(screen, "\033]0;myproj · claude\007") {
		t.Errorf("the tab title never reached the terminal; routing it to /dev/tty is "+
			"what lets the watcher drop stdout.\nfull terminal contents:\n%q", screen)
	}
	if !strings.Contains(screen, "tmux: a REAL launch failure") {
		t.Errorf("a tmux launch failure must still reach the user on fd 3 — after it "+
			"there is no UI left to protect.\nfull terminal contents:\n%q", screen)
	}

	// Silenced, but not lost.
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("session error log missing: %v", err)
	}
	if !strings.Contains(string(data), "No such file or directory") ||
		!strings.Contains(string(data), "STRAY STDERR") {
		t.Errorf("errors were kept off the terminal but not written to the session log; "+
			"silencing must not mean losing.\nlog:\n%s", data)
	}
}
