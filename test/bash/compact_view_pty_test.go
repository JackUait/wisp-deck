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

func init() {
	// This file is the parity suite for the retained shell renderer. Interactive
	// compact_view now selects the native renderer when an installed binary
	// advertises it, so pin these legacy assertions to the documented fallback.
	// Native PTY behavior is exercised separately in native_ledger_pty_test.go.
	_ = os.Setenv("WISP_DECK_LEDGER_SHELL_FALLBACK", "1")
}

// waitForFrame drains the pty until a frame containing want shows up, or the
// deadline passes. A fixed sleep asserts how FAST the render is, which a loaded
// 3-core CI runner will lose; polling asserts that it renders at all.
func waitForFrame(read func() string, want string, timeout time.Duration) (string, time.Duration, bool) {
	start := time.Now()
	var acc strings.Builder
	for {
		acc.WriteString(read())
		if strings.Contains(acc.String(), want) {
			return acc.String(), time.Since(start), true
		}
		if time.Since(start) >= timeout {
			return acc.String(), time.Since(start), false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;?<>]*[a-zA-Z]|\x1b[()][AB012]|\x1b.`)

// describeFrame renders a captured pty frame as the grid a human would see:
// one numbered row per screen line, with the escape codes stripped. A %q dump of
// the raw bytes is unreadable and hides WHERE things landed, which is the only
// question worth asking when a mouse hit-test misses.
func describeFrame(raw string) string {
	frames := strings.Split(raw, "\x1b[H") // cursor-home starts each repaint
	last := frames[len(frames)-1]
	var b strings.Builder
	for i, line := range strings.Split(last, "\r\n") {
		text := ansiSeq.ReplaceAllString(line, "")
		mark := ""
		if strings.Contains(text, "\U000f0004") {
			mark = "  <- the account pill is on this row"
		}
		fmt.Fprintf(&b, "  row %2d: %q%s\n", i+1, text, mark)
	}
	return b.String()
}

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

// Regression: scrolling the file list must not BLINK. The redraw used to begin
// every frame with a full-screen erase (\033[2J), which blanks the whole pane
// for one frame before the content is reprinted — a visible flicker on every
// scroll step. The flicker-free redraw homes the cursor (\033[H) and overwrites
// each row in place (\033[K per line, \033[J to drop trailing rows), so the
// screen is never blanked. This drives the real loop, scrolls a tall list, and
// asserts the session never emits a single \033[2J.
func TestCompactView_does_not_blank_screen_on_scroll(t *testing.T) {
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
	writeTempFile(t, dir, "seed.txt", "x\n")
	git("add", "seed.txt")
	git("commit", "-q", "-m", "init")
	// Many staged files so the ledger overflows a short pane and scrolls.
	for i := 0; i < 60; i++ {
		writeTempFile(t, dir, fmt.Sprintf("f%02d.txt", i), "x\n")
	}
	git("add", ".")

	cmd := exec.Command("bash", "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if len(e) >= 5 && e[:5] == "TMUX=" {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=2", "TERM=xterm")

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

	time.Sleep(600 * time.Millisecond) // first frame
	// Scroll down a handful of steps, each forcing a redraw.
	for i := 0; i < 8; i++ {
		_, _ = ptmx.Write([]byte("j"))
		time.Sleep(40 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	_, _ = ptmx.Write([]byte{0x03}) // Ctrl-C
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	got := out.String()
	mu.Unlock()

	if n := strings.Count(got, "\x1b[2J"); n > 0 {
		t.Errorf("redraw emitted full-screen erase \\033[2J %d time(s); the list blinks on every scroll. "+
			"Home the cursor and overwrite in place instead.", n)
	}
}

// Regression: the hovered-row highlight must not BLINK while the list scrolls.
// The loop cleared the hover on every keystroke (hover_line=0) and only a mouse
// MOTION report re-set it. A scroll gesture interleaves wheel reports with the
// incidental motion reports a trackpad/mouse emits as the cursor drifts, so the
// selection bar flipped off (wheel frame) then on (motion frame) on every step —
// a visible blink. The fix re-derives the hover from the wheel report's own
// cursor row, so a wheel frame keeps the highlight on the file under the cursor.
// This drives the real loop under zsh, interleaves motion+wheel like a real
// scroll, and asserts the highlight never drops out once it has appeared.
func TestCompactView_hover_highlight_does_not_blink_on_scroll(t *testing.T) {
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
	writeTempFile(t, dir, "seed.txt", "x\n")
	git("add", "seed.txt")
	git("commit", "-q", "-m", "init")
	for i := 0; i < 60; i++ {
		writeTempFile(t, dir, fmt.Sprintf("f%02d.txt", i), "x\n")
	}
	git("add", ".")

	cmd := exec.Command(zsh, "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") {
			continue
		}
		env = append(env, e)
	}
	// Long interval so the timed rebuild never interleaves with the scroll burst.
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=5", "TERM=xterm")

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

	// Wait for the first frame to actually render before resetting (poll, don't
	// race a fixed sleep) so the reset can't clear a not-yet-drawn frame and leave
	// the scroll burst with nothing to show.
	contains := func(sub string) bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(out.String(), sub)
	}
	for i := 0; i < 40 && !contains("staged"); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	out.Reset() // capture only the scroll frames
	mu.Unlock()

	// A scroll gesture: hover-motion (cursor over row 5) interleaved with
	// wheel-down, both carrying the cursor position. Row 5 sits over a file row
	// for the whole scroll, so the highlight should persist on every frame.
	for i := 0; i < 6; i++ {
		_, _ = ptmx.Write([]byte("\x1b[<35;12;5M")) // motion (hover)
		time.Sleep(25 * time.Millisecond)
		_, _ = ptmx.Write([]byte("\x1b[<65;12;5M")) // wheel down
		time.Sleep(25 * time.Millisecond)
	}
	// Wait for the WHOLE burst to settle (output goes quiet) before quitting.
	// Ctrl-C must NOT land mid-burst: SIGINT interrupts an in-flight mouse-report
	// read, which truncates that report and blanks its hover — a test-only
	// artifact, not a real blink. Quiescence-poll instead of a fixed sleep so heavy
	// CPU load (which slows event processing) can't race Ctrl-C into the burst.
	outLen := func() int { mu.Lock(); defer mu.Unlock(); return out.Len() }
	prev, stable := -1, 0
	for i := 0; i < 40 && stable < 3; i++ {
		time.Sleep(50 * time.Millisecond)
		if n := outLen(); n == prev {
			stable++
		} else {
			prev, stable = n, 0
		}
	}
	_, _ = ptmx.Write([]byte{0x03}) // Ctrl-C
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	got := out.String()
	mu.Unlock()

	// The hover highlight is an SGR background (48;5;238). Walk the scroll frames
	// (split at each cursor-home) and, once the highlight has appeared, assert no
	// later frame drops it — a drop-then-return is the blink.
	frames := strings.Split(got, "\x1b[H")
	seen := false
	blinks := 0
	for _, f := range frames {
		if f == "" {
			continue
		}
		hl := strings.Contains(f, "48;5;238")
		if hl {
			seen = true
		} else if seen {
			blinks++ // highlight was on, this scroll frame has it off -> blink
		}
	}
	if !seen {
		t.Fatalf("hover highlight never appeared; test cannot assess blinking")
	}
	if blinks > 0 {
		t.Errorf("hover highlight blinked off on %d scroll frame(s); it must stay on the "+
			"file under the cursor while scrolling", blinks)
	}
}

// Regression: the hover highlight must FLY, not crawl. Under any-motion mouse
// tracking (\033[?1003h) the terminal emits one report per cursor cell, so a
// single fast mouse move buffers a BURST of motion reports. The loop used to
// process one report per iteration and repaint the whole pane for EVERY one, so
// a fast move queued dozens of full redraws and the selection bar drained the
// backlog long after the cursor had stopped — visible lag. The fix coalesces:
// it drains every already-buffered report and repaints ONCE for the settled
// position. This writes a burst of 30 distinct-row motion reports in a single
// write (so they all sit in the tty buffer at once) and asserts the loop emits
// only a handful of frames — not ~one per report — while still landing the
// highlight on the LAST report's row.
func TestCompactView_coalesces_motion_flood_into_few_redraws(t *testing.T) {
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
	writeTempFile(t, dir, "seed.txt", "x\n")
	git("add", "seed.txt")
	git("commit", "-q", "-m", "init")
	for i := 0; i < 60; i++ {
		writeTempFile(t, dir, fmt.Sprintf("f%02d.txt", i), "x\n")
	}
	git("add", ".")

	cmd := exec.Command(zsh, "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") {
			continue
		}
		env = append(env, e)
	}
	// Long interval so no timed rebuild interleaves and inflates the frame count.
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=5", "TERM=xterm")

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

	// Wait for the first frame to actually render (poll, don't race a fixed
	// sleep), then reset so we count only the frames the burst causes.
	contains := func(sub string) bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(out.String(), sub)
	}
	for i := 0; i < 40 && !contains("staged"); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	out.Reset()
	mu.Unlock()

	// One write = 30 motion reports over consecutive (changing) rows, so every
	// report WOULD move the highlight (no same-row skip) and the buggy build
	// repaints once per report. Rows 4..9 all sit over file rows.
	var burst bytes.Buffer
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&burst, "\x1b[<35;12;%dM", []int{4, 5, 6, 7, 8, 9}[i%6])
	}
	_, _ = ptmx.Write(burst.Bytes())

	// Poll for the settled highlight to render — generous so heavy CPU load can't
	// race the assertion (the repaint may lag behind the input under contention).
	for i := 0; i < 40 && !contains("48;5;238"); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	_, _ = ptmx.Write([]byte{0x03}) // Ctrl-C
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	got := out.String()
	mu.Unlock()

	// The CORE contract: a 30-report flood collapses into a HANDFUL of repaints,
	// not ~one per report. Coalescing yields 1-2 here; the buggy build yielded 30.
	// The threshold sits well below 30 but above the coalesced count so scheduling
	// jitter (which can split the flood into a couple of batches) never trips it.
	frames := strings.Count(got, "\x1b[H")
	if frames < 1 || frames > 12 {
		t.Errorf("a 30-report buffered motion flood produced %d redraw frames; the loop "+
			"should coalesce them into a handful (the buggy build repainted per report, ~30, "+
			"and the highlight crawled a backlog behind the cursor).", frames)
	}
	// And the flood must have been PROCESSED to a settled highlight, not ignored.
	if !strings.Contains(got, "48;5;238") {
		t.Errorf("after the motion flood the hover highlight (48;5;238) never rendered; " +
			"the coalesced repaint dropped the settled cursor position.")
	}
}

// Regression: hovering a file row and then moving the pointer SIDEWAYS out of
// the file list must clear the highlight. The outer tmux runs with mouse OFF, so
// the active ledger pane receives motion reports for the WHOLE terminal width —
// including cursor positions over the neighbouring AI pane to its right. The
// hover derivation keyed on the report's ROW alone, ignoring the column, so a
// cursor that drifted right into the AI pane at the same vertical level as a file
// left that row lit forever (no in-pane event ever arrived to clear it). The fix
// bounds the hover to this pane's width: a report whose column exceeds the pane
// width clears the highlight. Drives the REAL loop under zsh, hovers a row, then
// fires a same-row motion report with a column far past the pane edge and asserts
// the highlight (SGR 48;5;238) goes away.
func TestCompactView_hover_clears_when_pointer_leaves_pane_sideways(t *testing.T) {
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
	git("checkout", "-q", "-b", "main")
	writeTempFile(t, dir, "a.txt", "base\n")
	git("add", "a.txt")
	git("commit", "-q", "-m", "init")
	writeTempFile(t, dir, "a.txt", "base\nDIRTY\n")

	cmd := exec.Command(zsh, "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") {
			continue
		}
		env = append(env, e)
	}
	// Long interval so no timed rebuild interleaves and repaints between the two
	// motion reports (which could mask the clear/stale distinction).
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=5", "TERM=xterm")

	const cols = 60
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 12, Cols: cols})
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

	// The current frame is the slice from the last cursor-home (each redraw begins
	// with \033[H); keep the ANSI so the highlight SGR is visible.
	lastRawFrame := func() string {
		mu.Lock()
		defer mu.Unlock()
		s := out.String()
		if i := strings.LastIndex(s, "\x1b[H"); i >= 0 {
			return s[i:]
		}
		return s
	}
	frameHasHighlight := func() bool { return strings.Contains(lastRawFrame(), "48;5;238") }

	// Wait for the first frame.
	for i := 0; i < 40 && !strings.Contains(lastRawFrame(), "modified"); i++ {
		time.Sleep(50 * time.Millisecond)
	}

	// Hover a.txt's row: there is no pinned header now, so row 1 is the
	// "modified" group header and row 2 the file. Column 10 is well inside the
	// 60-col pane.
	_, _ = ptmx.Write([]byte("\x1b[<35;10;2M"))
	lit := false
	for i := 0; i < 40; i++ {
		if frameHasHighlight() {
			lit = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !lit {
		t.Fatalf("hover over the file row never lit the highlight; cannot test the clear.\nframe:\n%q", lastRawFrame())
	}

	// Move the pointer SIDEWAYS into the neighbouring pane: SAME row (2), but a
	// column far past the pane's right edge (200 > 60). With mouse off in the outer
	// tmux this report still reaches the active ledger; the highlight must clear.
	_, _ = ptmx.Write([]byte("\x1b[<35;200;2M"))
	cleared := false
	for i := 0; i < 40; i++ {
		if !frameHasHighlight() {
			cleared = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	_, _ = ptmx.Write([]byte{0x03}) // Ctrl-C
	time.Sleep(200 * time.Millisecond)

	if !cleared {
		t.Errorf("the file row stayed highlighted after the pointer moved sideways out of the "+
			"list (column past the pane width). Hover must be bounded to this pane's width, not "+
			"keyed on the row alone.\nframe:\n%q", lastRawFrame())
	}
}

// stripANSI removes CSI escape sequences so frame content can be asserted on.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func lastFrame(s string) string {
	// Every frame begins by homing the cursor (\033[H) and overwriting in place
	// (no \033[2J — that would blink the screen). The pinned header is redrawn at
	// the top of every frame, so the slice from the last home is what's on screen.
	if i := strings.LastIndex(s, "\x1b[H"); i >= 0 {
		s = s[i:]
	}
	return ansiRE.ReplaceAllString(s, "")
}

// The pinned chrome must stay put when scrolling: the changed-file stamp is
// pinned at the top and the bottom bar below, so neither is ever pushed off
// screen. Overflow the list, jump to the bottom with G, and assert the latest
// frame still shows the stamp heading — never the branch name, which lives in
// the Claude statusline — while a top file has scrolled away and a bottom file
// is visible.
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

	// Poll for the first frame, then jump to the bottom and poll until it lands —
	// fixed sleeps race the startup git-build under load (the jump may not have
	// repainted yet when sampled).
	contains := func(sub string) bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(out.String(), sub)
	}
	for i := 0; i < 60 && !contains("f00.txt"); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	_, _ = ptmx.Write([]byte("G")) // jump to bottom
	for i := 0; i < 60 && !contains("f59.txt"); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	_, _ = ptmx.Write([]byte{0x03}) // Ctrl-C to exit cleanly
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	frame := lastFrame(out.String())
	mu.Unlock()

	if !strings.Contains(frame, "60 files") {
		t.Errorf("stamp heading must stay pinned after scrolling to bottom; frame:\n%s", frame)
	}
	if strings.Contains(frame, "pinnedbr") {
		t.Errorf("ledger must not name the branch anywhere; frame:\n%s", frame)
	}
	if !strings.Contains(frame, "f59.txt") {
		t.Errorf("bottom of the list (f59.txt) should be visible after G; frame:\n%s", frame)
	}
	if strings.Contains(frame, "f00.txt") {
		t.Errorf("top file (f00.txt) should have scrolled away after G; frame:\n%s", frame)
	}
}

// End-to-end: select several files with the keyboard toggle and discard them
// together. Drives the REAL loop over a pty — hover a.txt's row and press 'x',
// hover b.txt's row and press 'x' (marking both), then 'd' to arm the confirm
// and 'y' to run it — and asserts BOTH selected files reverted to HEAD while the
// unselected c.txt keeps its working-tree edit. This exercises the whole wiring:
// hover→path mapping, toggle_selection, the armed confirm, and the batch restore.
func TestCompactView_multiselect_discards_selected_files(t *testing.T) {
	// The live pane runs zsh, and the mouse-report follow-up reads (read -k) that
	// map a hover to a file row depend on zsh's semantics, so drive the loop under
	// zsh exactly like the hover tests above.
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
	git("checkout", "-q", "-b", "main")
	for _, f := range []string{"a.txt", "b.txt", "c.txt"} {
		writeTempFile(t, dir, f, "base\n")
		git("add", f)
	}
	git("commit", "-q", "-m", "init")
	// Modify all three so the ledger lists them (alphabetical numstat order).
	for _, f := range []string{"a.txt", "b.txt", "c.txt"} {
		writeTempFile(t, dir, f, "base\nDIRTY\n")
	}

	cmd := exec.Command(zsh, "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if len(e) >= 5 && e[:5] == "TMUX=" {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=5", "TERM=xterm")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 12, Cols: 60})
	if err != nil {
		t.Fatalf("start pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()

	go func() {
		b := make([]byte, 4096)
		for {
			if _, err := ptmx.Read(b); err != nil {
				return
			}
		}
	}()

	// Body layout with no pinned header: screen row 1 is the "modified" group
	// header, row 2 = a.txt, row 3 = b.txt, row 4 = c.txt. A no-button SGR
	// motion report (button 35) sets the hover to the row's file.
	hover := func(row int) {
		_, _ = ptmx.Write([]byte(fmt.Sprintf("\x1b[<35;10;%dM", row)))
		time.Sleep(120 * time.Millisecond)
	}

	time.Sleep(700 * time.Millisecond) // first frame

	hover(2)                       // a.txt
	_, _ = ptmx.Write([]byte("x")) // select a.txt
	time.Sleep(120 * time.Millisecond)
	hover(3)                       // b.txt
	_, _ = ptmx.Write([]byte("x")) // select b.txt
	time.Sleep(120 * time.Millisecond)
	_, _ = ptmx.Write([]byte("d")) // arm the confirm
	time.Sleep(150 * time.Millisecond)
	_, _ = ptmx.Write([]byte("y")) // confirm the batch discard
	time.Sleep(500 * time.Millisecond)

	_, _ = ptmx.Write([]byte{0x03}) // Ctrl-C
	time.Sleep(300 * time.Millisecond)

	read := func(f string) string {
		b, _ := os.ReadFile(filepath.Join(dir, f))
		return string(b)
	}
	if got := read("a.txt"); got != "base\n" {
		t.Errorf("a.txt should be reverted after batch discard: got %q, want %q", got, "base\n")
	}
	if got := read("b.txt"); got != "base\n" {
		t.Errorf("b.txt should be reverted after batch discard: got %q, want %q", got, "base\n")
	}
	if got := read("c.txt"); got != "base\nDIRTY\n" {
		t.Errorf("unselected c.txt must keep its edit: got %q", got)
	}
}

// End-to-end, FULLY MOUSE-DRIVEN: click the checkbox to the left of two file rows
// to mark them, click the "[ discard N ]" button that now sits at the TOP of the
// file list — next to the first group header ("modified") — to arm the confirm,
// then click "[ yes ]" to run it — no keyboard at all. Asserts the button and its
// confirm render on the group-header row (not the bottom bar) and that the two
// clicked files reverted to HEAD while the unclicked one keeps its edit. Exercises
// the new click wiring: checkbox-slot toggle, the top discard button span, and the
// yes/no confirm spans overlaid on the group title.
func TestCompactView_mouse_marks_and_discards(t *testing.T) {
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
	git("checkout", "-q", "-b", "main")
	for _, f := range []string{"a.txt", "b.txt", "c.txt"} {
		writeTempFile(t, dir, f, "base\n")
		git("add", f)
	}
	git("commit", "-q", "-m", "init")
	for _, f := range []string{"a.txt", "b.txt", "c.txt"} {
		writeTempFile(t, dir, f, "base\nDIRTY\n")
	}

	cmd := exec.Command(zsh, "-c", "source "+module+" && compact_view "+dir)
	// Strip TMUX and the relaunch-context file so NO account pill renders — the
	// pill's width is environment-dependent and would shift the group header's
	// (and thus the discard button's) columns.
	env := []string{}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") || strings.HasPrefix(e, "WISP_DECK_RELAUNCH_FILE=") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=5", "TERM=xterm")

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
	frame := func() string {
		mu.Lock()
		defer mu.Unlock()
		return lastFrame(out.String())
	}

	// A left-PRESS SGR report (button 0, terminator M) at (col,row).
	click := func(col, row int) {
		_, _ = ptmx.Write([]byte(fmt.Sprintf("\x1b[<0;%d;%dM", col, row)))
		time.Sleep(180 * time.Millisecond)
	}

	deadline := time.Now().Add(6 * time.Second)
	for !strings.Contains(frame(), "modified") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(frame(), "modified") {
		t.Fatalf("ledger did not paint before mouse input:\n%s", frame())
	}

	// Body (no pinned header): row 1 = "modified" header, row 2 = a.txt,
	// row 3 = b.txt, row 4 = c.txt. The checkbox lives in the left indent
	// (cols 1-3), so a click at col 2 toggles the row's mark.
	click(2, 2) // mark a.txt
	click(2, 3) // mark b.txt

	// The "[ discard 2 ]" button now rides the group-header row NEXT TO the title,
	// not the bottom bar. Assert it renders on the SAME line as "modified".
	headerLine := ""
	for _, ln := range strings.Split(frame(), "\n") {
		if strings.Contains(ln, "modified") {
			headerLine = ln
			break
		}
	}
	if !strings.Contains(headerLine, "discard 2") {
		t.Errorf("discard button should sit next to the group title; got header line %q\nframe:\n%s", headerLine, frame())
	}

	// The header " ● modified  (3)" is 16 cols; a 2-space gap then "[ discard 2 ]"
	// (13 cols) spans cols 19-31 on row 1 (the group header, now the first row).
	// Click inside it to arm the confirm.
	click(25, 1)

	// The confirm overlays the same row: "  Discard 2 files? [ yes ] [ no ]"; the
	// "[ yes ]" button spans cols 36-42. Assert it landed on the group-header row.
	confirmLine := ""
	for _, ln := range strings.Split(frame(), "\n") {
		if strings.Contains(ln, "Discard 2 files?") {
			confirmLine = ln
			break
		}
	}
	if confirmLine == "" {
		t.Errorf("arming should draw the confirm at the top of the list; frame:\n%s", frame())
	}
	click(39, 1) // click [ yes ]
	time.Sleep(500 * time.Millisecond)

	_, _ = ptmx.Write([]byte{0x03}) // Ctrl-C
	time.Sleep(300 * time.Millisecond)

	read := func(f string) string {
		b, _ := os.ReadFile(filepath.Join(dir, f))
		return string(b)
	}
	if got := read("a.txt"); got != "base\n" {
		t.Errorf("a.txt should be reverted after mouse discard: got %q, want %q", got, "base\n")
	}
	if got := read("b.txt"); got != "base\n" {
		t.Errorf("b.txt should be reverted after mouse discard: got %q, want %q", got, "base\n")
	}
	if got := read("c.txt"); got != "base\nDIRTY\n" {
		t.Errorf("unclicked c.txt must keep its edit: got %q", got)
	}
}

// Uncheck: a marked file's checkbox toggles OFF on a second click, removing it from
// the discard set. Marks a.txt, clicks its box AGAIN to unmark it, marks b.txt, then
// discards — so only b.txt reverts while the unchecked a.txt keeps its edit. Fully
// mouse-driven; proves the checkbox is a true toggle, not a one-way mark.
func TestCompactView_mouse_uncheck_removes_mark(t *testing.T) {
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
	git("checkout", "-q", "-b", "main")
	for _, f := range []string{"a.txt", "b.txt", "c.txt"} {
		writeTempFile(t, dir, f, "base\n")
		git("add", f)
	}
	git("commit", "-q", "-m", "init")
	for _, f := range []string{"a.txt", "b.txt", "c.txt"} {
		writeTempFile(t, dir, f, "base\nDIRTY\n")
	}

	cmd := exec.Command(zsh, "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") || strings.HasPrefix(e, "WISP_DECK_RELAUNCH_FILE=") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=5", "TERM=xterm")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 12, Cols: 60})
	if err != nil {
		t.Fatalf("start pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()

	go func() {
		b := make([]byte, 4096)
		for {
			if _, err := ptmx.Read(b); err != nil {
				return
			}
		}
	}()

	click := func(col, row int) {
		_, _ = ptmx.Write([]byte(fmt.Sprintf("\x1b[<0;%d;%dM", col, row)))
		time.Sleep(180 * time.Millisecond)
	}

	time.Sleep(700 * time.Millisecond) // first frame

	// Row 2 = a.txt, row 3 = b.txt (no pinned header). Mark a.txt, then click its
	// box AGAIN to unmark, then mark b.txt — leaving ONLY b.txt in the discard set.
	click(2, 2) // mark a.txt
	click(2, 2) // UNMARK a.txt
	click(2, 3) // mark b.txt

	// With one file marked the button spans cols 19-31 on row 1 (the group
	// header, now the first row); arm and confirm.
	click(25, 1) // [ discard 1 ]
	click(39, 1) // [ yes ]  — pre "Discard 1 file? " is 16 cols, so yes spans 35-41
	time.Sleep(500 * time.Millisecond)

	_, _ = ptmx.Write([]byte{0x03}) // Ctrl-C
	time.Sleep(300 * time.Millisecond)

	read := func(f string) string {
		b, _ := os.ReadFile(filepath.Join(dir, f))
		return string(b)
	}
	if got := read("a.txt"); got != "base\nDIRTY\n" {
		t.Errorf("unchecked a.txt must keep its edit (not be discarded): got %q", got)
	}
	if got := read("b.txt"); got != "base\n" {
		t.Errorf("b.txt should be reverted after discard: got %q, want %q", got, "base\n")
	}
	if got := read("c.txt"); got != "base\nDIRTY\n" {
		t.Errorf("untouched c.txt must keep its edit: got %q", got)
	}
}

// Discoverability: hovering a file row reveals a checkbox (☐) to its LEFT — the
// clickable mark affordance — and it is gone when nothing is hovered (so the idle
// view stays box-free). Drives the real loop under zsh, checks the idle frame has
// no box, then hovers a file row and asserts the ☐ shows up on that row.
func TestCompactView_shows_hover_checkbox(t *testing.T) {
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
	git("checkout", "-q", "-b", "main")
	writeTempFile(t, dir, "a.txt", "base\n")
	git("add", "a.txt")
	git("commit", "-q", "-m", "init")
	writeTempFile(t, dir, "a.txt", "base\nDIRTY\n")

	cmd := exec.Command(zsh, "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if len(e) >= 5 && e[:5] == "TMUX=" {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=5", "TERM=xterm")

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

	time.Sleep(700 * time.Millisecond) // idle first frame
	mu.Lock()
	idle := out.String()
	out.Reset()
	mu.Unlock()
	if strings.Contains(idle, "☐") {
		t.Errorf("idle frame (no hover) should not show a checkbox; got:\n%s", idle)
	}

	// Hover the single file row: screen row 2 (row 1 is the "modified" header,
	// no pinned header above it).
	_, _ = ptmx.Write([]byte("\x1b[<35;10;2M"))
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	hovered := out.String()
	mu.Unlock()

	_, _ = ptmx.Write([]byte{0x03}) // Ctrl-C
	time.Sleep(200 * time.Millisecond)

	if !strings.Contains(hovered, "☐") {
		t.Errorf("hovering a file row should reveal a ☐ checkbox on it; got:\n%s", hovered)
	}
}

// In a CLAUDE pane the branch NAME and the push/pull commit counts both live in
// the statusline, and there is no pinned top header at all: the changed-file
// stamp rides the BOTTOM bar below the listed files, right-aligned. Drives the
// real loop under zsh with an upstream two commits ahead, and asserts the first
// row is the group header (not a stamp), no line names the branch or the
// divergence, the last content line carries the stamp, and the file list sits
// above it. (A Codex pane keeps the counts — see
// TestCompactView_bottom_bar_divergence_follows_the_tool.)
func TestCompactView_stamp_at_bottom(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not available")
	}
	module := filepath.Join(projectRoot(t), "lib", "compact-view.sh")

	dir := t.TempDir()
	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	gitIn := func(wd string, args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", wd}, args...)...)
		c.Env = gitEnv
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git := func(args ...string) { gitIn(dir, args...) }

	git("init", "-q")
	git("checkout", "-q", "-b", "main")
	writeTempFile(t, dir, "a.txt", "base\n")
	git("add", "a.txt")
	git("commit", "-q", "-m", "init")
	// Upstream via a bare remote so @{u} resolves; push at the init commit, set
	// upstream, then add two local commits so the branch is ahead by 2 (to push).
	bare := filepath.Join(t.TempDir(), "r.git")
	c := exec.Command("git", "init", "--bare", "-q", bare)
	c.Env = gitEnv
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	git("remote", "add", "origin", bare)
	git("push", "-q", "origin", "main")
	git("branch", "--set-upstream-to=origin/main", "main")
	git("commit", "-q", "--allow-empty", "-m", "ahead1")
	git("commit", "-q", "--allow-empty", "-m", "ahead2")
	// Dirty a.txt so the ledger lists a file above the bottom bar.
	writeTempFile(t, dir, "a.txt", "base\nDIRTY\n")

	cmd := exec.Command(zsh, "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if len(e) >= 5 && e[:5] == "TMUX=" {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=5", "TERM=xterm")

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

	time.Sleep(800 * time.Millisecond) // first frame
	_, _ = ptmx.Write([]byte{0x03})    // Ctrl-C
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	frame := lastFrame(out.String())
	mu.Unlock()

	// Non-empty, trailing-space-trimmed content lines, top to bottom.
	var lines []string
	for _, ln := range strings.Split(frame, "\n") {
		if s := strings.TrimRight(ln, " "); strings.TrimSpace(s) != "" {
			lines = append(lines, s)
		}
	}
	if len(lines) == 0 {
		t.Fatalf("no content rendered; frame:\n%q", frame)
	}
	// No pinned header: the first content row is the "modified" group header,
	// never the stamp.
	heading := lines[0]
	if !strings.Contains(heading, "modified") {
		t.Errorf("first row must be the group header (no pinned stamp heading); got %q\nframe:\n%s", heading, frame)
	}
	if strings.Contains(heading, "1 file  +1 −0") {
		t.Errorf("the stamp must not be pinned at the top anymore; got %q\nframe:\n%s", heading, frame)
	}
	// The stamp rides the bottom bar, right-aligned; the push count is gone.
	bottom := lines[len(lines)-1]
	if strings.Contains(frame, "↑2") {
		t.Errorf("a claude pane's ledger still shows the push count — the statusline has it; frame:\n%s", frame)
	}
	if !strings.Contains(bottom, "1 file  +1 −0") {
		t.Errorf("bottom bar must carry the right-aligned changed-file stamp; got %q\nframe:\n%s", bottom, frame)
	}
	// The branch must appear NOWHERE — it lives in the Claude statusline.
	for _, ln := range lines {
		if strings.Contains(ln, "main") {
			t.Errorf("no ledger line may name the branch; got %q\nframe:\n%s", ln, frame)
		}
	}
	// The bottom bar must sit BELOW the file list: a.txt appears on an earlier line.
	fileRow := -1
	for i, ln := range lines {
		if strings.Contains(ln, "a.txt") {
			fileRow = i
			break
		}
	}
	if fileRow < 0 || fileRow >= len(lines)-1 {
		t.Errorf("a.txt (the file list) must appear above the bottom bar; lines:\n%s", strings.Join(lines, "\n"))
	}

	// It must sit at the VERY bottom of the pane, not just below the (short) list:
	// the body is padded with blank rows so the bar lands on the last screen row.
	// Keep every row (blanks included) and confirm the stamp bar is the last row,
	// with blank filler on the row directly above it.
	var rows []string
	for _, ln := range strings.Split(frame, "\n") {
		rows = append(rows, strings.TrimRight(ln, " \t\r"))
	}
	// Drop any trailing fully-empty rows the split may leave, then the last row
	// must be the branch bar and the one above it blank (the padding).
	last := len(rows) - 1
	for last > 0 && rows[last] == "" {
		last--
	}
	if !strings.Contains(rows[last], "1 file  +1 −0") {
		t.Errorf("the very last rendered row must be the bottom bar; got %q", rows[last])
	}
	// A 1-file list is far shorter than the 12-row pane, so there is blank filler
	// pushing the bar down: the row directly above it is empty.
	if last < 1 || rows[last-1] != "" {
		t.Errorf("bottom bar must be pushed to the pane bottom with blank filler above it; row above = %q\nrows:\n%s", func() string {
			if last >= 1 {
				return rows[last-1]
			}
			return "<none>"
		}(), strings.Join(rows, "\n"))
	}
	// And the bar sits near the pane's last row (pane is 12 tall): the list is only
	// a few rows, so without bottom-pinning the bar would be near the top.
	if last < 9 {
		t.Errorf("push/pull bar landed on row %d, expected near the pane bottom (~row 12); rows:\n%s", last+1, strings.Join(rows, "\n"))
	}
}

// Regression: on an OVERFLOWING list, hovering a file row reveals the checkbox
// (☐) on that row while the bottom bar KEEPS its scroll position indicator — the
// hover does not disturb the scroll data. Drives the real loop under zsh with
// enough modified files to overflow a short pane, hovers a row, and asserts both
// the ☐ box on the row and the "N-M/T" scroll data show.
func TestCompactView_overflow_hover_keeps_scroll_position(t *testing.T) {
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
	git("checkout", "-q", "-b", "main")
	// Enough files to overflow a 12-row pane (each file is one body row).
	for i := 0; i < 30; i++ {
		name := fmt.Sprintf("file%02d.txt", i)
		writeTempFile(t, dir, name, "base\n")
		git("add", name)
	}
	git("commit", "-q", "-m", "init")
	for i := 0; i < 30; i++ {
		name := fmt.Sprintf("file%02d.txt", i)
		writeTempFile(t, dir, name, "base\nDIRTY\n")
	}

	cmd := exec.Command(zsh, "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if len(e) >= 5 && e[:5] == "TMUX=" {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=5", "TERM=xterm")

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

	time.Sleep(700 * time.Millisecond) // idle first frame (shows bare scroll status)
	mu.Lock()
	out.Reset()
	mu.Unlock()

	// Hover a file row: screen row 3 (no pinned header, row 1 is the "modified"
	// group header, row 2 is the first file — row 3 is a file row).
	_, _ = ptmx.Write([]byte("\x1b[<35;10;3M"))
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	hovered := out.String()
	mu.Unlock()

	_, _ = ptmx.Write([]byte{0x03}) // Ctrl-C
	time.Sleep(200 * time.Millisecond)

	if !strings.Contains(hovered, "☐") {
		t.Errorf("overflow hover should reveal a ☐ checkbox on the hovered row; got:\n%s", hovered)
	}
	// The scroll position indicator ("first-last/total", e.g. "1-9/32") must
	// remain in the bottom bar while a row is hovered.
	scrollRE := regexp.MustCompile(`\d+-\d+/\d+`)
	if !scrollRE.MatchString(hovered) {
		t.Errorf("overflow hover should KEEP the scroll position data in the bottom bar; got:\n%s", hovered)
	}
}

// Regression: the bottom branch bar must never LEAK a raw shell variable dump
// onto the screen (the "blinking" bug). The live pane runs this script under
// zsh, where `local NAME` with NO assignment on an ALREADY-SET variable is a
// *display* command that prints "NAME=value" to stdout. The branch-bar build
// declared `local ab_counts` INSIDE the refresh loop; on the first tick it is a
// harmless declaration, but on every subsequent tick ab_counts is already set,
// so zsh dumped `ab_counts=$'8\t0'` right after the branch bar for one frame —
// a visible blink. The fix hoists the declaration to the pre-loop local block
// (like every other loop-local) so it is never a redisplay. This drives the
// REAL loop under zsh against an upstream-diverged repo across multiple refresh
// ticks and asserts no such variable dump ever reaches the screen.
func TestCompactView_branch_bar_no_local_redisplay_leak(t *testing.T) {
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
	// A bare "remote" so HEAD can diverge from its upstream (ahead > 0), which is
	// what makes the branch-bar build run the `local ab_counts` path.
	remote := t.TempDir()
	rc := exec.Command("git", "init", "-q", "--bare", remote)
	if out, err := rc.CombinedOutput(); err != nil {
		t.Fatalf("git init bare: %v\n%s", err, out)
	}
	git("init", "-q")
	writeTempFile(t, dir, "seed.txt", "x\n")
	git("add", "seed.txt")
	git("commit", "-q", "-m", "init")
	git("remote", "add", "origin", remote)
	git("push", "-q", "-u", "origin", "HEAD")
	// Diverge: 8 commits ahead of the pushed upstream (rev-list emits "8\t0").
	for i := 0; i < 8; i++ {
		writeTempFile(t, dir, "seed.txt", fmt.Sprintf("x%d\n", i))
		git("commit", "-q", "-am", fmt.Sprintf("c%d", i))
	}
	// Some working-tree changes so the ledger has a body to render.
	for i := 0; i < 6; i++ {
		writeTempFile(t, dir, fmt.Sprintf("f%02d.txt", i), "changed\n")
	}

	// Mirror the live pane exactly: zsh -c sourcing the module. A short refresh
	// interval so several build ticks fire within the observation window (the
	// leak only appears on the SECOND and later ticks, once ab_counts is set).
	cmd := exec.Command(zsh, "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if len(e) >= 5 && e[:5] == "TMUX=" {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=0.1", "TERM=xterm")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 20, Cols: 60})
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

	// Observe across many refresh ticks (interval 0.1s) so tick 2+ runs.
	time.Sleep(900 * time.Millisecond)
	_, _ = ptmx.Write([]byte{0x03}) // Ctrl-C
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	got := out.String()
	mu.Unlock()

	// The specific leak from this bug.
	if strings.Contains(got, "ab_counts=") {
		t.Errorf("branch-bar build leaked the raw `ab_counts=` variable dump onto the screen "+
			"(zsh `local NAME` redisplay inside the loop); frame blinks. Hoist the declaration "+
			"out of the loop. Got:\n%s", got)
	}
	// The general signature of ANY zsh loop-local redisplay of a value holding a
	// tab/newline (git numstat, rev-list, etc.): `name=$'...'`. Guards the whole
	// class so a future stray in-loop `local NAME` can't reintroduce the blink.
	redisplayRE := regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*=\$'`)
	if m := redisplayRE.FindString(got); m != "" {
		t.Errorf("a shell variable was redisplayed onto the screen (%q) — an in-loop `local NAME` "+
			"without assignment leaks under zsh. Declare loop-locals ONCE before the loop.", m)
	}
}

// Regression (class-wide "never blink again"): an IDLE ledger — no input, no
// changing git state — must render byte-identical frames tick after tick. A
// "blink" is by definition a frame that momentarily differs from its neighbors,
// so frame stability is the truest encoding of the contract. This catches the
// whole class of transient-corruption bugs, not one variable: the zsh `local
// NAME` redisplay leak (this bug), a stray plain-value dump like "w=141" that a
// `$'...'` regex would miss, or any future cause. The refresh loop writes each
// frame as one printf that begins with cursor-home (ESC[H); splitting the raw
// pty stream on ESC[H yields the per-tick chunks. A redisplay prints its junk to
// stdout just BEFORE the next frame's ESC[H, so it lands as a trailing diff on a
// chunk — making that chunk differ from a clean one. We drive the REAL loop
// under zsh against an upstream-diverged repo (exercises the branch-bar path),
// let it idle across many ticks, and assert the steady-state frames are all equal.
func TestCompactView_idle_frames_are_stable_no_blink(t *testing.T) {
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
	// Bare remote so HEAD can be ahead of its upstream, running the exact build
	// path that leaked ab_counts.
	remote := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("git init bare: %v\n%s", err, out)
	}
	git("init", "-q")
	writeTempFile(t, dir, "seed.txt", "x\n")
	git("add", "seed.txt")
	git("commit", "-q", "-m", "init")
	git("remote", "add", "origin", remote)
	git("push", "-q", "-u", "origin", "HEAD")
	for i := 0; i < 8; i++ {
		writeTempFile(t, dir, "seed.txt", fmt.Sprintf("x%d\n", i))
		git("commit", "-q", "-am", fmt.Sprintf("c%d", i))
	}
	for i := 0; i < 6; i++ {
		writeTempFile(t, dir, fmt.Sprintf("f%02d.txt", i), "changed\n")
	}

	cmd := exec.Command(zsh, "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if len(e) >= 5 && e[:5] == "TMUX=" {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=0.1", "TERM=xterm")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 20, Cols: 60})
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

	// Idle: no keystrokes. Wait for enough refresh ticks to evaluate the
	// between-frame output. Native-binary startup varies under parallel load, so
	// do not spend the observation budget before the first paint exists.
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		homes := bytes.Count(out.Bytes(), []byte("\x1b[H"))
		mu.Unlock()
		if homes >= 5 || time.Now().After(deadline) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	_, _ = ptmx.Write([]byte{0x03}) // Ctrl-C
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	got := out.String()
	mu.Unlock()

	// Split the stream into per-tick chunks on the cursor-home that opens each
	// redraw (printf '\033[H%s\033[K\033[J'). Chunk[i] holds tick i's frame.
	frames := strings.Split(got, "\x1b[H")
	if len(frames) < 6 {
		t.Fatalf("expected many idle redraws (>=6 chunks); got %d.\nraw:\n%q", len(frames), got)
	}
	// The redraw is the ONLY thing that should write to stdout, and it ends every
	// frame with the trailing erase \033[J. So in each steady-state chunk there
	// must be NOTHING after the frame's final \033[J and before the next chunk's
	// cursor-home. A variable redisplay (zsh `local NAME`, a stray echo, any
	// between-frames write — plain-value OR $'...') lands exactly there, so a
	// non-empty tail is the general signature of the blink — for THIS bug and any
	// future one, regardless of whether the leak repeats every tick (which would
	// fool a frame-equality check). Skip warmup (alt-screen enter + first paint)
	// and the final chunk (truncated by Ctrl-C / teardown escapes).
	for i, chunk := range frames[2 : len(frames)-1] {
		j := strings.LastIndex(chunk, "\x1b[J")
		if j < 0 {
			continue // not a full redraw chunk (e.g. a split escape); ignore
		}
		tail := chunk[j+len("\x1b[J"):]
		if tail != "" {
			t.Fatalf("idle ledger BLINKS: %q was written between the end of a frame "+
				"(\\033[J) and the next redraw (\\033[H) on steady-state tick %d. Only the "+
				"home-anchored frame printf may touch stdout; a stray write here is the blink. "+
				"(Commonly a zsh `local NAME`-without-assignment redisplay inside the loop.)",
				tail, i)
		}
	}
}

// Outside tmux the ledger measures the pane with `tput`, which reports terminfo's
// STATIC size (80x24 for xterm) whenever it cannot read the tty's real dimensions
// — which is what happens on CI. The pill is only clickable on the bottom row
// ($h), so a pane it believes is 24 rows tall puts the pill's hit-box on row 24 of
// a 12-row pane: unreachable, and the ledger paints 80 columns wide into 60. Size
// must come from the terminal itself, not from terminfo's guess.
func TestCompactView_hover_pill_when_tput_reports_the_wrong_size(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "tput", `
case "$1" in
  cols)  echo 80 ;;   # terminfo's static xterm size, NOT this 60x12 pty
  lines) echo 24 ;;
  *) exit 0 ;;
esac`)
	hoverPillScenario(t, binDir)
}

// Hovering the account pill (the mid-session "switch account" button) must make it
// highlight so it reads as pressable, and the highlight must clear when the pointer
// leaves the pill. Drives the real loop under zsh with a deterministic 2-account
// relaunch context so the pill renders, then fires a motion report over the pill's
// bottom-bar span and asserts the hover background (48;5;238) appears — and a
// motion just past the pill (still on the bottom bar, so not a file-row hover)
// clears it.
func TestCompactView_hover_highlights_account_pill(t *testing.T) {
	hoverPillScenario(t, "")
}

// hoverPillScenario drives the pill hover against a real 60x12 pty. pathPrefix, when
// set, is prepended to PATH — the tput-lies regression uses it to reproduce CI.
func hoverPillScenario(t *testing.T, pathPrefix string) {
	t.Helper()
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not available")
	}
	root := projectRoot(t)
	lib := filepath.Join(root, "lib")
	module := filepath.Join(lib, "compact-view.sh")

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
	git("checkout", "-q", "-b", "main")
	writeTempFile(t, dir, "a.txt", "base\n")
	git("add", "a.txt")
	git("commit", "-q", "-m", "init")
	writeTempFile(t, dir, "a.txt", "base\nDIRTY\n") // a listed file above the bottom bar

	// A 2-account config (one managed login + the implicit Default) so the pill is
	// eligible to render, plus the relaunch context that points the ledger at it.
	cfg := t.TempDir()
	writeTempFile(t, cfg, "claude-accounts.list", "Work:work\n")
	writeTempFile(t, cfg, "claude-account-colors", "default:78\nwork:170\n")
	relaunch := filepath.Join(cfg, "relaunch.ctx")
	writeTempFile(t, cfg, "relaunch.ctx", fmt.Sprintf(
		"tool=claude\nproject_dir=%s\naccounts_dir=%s\npointer=%s\nlist=%s\ncolors=%s\ndefault_label=%s\n",
		dir, filepath.Join(cfg, "claude-accounts"),
		filepath.Join(cfg, "claude-account"),
		filepath.Join(cfg, "claude-accounts.list"),
		filepath.Join(cfg, "claude-account-colors"),
		filepath.Join(cfg, "claude-account-default-label")))

	cmd := exec.Command(zsh, "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		// Strip TMUX and any inherited relaunch file so the pill context is ours.
		if strings.HasPrefix(e, "TMUX=") || strings.HasPrefix(e, "WISP_DECK_RELAUNCH_FILE=") {
			continue
		}
		if pathPrefix != "" && strings.HasPrefix(e, "PATH=") {
			e = "PATH=" + pathPrefix + ":" + strings.TrimPrefix(e, "PATH=")
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=5", "TERM=xterm",
		"WISP_DECK_LIB_DIR="+lib, "WISP_DECK_RELAUNCH_FILE="+relaunch,
		"WISP_DECK_PLAN=Standard Claude")

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

	read := func() string {
		mu.Lock()
		defer mu.Unlock()
		s := out.String()
		out.Reset()
		return s
	}

	idle, _, ok := waitForFrame(read, "\U000f0004", 6*time.Second)
	if !ok {
		t.Fatalf("account pill (󰀄) never rendered — hover test cannot run; got:\n%q", idle)
	}
	if strings.Contains(idle, "48;5;238") {
		t.Fatalf("the un-hovered pill must not carry the hover background (48;5;238); got:\n%q", idle)
	}

	// Motion over the pill: bottom bar is row 12 (pane height), pill starts at col 1.
	_, _ = ptmx.Write([]byte("\x1b[<35;3;12M"))
	hovered, took, ok := waitForFrame(read, "48;5;238", 6*time.Second)
	if !ok {
		t.Fatalf("hovering the account pill should light it with the hover background "+
			"(48;5;238), but no frame did in %s.\nthe pane is 60x12 and the click went to "+
			"row 12, col 3.\nthe idle frame it was hovering renders as:\n%s\nthe frames the "+
			"hover produced render as:\n%s", took, describeFrame(idle), describeFrame(hovered))
	}
	t.Logf("pill hover highlighted after %s", took)

	// Motion just past the pill, still on the bottom bar (col 40, row 12) — not a
	// file row, so no file-row hover. The pill highlight must clear.
	_, _ = ptmx.Write([]byte("\x1b[<35;40;12M"))
	time.Sleep(300 * time.Millisecond)
	left := read()

	_, _ = ptmx.Write([]byte{0x03}) // Ctrl-C
	time.Sleep(200 * time.Millisecond)

	// The final settled frame in `left` must not carry the pill highlight anymore.
	frames := strings.Split(left, "\x1b[H")
	last := frames[len(frames)-1]
	if strings.Contains(last, "48;5;238") {
		t.Fatalf("moving off the pill should clear its hover highlight, but the settled "+
			"frame still carried 48;5;238; got:\n%q", last)
	}
}

// End-to-end: a modified IMAGE in the ledger shows its byte-size change, not the
// "+0 −0" that numstat gives a binary file. Renders the real loop over a pty with
// a committed 1 KiB pic.png grown to 3 KiB and asserts the "+2.0KB" delta lands on
// the file's row (and that no "+0" line-count row does).
func TestCompactView_image_row_shows_size_delta(t *testing.T) {
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
	git("checkout", "-q", "-b", "main")
	writeTempFile(t, dir, "pic.png", strings.Repeat("a", 1024))
	git("add", "pic.png")
	git("commit", "-q", "-m", "init")
	writeTempFile(t, dir, "pic.png", strings.Repeat("a", 3072)) // grew by 2048 bytes

	cmd := exec.Command("bash", "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") {
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
	read := func() string {
		mu.Lock()
		defer mu.Unlock()
		return out.String()
	}

	acc, took, ok := waitForFrame(read, "2.0KB", 6*time.Second)
	_, _ = ptmx.Write([]byte{0x03}) // Ctrl-C
	time.Sleep(150 * time.Millisecond)
	if !ok {
		t.Fatalf("modified image should render a +2.0KB size delta within %s, but no frame did.\n%s",
			took, describeFrame(acc))
	}
	// The size delta REPLACES the line counts: the image's row must not show +0.
	plain := ansiSeq.ReplaceAllString(acc, "")
	for _, line := range strings.Split(plain, "\n") {
		if strings.Contains(line, "pic.png") && strings.Contains(line, "+0") {
			t.Errorf("image row must not show a +0 line count, got %q", line)
		}
	}
}
