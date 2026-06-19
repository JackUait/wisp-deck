package bash_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// Regression: when the user scrolls fast, SGR mouse reports must never leak onto
// the screen as literal text (e.g. "[<65;40;18M"). `read -s` only silences echo
// while it is actively reading; scroll events that arrive during the render gap
// get echoed by the tty's line discipline. The fix disables terminal echo for
// the interactive session (stty -echo). This test drives the REAL loop over a
// pty, fires a burst of wheel-down reports, and asserts none echo back.
func TestCompactView_does_not_echo_mouse_reports(t *testing.T) {
	module := filepath.Join(projectRoot(t), "lib", "compact-view.sh")

	// A repo with a tall modified file so the ledger overflows and scrolls.
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	writeTempFile(t, dir, "app.txt", "one\n")
	git("add", "app.txt")
	git("commit", "-q", "-m", "init")
	var tall bytes.Buffer
	for i := 0; i < 40; i++ {
		tall.WriteString("changed line\n")
	}
	writeTempFile(t, dir, "app.txt", tall.String())

	cmd := exec.Command("bash", "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if len(e) >= 5 && e[:5] == "TMUX=" {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=1", "TERM=xterm")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 12, Cols: 60})
	if err != nil {
		t.Fatalf("start pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()

	var mu sync.Mutex
	var out bytes.Buffer
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := ptmx.Read(b)
			if n > 0 {
				mu.Lock()
				out.Write(b[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	time.Sleep(600 * time.Millisecond) // let the first frame render
	// Burst of wheel-down reports with tiny gaps so many land during a render.
	for i := 0; i < 40; i++ {
		_, _ = ptmx.Write([]byte("\x1b[<65;40;18M"))
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(1200 * time.Millisecond)
	_, _ = ptmx.Write([]byte{0x03}) // Ctrl-C
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	got := out.String()
	mu.Unlock()

	leak := regexp.MustCompile(`\[<\d+;\d+;\d+M`)
	if m := leak.FindAllString(got, -1); len(m) > 0 {
		t.Errorf("mouse reports echoed to screen %d time(s) (terminal echo not disabled); first: %q",
			len(m), m[0])
	}
}

// Regression: Ctrl-C must actually EXIT the view under zsh — the live pane runs
// `zsh -c '... compact_view ...'`. In zsh, an `exit` issued from a signal trap
// that interrupted `read -t` does NOT terminate the script; the handler returns
// and the loop keeps running forever (and, having restored echo + left the
// alternate screen, resurrects the leak it was guarding against). The loop must
// break on a quit flag so the process exits. Asserting the process exits within
// a bound is the deterministic signal: the buggy build never exits, the fixed
// build does. (The echo-leak-during-operation contract is covered separately by
// TestCompactView_does_not_echo_mouse_reports.)
func TestCompactView_zsh_ctrlc_exits(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not available")
	}
	module := filepath.Join(projectRoot(t), "lib", "compact-view.sh")

	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	writeTempFile(t, dir, "app.txt", "one\n")
	git("add", "app.txt")
	git("commit", "-q", "-m", "init")
	var tall bytes.Buffer
	for i := 0; i < 40; i++ {
		tall.WriteString("changed line\n")
	}
	writeTempFile(t, dir, "app.txt", tall.String())

	// Mirror the live pane exactly: zsh -c sourcing the module.
	cmd := exec.Command(zsh, "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if len(e) >= 5 && e[:5] == "TMUX=" {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=1", "TERM=xterm")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 12, Cols: 60})
	if err != nil {
		t.Fatalf("start pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()

	// Drain output so the pty never blocks on a full buffer.
	go func() {
		b := make([]byte, 4096)
		for {
			if _, err := ptmx.Read(b); err != nil {
				return
			}
		}
	}()

	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()

	time.Sleep(700 * time.Millisecond) // first frame
	_, _ = ptmx.Write([]byte{0x03})    // Ctrl-C once

	select {
	case <-exited:
		// good: the process terminated on Ctrl-C
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("compact_view did not exit within 3s of Ctrl-C under zsh (loop kept running)")
	}
}

// stripANSI removes CSI escape sequences so frame content can be asserted on.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func lastFrame(s string) string {
	// Frames are separated by the screen-clear \033[2J. The pinned header is
	// redrawn at the top of every frame, so the last frame is what's on screen.
	if i := strings.LastIndex(s, "\x1b[2J"); i >= 0 {
		s = s[i:]
	}
	return ansiRE.ReplaceAllString(s, "")
}

// The branch heading + changed-file count must be PINNED: scrolling the file
// list must never push it off screen. Overflow the list, jump to the bottom
// with G, and assert the latest frame still shows the branch while a top file
// has scrolled away and a bottom file is visible.
func TestCompactView_header_stays_pinned_when_scrolled(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not available")
	}
	module := filepath.Join(projectRoot(t), "lib", "compact-view.sh")

	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	// One committed file so there is history, then many staged additions so the
	// list overflows a short pane.
	writeTempFile(t, dir, "seed.txt", "x\n")
	git("add", "seed.txt")
	git("commit", "-q", "-m", "init")
	git("branch", "-m", "pinnedbr") // deterministic, unique branch name
	for i := 0; i < 60; i++ {
		writeTempFile(t, dir, fmt.Sprintf("f%02d.txt", i), "x\n")
	}
	git("add", ".")

	cmd := exec.Command(zsh, "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if len(e) >= 5 && e[:5] == "TMUX=" {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=2", "TERM=xterm")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 14, Cols: 60})
	if err != nil {
		t.Fatalf("start pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()

	var mu sync.Mutex
	var out bytes.Buffer
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := ptmx.Read(b)
			if n > 0 {
				mu.Lock()
				out.Write(b[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	time.Sleep(700 * time.Millisecond) // first frame
	_, _ = ptmx.Write([]byte("G"))     // jump to bottom
	time.Sleep(400 * time.Millisecond)
	_, _ = ptmx.Write([]byte{0x03}) // Ctrl-C to exit cleanly
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	frame := lastFrame(out.String())
	mu.Unlock()

	if !strings.Contains(frame, "pinnedbr") {
		t.Errorf("branch heading must stay pinned after scrolling to bottom; frame:\n%s", frame)
	}
	if !strings.Contains(frame, "f59.txt") {
		t.Errorf("bottom of the list (f59.txt) should be visible after G; frame:\n%s", frame)
	}
	if strings.Contains(frame, "f00.txt") {
		t.Errorf("top file (f00.txt) should have scrolled away after G; frame:\n%s", frame)
	}
}
