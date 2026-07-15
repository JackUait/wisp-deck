package bash_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// This is the real-tmux regression guard for exact pane-position restore.
//
// The mocked wrapper/snapshot tests prove the plumbing (the layout field is
// captured, threaded through the queue, and handed to `select-layout`). They
// do NOT prove the mechanism the whole feature rests on: that capturing tmux's
// #{window_layout} and replaying it with `select-layout` actually reproduces
// the pane geometry byte-for-byte. This test exercises that against the real
// tmux binary so a future tmux change — or a regression in the pane build
// order — can never silently reintroduce the "panes come back at the defaults"
// bug.
//
// Skips (never fails) when tmux is unavailable, e.g. minimal CI.

// rt runs a tmux command on an isolated server socket, failing the test on error.
func rt(t *testing.T, ctx context.Context, tmux, sock string, args ...string) string {
	t.Helper()
	full := append([]string{"-L", sock}, args...)
	out, err := exec.CommandContext(ctx, tmux, full...).CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// buildPanes reproduces wrapper.sh's exact three-pane construction on window 0
// of the named session: pane 0 (top-left), split -h for the AI pane, then
// split -v under the left pane for the spare. The build order must match
// wrapper.sh so captured layouts map cleanly back onto rebuilt panes.
func buildPanes(t *testing.T, ctx context.Context, tmux, sock, sess string) {
	t.Helper()
	rt(t, ctx, tmux, sock, "new-session", "-d", "-s", sess, "-x", "200", "-y", "50", "cat")
	rt(t, ctx, tmux, sock, "split-window", "-h", "-p", "75", "-t", sess+":0", "cat")
	rt(t, ctx, tmux, sock, "select-pane", "-L", "-t", sess+":0")
	rt(t, ctx, tmux, sock, "split-window", "-v", "-p", "45", "-t", sess+":0", "cat")
	rt(t, ctx, tmux, sock, "select-pane", "-R", "-t", sess+":0")
}

// geom returns the sorted per-pane rectangle of window 0 — the ground truth we
// compare capture against restore.
func geom(t *testing.T, ctx context.Context, tmux, sock, sess string) string {
	t.Helper()
	return rt(t, ctx, tmux, sock, "list-panes", "-t", sess+":0",
		"-F", "#{pane_index} #{pane_left} #{pane_top} #{pane_width} #{pane_height}")
}

func TestLayoutRoundtrip_select_layout_reproduces_exact_geometry(t *testing.T) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sock := fmt.Sprintf("wisp-rt-%d", os.Getpid())
	// Kill any stale server on this socket up front and always on cleanup.
	_ = exec.Command(tmux, "-L", sock, "kill-server").Run()
	t.Cleanup(func() { _ = exec.Command(tmux, "-L", sock, "kill-server").Run() })

	// Session A: build the panes, then simulate the user dragging borders to a
	// NON-default arrangement (so a passing test cannot be a coincidence of the
	// defaults matching).
	buildPanes(t, ctx, tmux, sock, "A")
	rt(t, ctx, tmux, sock, "resize-pane", "-t", "A:0.2", "-x", "120")
	rt(t, ctx, tmux, sock, "resize-pane", "-t", "A:0.0", "-y", "10")
	want := geom(t, ctx, tmux, sock, "A")

	// Capture EXACTLY as lib/session-restore.sh's write_session_snapshot does.
	layout := rt(t, ctx, tmux, sock, "display-message", "-p", "-t", "A:0", "#{window_layout}")
	if layout == "" {
		t.Fatal("captured empty window_layout")
	}

	// Session B: fresh build at the SAME size with default splits (deliberately
	// different geometry), then replay the captured layout exactly as wrapper.sh
	// does on restore.
	buildPanes(t, ctx, tmux, sock, "B")
	before := geom(t, ctx, tmux, sock, "B")
	if before == want {
		t.Fatal("defaults already match the resized layout; test cannot detect a regression")
	}
	rt(t, ctx, tmux, sock, "select-layout", "-t", "B:0", layout)
	got := geom(t, ctx, tmux, sock, "B")

	if got != want {
		t.Errorf("restored geometry does not match captured geometry\n want:\n%s\n got:\n%s\nlayout: %s",
			want, got, layout)
	}
}

// TestDetachedSessionLifecycle verifies the tmux command-queue behavior that
// lets wrapper.sh avoid painting its one-pane construction state. The first
// client attaches only after all panes exist and the AI pane is active; arming
// exit-unattached after that attachment still tears down the session normally.
func TestDetachedSessionLifecycle(t *testing.T) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	sock := fmt.Sprintf("wisp-detached-%d", os.Getpid())
	sess := "C"
	_ = exec.Command(tmux, "-L", sock, "kill-server").Run()
	t.Cleanup(func() { _ = exec.Command(tmux, "-L", sock, "kill-server").Run() })

	cmd := exec.CommandContext(ctx, tmux,
		"-L", sock,
		"new-session", "-d", "-s", sess, "-x", "120", "-y", "30", "sleep 30",
		";", "split-window", "-h", "-p", "75", "sleep 30",
		";", "set-option", "-p", "@gt_ai", "1",
		";", "select-pane", "-L",
		";", "split-window", "-v", "-p", "45", "sleep 30",
		";", "select-pane", "-R",
		";", "attach-session", "-t", sess,
		";", "set-option", "exit-unattached", "on",
	)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 120})
	if err != nil {
		t.Fatalf("start attached tmux queue: %v", err)
	}
	defer ptmx.Close()
	go func() { _, _ = io.Copy(io.Discard, ptmx) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		clients, listErr := exec.Command(tmux, "-L", sock, "list-clients", "-F", "#{client_session}").CombinedOutput()
		if listErr == nil && strings.Contains(string(clients), sess) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tmux client did not attach: %v: %s", listErr, clients)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if panes := rt(t, ctx, tmux, sock, "display-message", "-p", "-t", sess+":0", "#{window_panes}"); panes != "3" {
		t.Fatalf("attached with %s panes, want complete three-pane layout", panes)
	}
	active := rt(t, ctx, tmux, sock, "list-panes", "-t", sess+":0", "-F", "#{pane_active} #{@gt_ai}")
	if !strings.Contains(active, "1 1") {
		t.Fatalf("AI pane was not active on attachment:\n%s", active)
	}
	if option := rt(t, ctx, tmux, sock, "show-options", "-qv", "-t", sess, "exit-unattached"); option != "on" {
		t.Fatalf("exit-unattached = %q, want on after attachment", option)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if _, err := ptmx.Write([]byte{0x02, 'd'}); err != nil {
		t.Fatalf("detach tmux client: %v", err)
	}
	select {
	case waitErr := <-done:
		if waitErr != nil {
			t.Fatalf("attached tmux command exited with error: %v", waitErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("attached tmux command did not exit after detach")
	}

	deadline = time.Now().Add(2 * time.Second)
	for exec.Command(tmux, "-L", sock, "has-session", "-t", sess).Run() == nil {
		if time.Now().After(deadline) {
			t.Fatal("exit-unattached left the detached session alive")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
