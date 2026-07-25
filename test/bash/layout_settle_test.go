package bash_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Regression guards for the crash-restore pane-shift bug.
//
// After a macOS crash, Ghostty respawns each restored tab's command BEFORE the
// tab's pty reaches its final size. The wrapper builds its 25/75 split at that
// small width, and when the real size lands, tmux distributes the width delta
// EQUALLY between the two columns (layout_resize_adjust is round-robin, not
// proportional) — corrupting the ratio (left pane drifted 25% → ~36%). The
// select-layout replay that should have fixed it was dead code: it was placed
// after the attached tmux launch command, which blocks until the session ENDS.
//
// Two guards:
//   - TestRestoreLayoutWatch_reapplies_after_late_resize drives the real tmux
//     binary through the exact race and requires restore_layout_watch to
//     converge on the captured geometry.
//   - TestWrapperRestore_replays_layout_while_session_alive runs wrapper.sh
//     against a tmux mock whose launch command BLOCKS (like the real final
//     attach) until select-layout is observed — so the replay can never again
//     be parked after the blocking call.

// TestRestoreLayoutWatch_reapplies_after_late_resize reproduces the
// crash-restore race against the real tmux binary: panes are built while the
// window is still small, the captured layout is handed to
// restore_layout_watch, and the window is resized to its final size only
// afterwards. The watcher must re-apply the layout after the resize so the
// final geometry matches the captured one exactly, and must terminate on its
// own once the size settles.
func TestRestoreLayoutWatch_reapplies_after_late_resize(t *testing.T) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sock := fmt.Sprintf("wisp-ls-%d", os.Getpid())
	_ = exec.Command(tmux, "-L", sock, "kill-server").Run()
	t.Cleanup(func() { _ = exec.Command(tmux, "-L", sock, "kill-server").Run() })

	// restore_layout_watch takes a single tmux command word; wrap the isolated
	// socket behind a shim so the watcher stays on this test's server.
	dir := t.TempDir()
	shim := filepath.Join(dir, "tmux-shim")
	if err := os.WriteFile(shim,
		[]byte("#!/bin/bash\nexec \""+tmux+"\" -L \""+sock+"\" \"$@\"\n"), 0755); err != nil {
		t.Fatalf("write shim: %v", err)
	}

	// Reference session at the FINAL window size: the default build there is
	// the geometry the user's layout was captured at (left column = 46 cols).
	buildPanesAt := func(sess string, x, y string) {
		rt(t, ctx, tmux, sock, "new-session", "-d", "-s", sess, "-x", x, "-y", y, "cat")
		rt(t, ctx, tmux, sock, "split-window", "-h", "-p", "75", "-t", sess+":0", "cat")
		rt(t, ctx, tmux, sock, "select-pane", "-L", "-t", sess+":0")
		rt(t, ctx, tmux, sock, "split-window", "-v", "-p", "45", "-t", sess+":0", "cat")
	}
	buildPanesAt("REF", "188", "51")
	layout := rt(t, ctx, tmux, sock, "display-message", "-p", "-t", "REF:0", "#{window_layout}")
	want := geom(t, ctx, tmux, sock, "REF")
	rt(t, ctx, tmux, sock, "kill-session", "-t", "REF")

	// The restored session: Ghostty spawned it while the pty was still small.
	buildPanesAt("B", "104", "24")

	root := projectRoot(t)
	watcher := exec.Command("bash", "-c",
		fmt.Sprintf("source %q && restore_layout_watch %q B %q 0.1 5 100",
			filepath.Join(root, "lib", "session-restore.sh"), shim, layout))
	if err := watcher.Start(); err != nil {
		t.Fatalf("start watcher: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- watcher.Wait() }()
	t.Cleanup(func() { _ = watcher.Process.Kill() })

	// Give the watcher a beat to make its first (pre-resize) application, then
	// deliver the late Ghostty resize that used to corrupt the ratio.
	time.Sleep(300 * time.Millisecond)
	rt(t, ctx, tmux, sock, "resize-window", "-x", "188", "-y", "51", "-t", "B:0")

	deadline := time.Now().Add(5 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		got = geom(t, ctx, tmux, sock, "B")
		if got == want {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got != want {
		t.Errorf("layout not re-applied after late resize\n want:\n%s\n got:\n%s", want, got)
	}

	// The watcher must terminate by itself once the window size settles — it
	// must never linger and fight the user's own pane resizes later on.
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Error("restore_layout_watch did not exit after the window size settled")
	}
}

// TestRestoreLayoutWatch_stops_on_user_pane_drag verifies the watcher's
// hands-off rule: the settle horizon has to be generous (a loaded post-crash
// storm can deliver the pty resize many seconds in), and a long-lived watcher
// is only safe if it stands down the moment the user drags a pane border. A
// drag changes #{window_layout} at a CONSTANT window size — the watcher must
// exit promptly on that signal and must never re-apply over the user's
// arrangement.
func TestRestoreLayoutWatch_stops_on_user_pane_drag(t *testing.T) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sock := fmt.Sprintf("wisp-ld-%d", os.Getpid())
	_ = exec.Command(tmux, "-L", sock, "kill-server").Run()
	t.Cleanup(func() { _ = exec.Command(tmux, "-L", sock, "kill-server").Run() })

	dir := t.TempDir()
	shim := filepath.Join(dir, "tmux-shim")
	if err := os.WriteFile(shim,
		[]byte("#!/bin/bash\nexec \""+tmux+"\" -L \""+sock+"\" \"$@\"\n"), 0755); err != nil {
		t.Fatalf("write shim: %v", err)
	}

	rt(t, ctx, tmux, sock, "new-session", "-d", "-s", "D", "-x", "188", "-y", "51", "cat")
	rt(t, ctx, tmux, sock, "split-window", "-h", "-p", "75", "-t", "D:0", "cat")
	rt(t, ctx, tmux, sock, "select-pane", "-L", "-t", "D:0")
	rt(t, ctx, tmux, sock, "split-window", "-v", "-p", "45", "-t", "D:0", "cat")
	layout := rt(t, ctx, tmux, sock, "display-message", "-p", "-t", "D:0", "#{window_layout}")

	// Long settle (10s) and horizon (30s): without drag detection the watcher
	// would linger for the full settle after the drag.
	root := projectRoot(t)
	watcher := exec.Command("bash", "-c",
		fmt.Sprintf("source %q && restore_layout_watch %q D %q 0.1 100 300",
			filepath.Join(root, "lib", "session-restore.sh"), shim, layout))
	if err := watcher.Start(); err != nil {
		t.Fatalf("start watcher: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- watcher.Wait() }()
	t.Cleanup(func() { _ = watcher.Process.Kill() })

	// Let the watcher make its initial application, then drag a border: the
	// right (AI) column from 141 to 100 columns, window size unchanged.
	time.Sleep(500 * time.Millisecond)
	rt(t, ctx, tmux, sock, "resize-pane", "-t", "D:0.2", "-x", "100")
	dragged := geom(t, ctx, tmux, sock, "D")

	// The watcher must exit well before its 10s settle — the drag is its cue.
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("watcher did not stand down after a user pane drag")
	}

	if got := geom(t, ctx, tmux, sock, "D"); got != dragged {
		t.Errorf("watcher reverted the user's drag\n want:\n%s\n got:\n%s", dragged, got)
	}
}

// TestWrapperRestore_replays_layout_while_session_alive locks in the fix for
// the dead-code replay: the wrapper's tmux command blocks at its final attach
// until the session ends, so select-layout must run while that command is
// alive. The mock waits in the launch command and records whether select-layout
// arrived during that window.
func TestWrapperRestore_replays_layout_while_session_alive(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}

	layoutRec := filepath.Join(home, "layout-rec")
	duringRec := filepath.Join(home, "during-rec")
	// The blocking client call is the batch that ends in attach-session (the
	// launch is two tmux client calls now; new-session returns immediately).
	// The replay watcher must have started before that attach, so the
	// select-layout record must appear while the attach is still blocked.
	tmuxMock := `#!/bin/bash
case " $* " in
  *" attach-session "*)
    for _ in $(seq 1 100); do
      if [ -f "$GT_LAYOUT_REC" ]; then echo yes > "$GT_DURING_REC"; break; fi
      sleep 0.1
    done
    exit 0
    ;;
esac
case "$1" in
  new-session)
    exit 0
    ;;
  select-layout)
    printf '%s\n' "$*" >> "$GT_LAYOUT_REC"
    ;;
  display-message)
    echo "188x51"
    ;;
esac
exit 0
`
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

	projDir := filepath.Join(home, "proj")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	confDir := filepath.Join(home, ".config", "wisp-deck")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatalf("mkdir conf: %v", err)
	}
	layout := "da25,188x51,0,0{46x51,0,0[46x27,0,0,0,46x23,0,28,2],141x51,47,0,1}"
	if err := os.WriteFile(filepath.Join(confDir, "restore-queue"),
		[]byte("12345|"+projDir+"|claude|sid-42|"+layout+"\n"), 0644); err != nil {
		t.Fatalf("write queue: %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "last-restore-boot"),
		[]byte("12345\n"), 0644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	seedChainTicket(t, confDir)

	env := buildEnv(t, nil, "HOME="+home,
		"GT_LAYOUT_REC="+layoutRec, "GT_DURING_REC="+duringRec)
	_, code := runBashScript(t, "wrapper.sh", nil, env)
	assertExitCode(t, code, 0)

	if _, err := os.Stat(duringRec); err != nil {
		t.Fatal("select-layout never ran while the tmux launch was attached — the replay is dead code again")
	}
	data, err := os.ReadFile(layoutRec)
	if err != nil {
		t.Fatalf("select-layout was never invoked: %v", err)
	}
	if !strings.Contains(string(data), layout) {
		t.Errorf("select-layout did not receive the captured layout; got:\n%s", string(data))
	}
}
