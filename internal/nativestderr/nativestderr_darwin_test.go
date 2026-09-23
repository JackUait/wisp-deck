package nativestderr

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

const mslLine = "can't turn off malloc stack logging"

func buildProbe(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "mslprobe")
	build := exec.Command("go", "build", "-o", bin, "./testdata/mslprobe")
	build.Env = append(os.Environ(), "CGO_ENABLED=1")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build probe: %v\n%s", err, out)
	}
	return bin
}

// runOnTerminalStderr runs the probe with fd 2 on a real terminal, the way
// the launch chain's helpers run inside an agent pane.
func runOnTerminalStderr(t *testing.T, bin string, env ...string) string {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("open pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stderr = tty
	if err := cmd.Start(); err != nil {
		t.Fatalf("start probe: %v", err)
	}
	_ = tty.Close()
	done := make(chan []byte)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, ptmx)
		done <- buf.Bytes()
	}()
	_ = cmd.Wait()
	select {
	case out := <-done:
		return string(out)
	case <-time.After(10 * time.Second):
		t.Fatal("probe terminal never closed")
		return ""
	}
}

func TestProbeReproducesTheMallocStackLoggingLine(t *testing.T) {
	out := runOnTerminalStderr(t, buildProbe(t))
	if !strings.Contains(out, mslLine) {
		t.Fatalf("without the shield the probe should print the libmalloc line, got %q", out)
	}
}

func TestShieldKeepsLibmallocOffTheTerminal(t *testing.T) {
	out := runOnTerminalStderr(t, buildProbe(t), "MSLPROBE_SHIELD=1")
	if strings.Contains(out, mslLine) {
		t.Fatalf("libmalloc line reached the terminal: %q", out)
	}
	if !strings.Contains(out, "go-stderr-still-visible") {
		t.Fatalf("Go's own stderr must still reach the terminal, got %q", out)
	}
}

func TestShieldKeepsPanicsOnTheTerminal(t *testing.T) {
	out := runOnTerminalStderr(t, buildProbe(t), "MSLPROBE_SHIELD=1", "MSLPROBE_PANIC=1")
	if !strings.Contains(out, "probe-panic-still-visible") {
		t.Fatalf("a panic must still reach the terminal, got %q", out)
	}
}

func TestShieldLeavesANonTerminalStderrAlone(t *testing.T) {
	cmd := exec.Command(buildProbe(t))
	cmd.Env = append(os.Environ(), "MSLPROBE_SHIELD=1")
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), mslLine) {
		t.Fatalf("a piped stderr is not a pane and should be left as is, got %q", out)
	}
}
