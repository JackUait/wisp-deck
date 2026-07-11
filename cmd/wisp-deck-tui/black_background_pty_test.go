package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

// TestShowLogoPaintsTheScreenBlack drives the REAL binary on a pty: the unit
// tests prove FillBackground pads and colors a string, but only a live render
// proves the escape codes actually reach the screen.
func TestShowLogoPaintsTheScreenBlack(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "wisp-deck-tui")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	// Reproduce production: bash runs the binary via command substitution, so
	// STDOUT IS A PIPE and only /dev/tty has color. A test that hands the child a
	// tty stdout would pass even if the black background were built against the
	// Ascii-profile stdout renderer — the exact way color silently disappears in
	// this binary (see util.TUITeaOptions).
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty open: %v", err)
	}
	defer tty.Close()
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		t.Fatalf("pty setsize: %v", err)
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()

	cmd := exec.Command(bin, "show-logo")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	cmd.Stdin = tty
	cmd.Stdout = devNull // the JSON channel, a pipe in production
	cmd.Stderr = tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = ptmx.Close()
		_, _ = cmd.Process.Wait()
	})

	// The TUI queries the terminal (OSC 11 background, CSI 6n cursor position) and
	// blocks until it gets answers a real terminal would send.
	var mu sync.Mutex
	var acc bytes.Buffer
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				chunk := buf[:n]
				mu.Lock()
				acc.Write(chunk)
				mu.Unlock()
				if bytes.Contains(chunk, []byte("\x1b]11;?")) {
					_, _ = ptmx.Write([]byte("\x1b]11;rgb:1d1d/1f1f/2121\x1b\\"))
				}
				if bytes.Contains(chunk, []byte("\x1b[6n")) {
					_, _ = ptmx.Write([]byte("\x1b[1;1R"))
				}
			}
			if err != nil {
				return
			}
		}
	}()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		frame := acc.String()
		mu.Unlock()
		if strings.Contains(frame, "\x1b[48;2;0;0;0m") {
			return // the logo screen is painted pitch black
		}
		time.Sleep(50 * time.Millisecond)
	}

	mu.Lock()
	frame := acc.String()
	mu.Unlock()
	t.Fatalf("show-logo never painted a pitch-black background (no \\x1b[48;2;0;0;0m in %d bytes of output):\n%q", len(frame), frame)
}
