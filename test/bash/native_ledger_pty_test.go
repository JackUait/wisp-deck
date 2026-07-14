package bash_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

var (
	nativeLedgerBuildOnce sync.Once
	nativeLedgerBuildPath string
	nativeLedgerBuildErr  error
)

type nativeLedgerSnapshot struct {
	Generation uint64                    `json:"generation"`
	Rows       []nativeLedgerSnapshotRow `json:"rows"`
	Metadata   nativeLedgerMetadata      `json:"metadata"`
}

type nativeLedgerSnapshotRow struct {
	Kind    int               `json:"kind"`
	ID      nativeLedgerRowID `json:"id"`
	Path    string            `json:"path,omitempty"`
	Label   string            `json:"label,omitempty"`
	Count   int               `json:"count,omitempty"`
	Added   int               `json:"added,omitempty"`
	Deleted int               `json:"deleted,omitempty"`
}

type nativeLedgerRowID struct {
	Group int    `json:"group"`
	Path  string `json:"path,omitempty"`
}

type nativeLedgerMetadata struct {
	Branch     string `json:"branch"`
	Ahead      int    `json:"ahead"`
	Behind     int    `json:"behind"`
	Plan       string `json:"plan"`
	TotalFiles int    `json:"total_files"`
	Added      int    `json:"added"`
	Deleted    int    `json:"deleted"`
}

type nativeLedgerCapture struct {
	mu  sync.Mutex
	out bytes.Buffer
}

func (c *nativeLedgerCapture) write(data []byte) {
	c.mu.Lock()
	_, _ = c.out.Write(data)
	c.mu.Unlock()
}

func (c *nativeLedgerCapture) snapshot() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.out.String()
}

func (c *nativeLedgerCapture) length() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.out.Len()
}

func (c *nativeLedgerCapture) after(offset int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	raw := c.out.String()
	if offset > len(raw) {
		return ""
	}
	return raw[offset:]
}

type nativeLedgerPTY struct {
	cmd     *exec.Cmd
	ptmx    *os.File
	capture *nativeLedgerCapture
	exited  chan error
}

func nativeLedgerBinary(t *testing.T) string {
	t.Helper()
	root := projectRoot(t)
	nativeLedgerBuildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "wisp-native-ledger-bin-")
		if err != nil {
			nativeLedgerBuildErr = err
			return
		}
		nativeLedgerBuildPath = filepath.Join(dir, "wisp-deck-tui")
		cmd := exec.Command("go", "build", "-o", nativeLedgerBuildPath, "./cmd/wisp-deck-tui")
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			nativeLedgerBuildErr = fmt.Errorf("go build: %w\n%s", err, output)
		}
	})
	if nativeLedgerBuildErr != nil {
		t.Fatal(nativeLedgerBuildErr)
	}
	return nativeLedgerBuildPath
}

func writeNativeLedgerSnapshot(t *testing.T, total int, branch string) string {
	t.Helper()
	rows := make([]nativeLedgerSnapshotRow, 0, total+1)
	rows = append(rows, nativeLedgerSnapshotRow{Kind: 0, Label: "modified", Count: total})
	for i := 0; i < total; i++ {
		path := fmt.Sprintf("src/file_%05d.go", i)
		rows = append(rows, nativeLedgerSnapshotRow{
			Kind: 1, ID: nativeLedgerRowID{Group: 2, Path: path}, Path: path,
			Added: i%17 + 1, Deleted: i % 7,
		})
	}
	document := nativeLedgerSnapshot{
		Generation: 1,
		Rows:       rows,
		Metadata: nativeLedgerMetadata{
			Branch: branch, Ahead: 2, Behind: 1, Plan: "native parity",
			TotalFiles: total, Added: total * 2, Deleted: total,
		},
	}
	path := filepath.Join(t.TempDir(), fmt.Sprintf("ledger-%d.json", total))
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(file).Encode(document); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func initNativeLedgerRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", dir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	return dir
}

func startNativeLedgerPTY(t *testing.T, project, snapshot string, size *pty.Winsize, extraEnv []string, extraArgs ...string) *nativeLedgerPTY {
	t.Helper()
	args := []string{"ledger", project, "--snapshot-file", snapshot, "--refresh-interval", "1h"}
	args = append(args, extraArgs...)
	cmd := exec.Command(nativeLedgerBinary(t), args...)
	configDir := t.TempDir()
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"XDG_CONFIG_HOME="+configDir,
		"TMUX_PANE=%1",
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	ptmx, err := pty.StartWithSize(cmd, size)
	if err != nil {
		t.Fatalf("start native ledger pty: %v", err)
	}
	capture := &nativeLedgerCapture{}
	session := &nativeLedgerPTY{cmd: cmd, ptmx: ptmx, capture: capture, exited: make(chan error, 1)}
	go func() {
		buffer := make([]byte, 32*1024)
		var terminalQueries strings.Builder
		backgroundAnswered := false
		positionAnswered := false
		for {
			n, err := ptmx.Read(buffer)
			if n > 0 {
				chunk := buffer[:n]
				capture.write(chunk)
				terminalQueries.Write(chunk)
				queries := terminalQueries.String()
				if !backgroundAnswered && strings.Contains(queries, "\x1b]11;?\x1b\\") {
					backgroundAnswered = true
					_, _ = ptmx.Write([]byte("\x1b]11;rgb:0000/0000/0000\x1b\\"))
				}
				if !positionAnswered && strings.Contains(queries, "\x1b[6n") {
					positionAnswered = true
					_, _ = ptmx.Write([]byte("\x1b[1;1R"))
				}
			}
			if err != nil {
				return
			}
		}
	}()
	go func() { session.exited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = ptmx.Close()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			_ = cmd.Process.Kill()
		}
	})
	return session
}

func (s *nativeLedgerPTY) waitAfter(offset int, want string, timeout time.Duration) (time.Duration, string, bool) {
	start := time.Now()
	for time.Since(start) < timeout {
		raw := s.capture.after(offset)
		plain := ansiSeq.ReplaceAllString(raw, "")
		if strings.Contains(raw, want) || strings.Contains(plain, want) {
			return time.Since(start), raw, true
		}
		time.Sleep(time.Millisecond)
	}
	raw := s.capture.after(offset)
	return time.Since(start), raw, false
}

func (s *nativeLedgerPTY) waitFor(want string, timeout time.Duration) (time.Duration, string, bool) {
	return s.waitAfter(0, want, timeout)
}

func (s *nativeLedgerPTY) waitAfterAll(offset int, timeout time.Duration, wants ...string) (time.Duration, string, bool) {
	start := time.Now()
	for time.Since(start) < timeout {
		raw := s.capture.after(offset)
		plain := ansiSeq.ReplaceAllString(raw, "")
		matched := true
		for _, want := range wants {
			if !strings.Contains(raw, want) && !strings.Contains(plain, want) {
				matched = false
				break
			}
		}
		if matched {
			return time.Since(start), raw, true
		}
		time.Sleep(time.Millisecond)
	}
	return time.Since(start), s.capture.after(offset), false
}

func (s *nativeLedgerPTY) write(t *testing.T, input string) int {
	t.Helper()
	offset := s.capture.length()
	if _, err := s.ptmx.Write([]byte(input)); err != nil {
		t.Fatalf("write pty input: %v", err)
	}
	return offset
}

func (s *nativeLedgerPTY) stop(t *testing.T) time.Duration {
	t.Helper()
	start := time.Now()
	if _, err := s.ptmx.Write([]byte{0x03}); err != nil {
		t.Fatalf("write Ctrl-C: %v", err)
	}
	select {
	case <-s.exited:
		return time.Since(start)
	case <-time.After(2 * time.Second):
		_ = s.cmd.Process.Kill()
		t.Fatalf("native ledger did not exit within 2s of Ctrl-C")
		return 0
	}
}

func sgrMotion(x, y int) string { return fmt.Sprintf("\x1b[<35;%d;%dM", x, y) }
func sgrClick(x, y int) string  { return fmt.Sprintf("\x1b[<0;%d;%dM\x1b[<0;%d;%dm", x, y, x, y) }
func sgrPress(x, y int) string  { return fmt.Sprintf("\x1b[<0;%d;%dM", x, y) }

func installNativeLedgerTmux(t *testing.T) (binDir, logPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "tmux.log")
	body := `
printf '%s\n' "$*" >> "$WISP_NATIVE_TMUX_LOG"
case "$1" in
  display-message) printf '80 16\n' ;;
  list-panes) printf '%%1 0 0\n' ;;
  capture-pane) printf 'cached backdrop\n' ;;
esac
`
	binDir = mockCommand(t, dir, "tmux", body)
	return binDir, logPath
}

func TestNativeLedgerPTYInteractionParity10k(t *testing.T) {
	project := initNativeLedgerRepo(t)
	snapshot := writeNativeLedgerSnapshot(t, 10_000, "main")
	binDir, tmuxLog := installNativeLedgerTmux(t)
	env := []string{"PATH=" + binDir + ":" + os.Getenv("PATH"), "WISP_NATIVE_TMUX_LOG=" + tmuxLog}
	session := startNativeLedgerPTY(t, project, snapshot, &pty.Winsize{Rows: 16, Cols: 80}, env)

	if _, raw, ok := session.waitFor("file_00000.go", 2*time.Second); !ok {
		t.Fatalf("first native frame did not appear:\n%s", describeFrame(raw))
	}

	offset := session.write(t, sgrMotion(20, 4))
	if _, raw, ok := session.waitAfter(offset, "☐", 250*time.Millisecond); !ok {
		t.Fatalf("hover frame did not appear:\n%s", describeFrame(raw))
	}
	time.Sleep(30 * time.Millisecond)
	offset = session.capture.length()
	for i := 0; i < 50; i++ {
		if _, err := session.ptmx.Write([]byte(sgrMotion(20+i%4, 4))); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(60 * time.Millisecond)
	if delta := session.capture.after(offset); delta != "" {
		t.Fatalf("same-row motion emitted an extra terminal frame (%d bytes): %q", len(delta), delta)
	}

	offset = session.write(t, "\x1b[<65;20;4M")
	if _, raw, ok := session.waitAfter(offset, "☐", 250*time.Millisecond); !ok {
		t.Fatalf("wheel scroll blinked the hover highlight: %q", raw)
	} else if strings.Contains(raw, "\x1b[2J") {
		t.Fatalf("wheel scroll blanked the screen: %q", raw)
	}
	offset = session.write(t, "j")
	if _, raw, ok := session.waitAfter(offset, "☐", 250*time.Millisecond); !ok {
		t.Fatalf("keyboard scroll blinked the hover highlight: %q", raw)
	} else if strings.Contains(raw, "\x1b[2J") {
		t.Fatalf("keyboard scroll blanked the screen: %q", raw)
	}

	_ = session.write(t, "G")
	if _, raw, ok := session.waitFor("file_09999.go", 250*time.Millisecond); !ok {
		t.Fatalf("bottom viewport did not render: %q", raw)
	}
	offset = session.write(t, sgrMotion(20, 15))
	if _, raw, ok := session.waitAfterAll(offset, 250*time.Millisecond, "☐", "file_09999.go"); !ok {
		t.Fatalf("bottom-row hover did not render: %q", raw)
	}
	openedAt := time.Now()
	_ = session.write(t, sgrPress(20, 15))
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		raw, _ := os.ReadFile(tmuxLog)
		if strings.Contains(string(raw), "WISP_LEDGER_PATH=src/file_09999.go") {
			break
		}
		time.Sleep(time.Millisecond)
	}
	logRaw, _ := os.ReadFile(tmuxLog)
	if !strings.Contains(string(logRaw), "WISP_LEDGER_PATH=src/file_09999.go") {
		t.Fatalf("near-bottom click opened the wrong path or stalled:\n%s\nterminal:\n%s",
			logRaw, describeFrame(session.capture.snapshot()))
	}
	if elapsed := time.Since(openedAt); elapsed > 500*time.Millisecond {
		t.Fatalf("near-bottom direct open took %v, want <= 500ms including process startup", elapsed)
	}

	popupCount := strings.Count(string(logRaw), "display-popup")
	_ = session.write(t, sgrMotion(100, 15)+sgrClick(100, 15))
	time.Sleep(60 * time.Millisecond)
	logRaw, _ = os.ReadFile(tmuxLog)
	if got := strings.Count(string(logRaw), "display-popup"); got != popupCount {
		t.Fatalf("mouse outside pane retained a stale hovered row: popup count %d -> %d", popupCount, got)
	}

	offset = session.capture.length()
	if err := pty.Setsize(session.ptmx, &pty.Winsize{Rows: 18, Cols: 72}); err != nil {
		t.Fatalf("resize pty: %v", err)
	}
	if _, raw, ok := session.waitAfter(offset, "09999", 250*time.Millisecond); !ok {
		t.Fatalf("resize did not produce a bounded ledger frame: %q", raw)
	}
	if elapsed := session.stop(t); elapsed > 500*time.Millisecond {
		t.Fatalf("Ctrl-C cleanup took %v", elapsed)
	}
}

func TestNativeLedgerPTYSelectionAndDiscard(t *testing.T) {
	project := initNativeLedgerRepo(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", project}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	writeTempFile(t, project, "src/file_00000.go", "original\n")
	git("add", "src/file_00000.go")
	git("commit", "-q", "-m", "seed")
	writeTempFile(t, project, "src/file_00000.go", "changed\n")
	snapshot := writeNativeLedgerSnapshot(t, 1, "main")
	session := startNativeLedgerPTY(t, project, snapshot, &pty.Winsize{Rows: 10, Cols: 70}, nil)

	if _, raw, ok := session.waitFor("file_00000.go", 2*time.Second); !ok {
		t.Fatalf("first frame missing: %q", raw)
	}
	_ = session.write(t, sgrMotion(20, 4))
	offset := session.write(t, sgrClick(2, 4))
	if _, raw, ok := session.waitAfter(offset, "☑", 250*time.Millisecond); !ok {
		t.Fatalf("checkbox selection did not render: %q", raw)
	}
	offset = session.write(t, "d")
	if _, raw, ok := session.waitAfter(offset, "Discard 1 file?", 250*time.Millisecond); !ok {
		t.Fatalf("discard confirmation did not render: %q", raw)
	}
	_ = session.write(t, "y")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		raw, _ := os.ReadFile(filepath.Join(project, "src/file_00000.go"))
		if string(raw) == "original\n" {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	raw, err := os.ReadFile(filepath.Join(project, "src/file_00000.go"))
	if err != nil || string(raw) != "original\n" {
		t.Fatalf("discard did not restore tracked file: %q, %v", raw, err)
	}
	session.stop(t)
}

func TestNativeLedgerPTYWrappedBarAndAccountPill(t *testing.T) {
	project := initNativeLedgerRepo(t)
	snapshot := writeNativeLedgerSnapshot(t, 12, strings.Repeat("long-branch-", 12))
	relaunch := filepath.Join(t.TempDir(), "relaunch.env")
	if err := os.WriteFile(relaunch, []byte("tool=codex\ntools=claude codex\nproject_dir="+project+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session := startNativeLedgerPTY(t, project, snapshot, &pty.Winsize{Rows: 9, Cols: 38}, nil,
		"--relaunch-file", relaunch)
	if _, raw, ok := session.waitFor("Codex", 2*time.Second); !ok {
		t.Fatalf("account pill did not appear in wrapped/clipped bottom bar:\n%s", describeFrame(raw))
	}
	frame := describeFrame(session.capture.snapshot())
	if !strings.Contains(frame, "Codex") {
		t.Fatalf("account pill is not on the final terminal frame:\n%s", frame)
	}
	session.stop(t)
}
