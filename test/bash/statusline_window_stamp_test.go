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

// Each tab-view window runs its own conversation, so the durable stamp must
// also land on the pane's WINDOW: the session env is shared by every window
// and the last render to stamp it wins.

// stampTmux starts a private tmux server with one session of two windows and
// returns the real tmux path, the socket, a PATH dir whose `tmux` targets that
// socket, and the pane id of each window.
func stampTmux(t *testing.T, name string) (tmux, sock, shimDir, pane0, pane1 string) {
	t.Helper()
	tmux = closeTmux(t)
	sock = fmt.Sprintf("wisp-stamp-%s-%d", name, os.Getpid())
	_ = exec.Command(tmux, "-L", sock, "kill-server").Run()
	t.Cleanup(func() { _ = exec.Command(tmux, "-L", sock, "kill-server").Run() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pane0 = closeRun(t, ctx, tmux, sock, "new-session", "-d", "-s", "stamp", "-x", "120", "-y", "40",
		"-P", "-F", "#{pane_id}", "sleep 600")
	pane1 = closeRun(t, ctx, tmux, sock, "new-window", "-t", "=stamp:", "-P", "-F", "#{pane_id}", "sleep 600")
	shimDir = t.TempDir()
	body := "#!/bin/bash\nexec env -u TMUX " + tmux + " -L " + sock + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shimDir, "tmux"), []byte(body), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	return tmux, sock, shimDir, pane0, pane1
}

func stampPayload(t *testing.T, sid string, withModelTurn bool) string {
	t.Helper()
	body := "{\"type\":\"user\"}\n"
	if withModelTurn {
		body += "{\"type\":\"assistant\"}\n"
	}
	transcript := filepath.Join(t.TempDir(), sid+".jsonl")
	if err := os.WriteFile(transcript, []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return `{"session_id":"` + sid + `","transcript_path":"` + transcript + `","cwd":"/p/app"}`
}

func windowOpt(t *testing.T, tmux, sock, target string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return closeQuery(ctx, tmux, sock, "show-options", "-wqv", "-t", target, "@wd_claude_session")
}

func TestStatuslineStamp_sets_window_option_on_the_panes_own_window(t *testing.T) {
	tmux, sock, shimDir, pane0, pane1 := stampTmux(t, "own")
	env := buildEnv(t, []string{shimDir}, "TMUX=/private/sock,1,0", "TMUX_PANE="+pane1)
	_, code := runBashFunc(t, "lib/statusline.sh", "gt_stamp_claude_session",
		[]string{stampPayload(t, "sid-win1", true)}, env)
	assertExitCode(t, code, 0)

	if got := windowOpt(t, tmux, sock, pane1); got != "sid-win1" {
		t.Fatalf("window of %s: @wd_claude_session = %q, want sid-win1", pane1, got)
	}
	if got := windowOpt(t, tmux, sock, pane0); got != "" {
		t.Fatalf("other window %s must stay unstamped, got %q", pane0, got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	envLine := closeQuery(ctx, tmux, sock, "show-environment", "-t", "=stamp:", "WISP_DECK_CLAUDE_SESSION")
	if envLine != "WISP_DECK_CLAUDE_SESSION=sid-win1" {
		t.Fatalf("session env stamp must stay, got %q", envLine)
	}
}

func TestStatuslineStamp_window_option_obeys_the_durability_gate(t *testing.T) {
	tmux, sock, shimDir, _, pane1 := stampTmux(t, "gate")
	env := buildEnv(t, []string{shimDir}, "TMUX=/private/sock,1,0", "TMUX_PANE="+pane1)
	_, code := runBashFunc(t, "lib/statusline.sh", "gt_stamp_claude_session",
		[]string{stampPayload(t, "sid-fresh", false)}, env)
	assertExitCode(t, code, 0)

	if got := windowOpt(t, tmux, sock, pane1); got != "" {
		t.Fatalf("non-resumable transcript must not stamp the window, got %q", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	envLine := closeQuery(ctx, tmux, sock, "show-environment", "-t", "=stamp:", "WISP_DECK_CLAUDE_SESSION")
	if strings.Contains(envLine, "sid-fresh") {
		t.Fatalf("non-resumable transcript must not stamp the session env, got %q", envLine)
	}
}

func TestStatuslineStamp_without_tmux_pane_skips_window_option(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "tmux", fmt.Sprintf(`echo "$@" >> %q`, rec))
	env := buildEnv(t, []string{binDir}, "TMUX=/tmp/sock,1,0", "TMUX_PANE=")
	_, code := runBashFunc(t, "lib/statusline.sh", "gt_stamp_claude_session",
		[]string{stampPayload(t, "sid-42", true)}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("tmux not invoked: %v", err)
	}
	assertContains(t, string(data), "set-environment WISP_DECK_CLAUDE_SESSION sid-42")
	assertNotContains(t, string(data), "@wd_claude_session")
}

// One render, one tmux client: the statusline repaints constantly across
// every session, so the window stamp rides in the same invocation.
func TestStatuslineStamp_both_stamps_share_one_tmux_call(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "tmux", fmt.Sprintf(`echo "$@" >> %q`, rec))
	env := buildEnv(t, []string{binDir}, "TMUX=/tmp/sock,1,0", "TMUX_PANE=%7")
	_, code := runBashFunc(t, "lib/statusline.sh", "gt_stamp_claude_session",
		[]string{stampPayload(t, "sid-42", true)}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("tmux not invoked: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("want one tmux call, got %d:\n%s", len(lines), data)
	}
	assertContains(t, lines[0], "set-environment WISP_DECK_CLAUDE_SESSION sid-42")
	assertContains(t, lines[0], "if-shell -F -t %7 #{!=:#{@wd_claude_session},sid-42} set-option -w -t %7 @wd_claude_session sid-42")
}
