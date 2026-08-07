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

// Closing ONE tab of the tab view must kill everything that was running in
// that tab's terminals.
//
// A tab is a tmux window, and tmux's own kill-window only SIGHUPs each pane's
// process group. That reaps a plain background job, but it does NOT reach the
// spare pane's terminals: the spare pane runs a *client* of a nested tmux
// server that is shared by every window of the session, so the tab's inner
// session — and every shell and long-running command inside its terminal tabs
// — reparents away from the pane and survives the close forever. Verified
// against a real tmux before these tests were written: the inner session was
// still listed, and a `sleep` started in it was still alive, after the outer
// window was killed.
//
// The whole-tab (Ghostty tab / session) close is already covered by
// cleanup_tmux_session + spare_tabs_cleanup; these guard the per-window close,
// which had no cleanup at all.

func closeTmux(t *testing.T) string {
	t.Helper()
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}
	return tmux
}

// closeSock returns a unique outer socket label, the outer session name, and
// the inner spare label that lib/spare-tabs.sh derives from that session name
// (spare_tabs_socket), and kills both servers on cleanup.
func closeSock(t *testing.T, name string) (string, string, string) {
	t.Helper()
	tmux := closeTmux(t)
	sock := fmt.Sprintf("wisp-close-%s-%d", name, os.Getpid())
	sess := strings.NewReplacer("-", "_").Replace(sock)
	inner := "gtspare_" + sess
	for _, s := range []string{sock, inner} {
		_ = exec.Command(tmux, "-L", s, "kill-server").Run()
	}
	t.Cleanup(func() {
		for _, s := range []string{sock, inner} {
			_ = exec.Command(tmux, "-L", s, "kill-server").Run()
		}
	})
	return sock, sess, inner
}

func closeShim(t *testing.T, tmux, sock string) string {
	t.Helper()
	shim := filepath.Join(t.TempDir(), "tmuxw")
	body := "#!/bin/bash\nexec " + tmux + " -L " + sock + " \"$@\"\n"
	if err := os.WriteFile(shim, []byte(body), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	return shim
}

func closeRun(t *testing.T, ctx context.Context, tmux, sock string, args ...string) string {
	t.Helper()
	out, err := exec.CommandContext(ctx, tmux, append([]string{"-L", sock}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("tmux -L %s %v: %v\n%s", sock, args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func closeQuery(ctx context.Context, tmux, sock string, args ...string) string {
	out, _ := exec.CommandContext(ctx, tmux, append([]string{"-L", sock}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out))
}

func pidAlive(pid string) bool {
	if pid == "" {
		return false
	}
	return exec.Command("ps", "-p", pid).Run() == nil
}

// spareWindow builds one tab of the tab view: a window whose pane runs the
// real spare-pane launch command (a client of the shared inner tmux server).
// The inner shell writes the pid of a long-running job it starts, standing in
// for whatever the user left running in that terminal.
func spareWindow(t *testing.T, ctx context.Context, tmux, sock, sess, inner, pidFile string, first bool) {
	t.Helper()
	// Via a script file: the command string is re-expanded by tmux's shell, so
	// an inline `$!` would be eaten before the inner shell ever sees it.
	script := pidFile + ".sh"
	body := "#!/bin/bash\nsleep 600 &\necho $! > " + pidFile + "\nexec bash\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write spare script: %v", err)
	}
	paneCmd := fmt.Sprintf(`env -u TMUX -u TMUX_PANE %s -L %s new-session -c /tmp %s || exec bash`,
		tmux, inner, script)
	if first {
		closeRun(t, ctx, tmux, sock, "new-session", "-d", "-s", sess, "-x", "200", "-y", "50", paneCmd)
	} else {
		closeRun(t, ctx, tmux, sock, "new-window", "-t", sess+":", paneCmd)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(pidFile); err == nil && strings.TrimSpace(string(data)) != "" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("inner spare shell never started for %s", pidFile)
}

func readPid(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.TrimSpace(string(data))
}

// Closing a tab kills the processes in that tab's spare terminals, and leaves
// every other tab's terminals running.
func TestTabViewCloseWindow_kills_the_processes_in_that_tabs_terminals(t *testing.T) {
	tmux := closeTmux(t)
	sock, sess, inner := closeSock(t, "spare")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dir := t.TempDir()
	keepPid := filepath.Join(dir, "keep.pid")
	closePid := filepath.Join(dir, "close.pid")
	spareWindow(t, ctx, tmux, sock, sess, inner, keepPid, true)   // window 0 — stays open
	spareWindow(t, ctx, tmux, sock, sess, inner, closePid, false) // window 1 — the closed tab

	keep, doomed := readPid(t, keepPid), readPid(t, closePid)
	if !pidAlive(keep) || !pidAlive(doomed) {
		t.Fatalf("setup: both spare jobs should be running (keep=%s doomed=%s)", keep, doomed)
	}

	root := projectRoot(t)
	out, err := exec.CommandContext(ctx, "bash", "-c", fmt.Sprintf(
		`source %q && tab_view_close_window %q %q %s %s:1`,
		filepath.Join(root, "lib", "tab-view.sh"), closeShim(t, tmux, sock), filepath.Join(root, "lib"), sess, sess,
	)).CombinedOutput()
	if err != nil {
		t.Fatalf("tab_view_close_window: %v\n%s", err, out)
	}

	if windows := closeQuery(ctx, tmux, sock, "list-windows", "-t", sess, "-F", "#{window_index}"); windows != "0" {
		t.Errorf("closed tab should be gone, windows = %q", windows)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && pidAlive(doomed) {
		time.Sleep(100 * time.Millisecond)
	}
	if pidAlive(doomed) {
		_ = exec.Command("kill", "-9", doomed).Run()
		t.Errorf("a process running in the closed tab's spare terminal survived the close (pid %s)", doomed)
	}
	if sessions := closeQuery(ctx, tmux, inner, "list-sessions", "-F", "#{session_name}"); len(strings.Fields(sessions)) != 1 {
		t.Errorf("closed tab's inner spare session should be gone, inner sessions = %q", sessions)
	}
	if !pidAlive(keep) {
		t.Errorf("closing one tab killed another tab's spare terminal (pid %s)", keep)
	}
}

// The safety net: a tab closed by any path that bypasses the close helper (a
// bare tmux kill-window, the last pane exiting) strands its inner spare
// session with no client and no way to ever get one. Reaping it must kill the
// processes inside it, and must not touch a session that still has its pane.
func TestSpareTabsReapOrphans_kills_the_terminals_of_a_tab_closed_behind_our_back(t *testing.T) {
	tmux := closeTmux(t)
	sock, sess, inner := closeSock(t, "reap")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dir := t.TempDir()
	keepPid := filepath.Join(dir, "keep.pid")
	orphanPid := filepath.Join(dir, "orphan.pid")
	spareWindow(t, ctx, tmux, sock, sess, inner, keepPid, true)
	spareWindow(t, ctx, tmux, sock, sess, inner, orphanPid, false)
	keep, orphan := readPid(t, keepPid), readPid(t, orphanPid)

	closeRun(t, ctx, tmux, sock, "kill-window", "-t", sess+":1")
	time.Sleep(500 * time.Millisecond)
	if !pidAlive(orphan) {
		t.Skip("tmux reaped the nested spare session itself; nothing left to reap")
	}

	root := projectRoot(t)
	out, err := exec.CommandContext(ctx, "bash", "-c", fmt.Sprintf(
		`source %q && spare_tabs_reap_orphans %q 0`,
		filepath.Join(root, "lib", "spare-tabs.sh"), inner,
	)).CombinedOutput()
	if err != nil {
		t.Fatalf("spare_tabs_reap_orphans: %v\n%s", err, out)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && pidAlive(orphan) {
		time.Sleep(100 * time.Millisecond)
	}
	if pidAlive(orphan) {
		_ = exec.Command("kill", "-9", orphan).Run()
		t.Errorf("orphaned spare terminal survived the reap (pid %s)", orphan)
	}
	if !pidAlive(keep) {
		t.Errorf("the reap killed a live tab's spare terminal (pid %s)", keep)
	}
}

// A young inner session is briefly unattached between new-session and its
// client attaching. The reaper must never kill one that is still starting up.
func TestSpareTabsReapOrphans_spares_a_session_younger_than_the_grace_window(t *testing.T) {
	tmux := closeTmux(t)
	_, _, inner := closeSock(t, "young")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Detached (never attached), just created — exactly the startup window.
	closeRun(t, ctx, tmux, inner, "new-session", "-d", "-s", "starting", "sleep 600")

	root := projectRoot(t)
	out, err := exec.CommandContext(ctx, "bash", "-c", fmt.Sprintf(
		`source %q && spare_tabs_reap_orphans %q 30`,
		filepath.Join(root, "lib", "spare-tabs.sh"), inner,
	)).CombinedOutput()
	if err != nil {
		t.Fatalf("spare_tabs_reap_orphans: %v\n%s", err, out)
	}
	if sessions := closeQuery(ctx, tmux, inner, "list-sessions", "-F", "#{session_name}"); sessions != "starting" {
		t.Errorf("a just-created spare session must survive the reap, got %q", sessions)
	}
}

// Fail-open, like everything else in tab-view.sh: closing a window that isn't
// there must not error.
func TestTabViewCloseWindow_is_fail_open(t *testing.T) {
	tmux := closeTmux(t)
	sock, _, _ := closeSock(t, "failopen")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	root := projectRoot(t)
	cmd := exec.CommandContext(ctx, "bash", "-c", fmt.Sprintf(
		`source %q && tab_view_close_window %q %q nosuch nosuch:9`,
		filepath.Join(root, "lib", "tab-view.sh"), closeShim(t, tmux, sock), filepath.Join(root, "lib"),
	))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("close of a missing window should exit 0: %v\n%s", err, out)
	}
}

// The helpers only matter if the launch actually wires them up: a close
// binding for the tab, and the window-unlinked safety net for every close
// path that bypasses it. Static, because the alternative is booting a real
// session; the behaviour itself is covered by the tests above.
func TestWrapperWiresTheTabCloseCleanup(t *testing.T) {
	root := projectRoot(t)
	src, err := os.ReadFile(filepath.Join(root, "wrapper.sh"))
	if err != nil {
		t.Fatalf("read wrapper.sh: %v", err)
	}
	wrapper := string(src)
	for _, want := range []string{
		"tab_view_close_window",   // the per-tab close, tree-kill included
		"window-unlinked",         // fires on kill-window AND on the last pane exiting
		"spare_tabs_reap_orphans", // the net for closes that skip the binding
	} {
		if !strings.Contains(wrapper, want) {
			t.Errorf("wrapper.sh never wires %q — a closed tab would leak its terminals", want)
		}
	}
}
