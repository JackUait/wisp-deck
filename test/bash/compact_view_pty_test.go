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
		t.Errorf("after the motion flood the hover highlight (48;5;238) never rendered; "+
			"the coalesced repaint dropped the settled cursor position.")
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

	// Body layout with a short "main" heading (header_rows=2): screen row 3 is the
	// "modified" group header, row 4 = a.txt, row 5 = b.txt, row 6 = c.txt. A
	// no-button SGR motion report (button 35) sets the hover to the row's file.
	hover := func(row int) {
		_, _ = ptmx.Write([]byte(fmt.Sprintf("\x1b[<35;10;%dM", row)))
		time.Sleep(120 * time.Millisecond)
	}

	time.Sleep(700 * time.Millisecond) // first frame

	hover(4)                         // a.txt
	_, _ = ptmx.Write([]byte("x"))   // select a.txt
	time.Sleep(120 * time.Millisecond)
	hover(5)                         // b.txt
	_, _ = ptmx.Write([]byte("x"))   // select b.txt
	time.Sleep(120 * time.Millisecond)
	_, _ = ptmx.Write([]byte("d"))   // arm the confirm
	time.Sleep(150 * time.Millisecond)
	_, _ = ptmx.Write([]byte("y"))   // confirm the batch discard
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

// Discoverability: the select/discard keys must be advertised in-UI, not just in
// docs. The hint appears the moment the cursor is over a file row and is gone
// when nothing is hovered (so the idle view stays full-height). Drives the real
// loop under zsh, checks the idle frame has no hint, then hovers a file row and
// asserts the hint ("d discard") shows up.
func TestCompactView_shows_hover_hint(t *testing.T) {
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
	if strings.Contains(idle, "d discard") {
		t.Errorf("idle frame (no hover) should not show the hint; got:\n%s", idle)
	}

	// Hover the single file row: screen row 4 (row 3 is the "modified" header).
	_, _ = ptmx.Write([]byte("\x1b[<35;10;4M"))
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	hovered := out.String()
	mu.Unlock()

	_, _ = ptmx.Write([]byte{0x03}) // Ctrl-C
	time.Sleep(200 * time.Millisecond)

	if !strings.Contains(hovered, "d discard") {
		t.Errorf("hovering a file row should reveal the 'd discard' hint; got:\n%s", hovered)
	}
}
