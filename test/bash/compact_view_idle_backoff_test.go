package bash_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// The backoff decision itself, which is pure and so is checked exactly.
func TestCVIdleBackoffNext(t *testing.T) {
	cases := []struct{ unchanged, current, want string }{
		{"1", "0", "1"}, // first quiet build: skip one timeout  (2x interval)
		{"1", "1", "3"}, // still quiet: skip three              (4x interval)
		{"1", "3", "3"}, // capped, so a dormant pane still looks eventually
		{"0", "3", "0"}, // anything changed: back to every tick
		{"0", "0", "0"},
		{"1", "", "1"},   // never seen a build yet
		{"1", "xx", "1"}, // a corrupt counter must not wedge the loop
	}
	for _, c := range cases {
		out, code := runBashFunc(t, "lib/compact-view.sh", "cv_idle_backoff_next",
			[]string{c.unchanged, c.current}, buildEnv(t, nil))
		assertExitCode(t, code, 0)
		if got := strings.TrimSpace(out); got != c.want {
			t.Errorf("cv_idle_backoff_next %q %q = %q, want %q", c.unchanged, c.current, got, c.want)
		}
	}
}

// countCompactViewPolls drives the REAL loop over a pty for `runFor` and returns
// how many times it reached Git. When `churn` is set, a writer keeps changing
// the repository so the ledger has something new on every build.
func countCompactViewPolls(t *testing.T, runFor time.Duration, churn bool) int {
	t.Helper()
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
	writeTempFile(t, dir, "app.txt", "one\ntwo\n")

	shimDir := t.TempDir()
	buildLog := filepath.Join(shimDir, "builds.log")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("find git: %v", err)
	}
	shim := "#!/bin/bash\n" +
		"if [ \"$3\" = \"diff\" ] && [ \"$4\" = \"--cached\" ]; then echo tick >> " + buildLog + "; fi\n" +
		"exec " + realGit + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shimDir, "git"), []byte(shim), 0o755); err != nil {
		t.Fatalf("write git shim: %v", err)
	}

	cmd := exec.Command("bash", "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") || strings.HasPrefix(e, "PATH=") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env,
		"PATH="+shimDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		// Over a pty compact_view hands off to the native Go renderer when the
		// installed binary advertises it; this exercises the SHELL loop.
		"WISP_DECK_LEDGER_SHELL_FALLBACK=1",
		"COMPACT_VIEW_INTERVAL=1", "TERM=xterm")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 20, Cols: 80})
	if err != nil {
		t.Fatalf("start pty: %v", err)
	}
	go func() {
		b := make([]byte, 4096)
		for {
			if _, err := ptmx.Read(b); err != nil {
				return
			}
		}
	}()

	stop := make(chan struct{})
	var churnWG sync.WaitGroup
	if churn {
		churnWG.Add(1)
		go func() {
			defer churnWG.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				// The LINE COUNT has to move: the ledger's signature is built
				// from numstat, so rewriting a file with the same number of
				// lines looks identical and would leave this arm dormant too.
				body := "one\ntwo\n" + strings.Repeat("change\n", 1+i%40)
				_ = os.WriteFile(filepath.Join(dir, "app.txt"), []byte(body), 0o644)
				time.Sleep(50 * time.Millisecond)
			}
		}()
	}

	time.Sleep(runFor)
	close(stop)
	churnWG.Wait()
	_ = ptmx.Close()
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()

	raw, err := os.ReadFile(buildLog)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read build log: %v", err)
	}
	return bytes.Count(raw, []byte("tick"))
}

// A repository nobody is changing must be polled measurably less often than one
// that is changing constantly.
//
// The comparison is relative on purpose. This machine runs at load ~380 and one
// shell build tick is essentially pure fork cost, so an absolute
// polls-per-second assertion measures the machine, not the code -- the same
// wall-clock flakiness the repo already documents for compact-view tests. Both
// arms run at the same time, under the same load, so only the backoff separates
// them.
func TestCompactView_polls_a_dormant_repository_less_than_a_busy_one(t *testing.T) {
	const runFor = 20 * time.Second

	var quiet, busy int
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); quiet = countCompactViewPolls(t, runFor, false) }()
	go func() { defer wg.Done(); busy = countCompactViewPolls(t, runFor, true) }()
	wg.Wait()

	t.Logf("git polls in %v: dormant=%d busy=%d", runFor, quiet, busy)
	if busy == 0 {
		t.Fatal("the busy arm never reached Git; the harness is not driving the loop")
	}
	if quiet == 0 {
		t.Fatal("the dormant arm never reached Git at all; it must still refresh eventually")
	}
	// Measured on this machine: without the backoff the two arms are level
	// (12 vs 11, 10 vs 10); with it the dormant arm runs at ~0.68 of the busy
	// one. 0.85 separates those cleanly with room for load to move both.
	if quiet*100 > busy*85 {
		t.Errorf("dormant repository polled %d times against %d for a busy one; it is not backing off",
			quiet, busy)
	}
}
