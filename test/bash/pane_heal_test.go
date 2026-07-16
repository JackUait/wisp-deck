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

// The stuck-launch incident: when any split-window in the launch chain fails
// (the observed cause was a pre-resize tiny pty; a future cause could be a
// tmux error or a quoting bug), the tab sat attached to a lone full-width
// ledger pane forever — tmux runs the rest of the chain regardless and
// nothing verified the layout afterwards. gt_ensure_panes_watch is the
// class-level guard: a background watcher that waits for a sanely-sized
// window and rebuilds whatever panes are missing, so NO future split failure
// can strand a one-pane tab.

func healTmux(t *testing.T) string {
	t.Helper()
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}
	return tmux
}

func healSock(t *testing.T, name string) string {
	t.Helper()
	sock := fmt.Sprintf("wisp-heal-%s-%d", name, os.Getpid())
	tmux := healTmux(t)
	_ = exec.Command(tmux, "-L", sock, "kill-server").Run()
	t.Cleanup(func() { _ = exec.Command(tmux, "-L", sock, "kill-server").Run() })
	return sock
}

// healShim writes an executable that forwards to `tmux -L <sock>` so the
// watcher can be handed one command word, matching the wrapper's "$TMUX_CMD"
// convention.
func healShim(t *testing.T, tmux, sock string) string {
	t.Helper()
	shim := filepath.Join(t.TempDir(), "tmuxw")
	body := "#!/bin/bash\nexec " + tmux + " -L " + sock + " \"$@\"\n"
	if err := os.WriteFile(shim, []byte(body), 0755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	return shim
}

func healRun(t *testing.T, ctx context.Context, tmux, sock string, args ...string) string {
	t.Helper()
	out, err := exec.CommandContext(ctx, tmux, append([]string{"-L", sock}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func Test_gt_ensure_panes_watch_heals_single_pane_window(t *testing.T) {
	tmux := healTmux(t)
	sock := healSock(t, "heal")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// The stuck state: a session created at a size too small to split, left
	// with only the ledger pane.
	healRun(t, ctx, tmux, sock, "new-session", "-d", "-x", "2", "-y", "2",
		"-s", "stuck", "sleep 300")

	// Start the watcher the way the wrapper would (backgrounded, before the
	// window has its real size).
	root := projectRoot(t)
	watcher := exec.CommandContext(ctx, "bash", "-c",
		fmt.Sprintf(`source %q && gt_ensure_panes_watch %q stuck %q "sleep 300" "sleep 300" 0.1 100`,
			filepath.Join(root, "lib", "tmux-session.sh"), healShim(t, tmux, sock), t.TempDir()))
	watcher.Stdout, watcher.Stderr = os.Stderr, os.Stderr
	if err := watcher.Start(); err != nil {
		t.Fatalf("start watcher: %v", err)
	}
	defer func() { _ = watcher.Process.Kill() }()

	// The pty resize lands late, as in the incident.
	time.Sleep(300 * time.Millisecond)
	healRun(t, ctx, tmux, sock, "resize-window", "-t", "stuck", "-x", "100", "-y", "40")

	deadline := time.Now().Add(15 * time.Second)
	for {
		panes := healRun(t, ctx, tmux, sock, "display-message", "-p", "-t", "stuck:0", "#{window_panes}")
		if panes == "3" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("watcher never healed the one-pane window (panes=%s)", panes)
		}
		time.Sleep(200 * time.Millisecond)
	}
	// The healed AI pane must carry the @gt_ai marker every downstream
	// consumer (focus watcher, account switch) resolves panes by.
	marks := healRun(t, ctx, tmux, sock, "list-panes", "-t", "stuck:0", "-F", "#{@gt_ai}")
	if strings.Count(marks, "1") != 1 {
		t.Errorf("exactly one pane must be marked @gt_ai after healing, got %q", marks)
	}
}

func Test_gt_ensure_panes_watch_noops_on_healthy_window(t *testing.T) {
	tmux := healTmux(t)
	sock := healSock(t, "noop")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	healRun(t, ctx, tmux, sock, "new-session", "-d", "-x", "100", "-y", "40",
		"-s", "ok", "sleep 300")
	healRun(t, ctx, tmux, sock, "split-window", "-h", "-p", "75", "-t", "ok:0", "sleep 300")
	healRun(t, ctx, tmux, sock, "set-option", "-p", "-t", "ok:0", "@gt_ai", "1")
	healRun(t, ctx, tmux, sock, "select-pane", "-L", "-t", "ok:0")
	healRun(t, ctx, tmux, sock, "split-window", "-v", "-p", "45", "-t", "ok:0", "sleep 300")

	root := projectRoot(t)
	out, code := runBashSnippet(t, fmt.Sprintf(
		`source %q && gt_ensure_panes_watch %q ok %q "sleep 300" "sleep 300" 0.05 10`,
		filepath.Join(root, "lib", "tmux-session.sh"), healShim(t, tmux, sock), t.TempDir()), nil)
	assertExitCode(t, code, 0)
	_ = out

	panes := healRun(t, ctx, tmux, sock, "display-message", "-p", "-t", "ok:0", "#{window_panes}")
	if panes != "3" {
		t.Errorf("healthy window must be left untouched, got %s panes", panes)
	}
}

// The wrapper must actually run the watcher — a heal function nobody starts
// guards nothing.
func Test_wrapper_backgrounds_pane_heal_watcher(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "wrapper.sh"))
	if err != nil {
		t.Fatalf("read wrapper.sh: %v", err)
	}
	if !strings.Contains(string(data), "gt_ensure_panes_watch") {
		t.Error("wrapper.sh must background gt_ensure_panes_watch so a failed split can never strand a one-pane tab")
	}
}
