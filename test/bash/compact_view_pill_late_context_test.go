package bash_test

import (
	"bytes"
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

// The shell renderer is the fallback the pane falls back to for older binaries,
// WISP_DECK_LEDGER_SHELL_FALLBACK=1 recovery, and non-tty fixtures — and it
// races the relaunch context exactly like the native ledger does: wrapper.sh
// writes that file in the launch tail, after the tmux batch that created this
// pane. Reading the context once, before the render loop, left the pill's
// account paths empty for the pane's whole life whenever the pane won that
// race. The context must be re-read until it resolves.
func TestCompactView_pill_appears_when_relaunch_context_is_written_late(t *testing.T) {
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
	writeTempFile(t, dir, "a.txt", "base\nDIRTY\n")

	cfg := t.TempDir()
	writeTempFile(t, cfg, "claude-accounts.list", "Work:work\n")
	writeTempFile(t, cfg, "claude-account-colors", "default:78\nwork:170\n")
	// Named but NOT created: the pane starts before the launch tail publishes it.
	relaunch := filepath.Join(cfg, "relaunch.ctx")

	cmd := exec.Command(zsh, "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") || strings.HasPrefix(e, "WISP_DECK_RELAUNCH_FILE=") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=1", "TERM=xterm",
		"WISP_DECK_LIB_DIR="+lib, "WISP_DECK_RELAUNCH_FILE="+relaunch,
		"WISP_DECK_PLAN=Standard Claude")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 12, Cols: 60})
	if err != nil {
		t.Fatalf("start pty: %v", err)
	}
	t.Cleanup(func() {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})

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

	// The pane paints without a pill first — there is no context to resolve yet.
	if frame, _, ok := waitForFrame(read, "a.txt", 10*time.Second); !ok {
		t.Fatalf("ledger never painted:\n%q", frame)
	}

	// The launch tail publishes the context.
	writeTempFile(t, cfg, "relaunch.ctx", fmt.Sprintf(
		"tool=claude\nproject_dir=%s\naccounts_dir=%s\npointer=%s\nlist=%s\ncolors=%s\ndefault_label=%s\n",
		dir, filepath.Join(cfg, "claude-accounts"),
		filepath.Join(cfg, "claude-account"),
		filepath.Join(cfg, "claude-accounts.list"),
		filepath.Join(cfg, "claude-account-colors"),
		filepath.Join(cfg, "claude-account-default-label")))

	if frame, took, ok := waitForFrame(read, "\U000f0004", 15*time.Second); !ok {
		t.Fatalf("the account pill never appeared after the relaunch context was "+
			"published (%s) — the shell renderer read the context once, before its "+
			"loop, so a pane that started first stays pill-less forever.\nlast frames:\n%s",
			took, describeFrame(frame))
	}
}
