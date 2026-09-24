package bash_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// On tmux 3.6a a bare `-t name` falls back to a PREFIX match once no session
// has that exact name, and `=name` alone still prefix-matches on list-panes.
// Only `=name:` is exact on every verb. tmux also stores "." and ":" in a
// session name as "_", so the target must be written that way too.

func exactPanePid(t *testing.T, env []string, session string) int {
	t.Helper()
	pid, err := strconv.Atoi(isoTmux(t, env, "display-message", "-p", "-t", "="+session+":", "#{pane_pid}"))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func runCleanup(t *testing.T, env []string, session string) {
	t.Helper()
	watcher := exec.Command("sleep", "600")
	if err := watcher.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watcher.Process.Kill(); _, _ = watcher.Process.Wait() })
	root := projectRoot(t)
	script := fmt.Sprintf("source %q && source %q && cleanup_tmux_session %q %d tmux",
		filepath.Join(root, "lib", "process.sh"), filepath.Join(root, "lib", "tmux-session.sh"),
		session, watcher.Process.Pid)
	c := exec.Command("bash", "-c", script)
	c.Env = env
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("cleanup_tmux_session: %v\n%s", err, out)
	}
}

// The wrapper cleans up after its own session is already gone. A sibling tab
// whose name starts with the dead one's (same project, PID with more digits)
// must survive it.
func TestCleanupTmuxSession_spares_a_sibling_whose_name_it_prefixes(t *testing.T) {
	env := isolationServer(t)
	isoTmux(t, env, "new-session", "-d", "-s", "dev-x-10081", "sleep 600")
	sibling := exactPanePid(t, env, "dev-x-10081")
	isoTmux(t, env, "new-session", "-d", "-s", "dev-x-1008", "sleep 600")
	isoTmux(t, env, "kill-session", "-t", "=dev-x-1008:")

	runCleanup(t, env, "dev-x-1008")

	c := exec.Command("tmux", "has-session", "-t", "=dev-x-10081:")
	c.Env = env
	if err := c.Run(); err != nil {
		t.Fatal("cleanup of dev-x-1008 killed the sibling session dev-x-10081")
	}
	if err := syscall.Kill(sibling, 0); err != nil {
		t.Fatalf("cleanup of dev-x-1008 killed the sibling's pane process %d: %v", sibling, err)
	}
}

// A project named foo.io launches as dev-foo.io-<pid>, which tmux stores as
// dev-foo_io-<pid>. The exact target must still reach it.
func TestCleanupTmuxSession_kills_a_dotted_project_session(t *testing.T) {
	env := isolationServer(t)
	isoTmux(t, env, "new-session", "-d", "-s", "dev-foo.io-77", "sleep 600")
	pid := exactPanePid(t, env, "dev-foo_io-77")

	runCleanup(t, env, "dev-foo.io-77")

	c := exec.Command("tmux", "has-session", "-t", "=dev-foo_io-77:")
	c.Env = env
	if err := c.Run(); err == nil {
		t.Fatal("cleanup left the dotted session dev-foo_io-77 alive")
	}
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("cleanup left the dotted session's pane process %d alive", pid)
	}
}

// apply_session_theme is handed the wrapper's unsanitized name.
func TestApplySessionTheme_reaches_a_dotted_project_session(t *testing.T) {
	env := isolationServer(t)
	isoTmux(t, env, "new-session", "-d", "-s", "dev-foo.io-77", "sleep 600")
	isoTmux(t, env, "new-session", "-d", "-s", "zz-current", "sleep 600")
	script := fmt.Sprintf("source %q && apply_session_theme tmux dev-foo.io-77 141",
		filepath.Join(projectRoot(t), "lib", "tab-title-watcher.sh"))
	c := exec.Command("bash", "-c", script)
	c.Env = env
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("apply_session_theme: %v\n%s", err, out)
	}
	got := isoTmux(t, env, "show-options", "-v", "-t", "=dev-foo_io-77:", "pane-active-border-style")
	if got != "fg=colour141" {
		t.Fatalf("dotted session border = %q, want fg=colour141", got)
	}
}

// Every tmux -t in shipped shell names its target exactly. The only other
// targets allowed are pane ids, window ids, the pane tmux handed down, and
// key-binding targets that tmux resolves against the client at key time.
func TestShippedShell_targets_tmux_sessions_exactly(t *testing.T) {
	root := projectRoot(t)
	var files []string
	for _, g := range []string{"lib/*.sh", "templates/*.sh", "bin/*"} {
		m, _ := filepath.Glob(filepath.Join(root, g))
		files = append(files, m...)
	}
	files = append(files, filepath.Join(root, "wrapper.sh"))

	target := regexp.MustCompile(`(?:^|[\s(])-t\s+(\S+)`)
	// Not tmux: these commands take their own -t.
	notTmux := regexp.MustCompile(`\b(type|mktemp|lockf|read|ls)\b[^|;]*\s-t\s|\[ -t `)
	paneVars := `pane|ai_pane|new_pane|ledger_pane|ledger|spare|non_ai|first|current_pane|_gt_ai_pane|pid|wid|window|win|TMUX_PANE`
	allowed := regexp.MustCompile(`^\\?"?(=|\$\{?(` + paneVars + `)\b|\$\{TMUX_PANE:-\}|\$\{range#(wdtab|sel):\}|:|#\{@wisp_ledger_hover_pane\})`)
	// Measured: these verbs still prefix-match, or find nothing, on "=name".
	// A lone "=" is the mouse target inside a key binding.
	colonless := regexp.MustCompile(`\b(list-panes|display-message|set-option|show-options|set-hook)\b[^;|]*\s-t\s+"?=[^:\s"]+"?(\s|$)`)

	checked := 0
	for _, file := range files {
		if strings.HasSuffix(file, ".js") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") || notTmux.MatchString(line) {
				continue
			}
			rel, _ := filepath.Rel(root, file)
			if colonless.MatchString(line) {
				t.Errorf("%s:%d targets a pane verb with \"=name\": it needs \"=name:\"\n%s", rel, i+1, strings.TrimSpace(line))
			}
			for _, m := range target.FindAllStringSubmatch(line, -1) {
				checked++
				if !allowed.MatchString(m[1]) {
					t.Errorf("%s:%d targets tmux with %s: a session name needs \"=name:\" (a bare name prefix-matches another tab)\n%s",
						rel, i+1, m[1], strings.TrimSpace(line))
				}
			}
		}
	}
	if checked < 50 {
		t.Fatalf("only %d -t targets found; the scan is not reading the shell it guards", checked)
	}
}
