package bash_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Bash 5.3 feeds a here-document (and a here-string, which is one) through a
// pipe instead of a temp file, and writes all of it before the reader starts.
// A pipe holds only what the kernel granted it — 512 bytes once a machine's
// pipe budget is spent, which a deck of long-lived sessions manages to do — so
// anything past that blocks forever. A deadlocked helper is invisible from the
// outside: the ledger's account pill stays mid-switch and never opens the
// switcher again.
//
// Above ~64KB bash falls back to a temp file, so the dangerous range is bounded
// on both ends; the margin below keeps a body clear of the low end.
const heredocPipeSafeBytes = 400

// runBashSnippetWithin is runBashSnippet with a deadline: a deadlock must fail
// the test, not hang the whole package until Go's timeout.
func runBashSnippetWithin(t *testing.T, timeout time.Duration, script string, env []string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("snippet deadlocked after %s; output so far:\n%s", timeout, out)
	}
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatalf("failed to run snippet: %v", err)
		}
	}
	return string(out)
}

// The session environment a pane carries runs past the pipe's capacity on PATH
// alone, and reading it is the first thing every switcher click does.
func TestCurrentSessionIdentities_survives_an_environment_past_the_pipe_buffer(t *testing.T) {
	dir := t.TempDir()
	padding := strings.Repeat("x", 8*heredocPipeSafeBytes)
	tmuxBin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "show-environment" ]; then
  printf 'PATH=%s\n'
  printf 'WISP_DECK_CLAUDE_ACCOUNT=work\n'
  printf 'WISP_DECK_CLAUDE_CONFIG=qwen.json\n'
  exit 0
fi
exit 0`, padding))

	out := runBashSnippetWithin(t, 20*time.Second, accountSwitchSnippet(t, `
session_acct=""; session_config=""
_current_session_identities tmux `+
		filepath.Join(dir, "claude-account")+" "+filepath.Join(dir, "claude-config")+`
printf 'acct=%s config=%s\n' "$session_acct" "$session_config"`),
		buildEnv(t, []string{tmuxBin}))

	if !strings.Contains(out, "acct=work config=qwen.json") {
		t.Fatalf("identities not resolved from a large session environment:\n%s", out)
	}
}

var heredocOpener = regexp.MustCompile(`<<-?\s*'?"?([A-Za-z_][A-Za-z0-9_]*)'?"?\s*$`)

// A here-document body is fixed text, so its size is decidable here — unlike a
// here-string's, which is whatever the variable holds at runtime. Feed an
// oversized one to its reader some other way: `printf '%s\n' 'line' 'line'` for
// text, `python3 -c "$script"` for a script. Process substitution is NOT the
// answer for every file — several of these parse under the `/bin/sh` launch
// shell, where `< <(...)` is a syntax error.
func TestShippedShellCode_hasNoHereDocumentThePipeCannotHold(t *testing.T) {
	root := repoRoot(t)
	var offenders []string
	report := func(path string) {
		data, err := os.ReadFile(path)
		if err != nil || !isShellSource(path, data) {
			return
		}
		lines := strings.Split(string(data), "\n")
		for i := 0; i < len(lines); i++ {
			match := heredocOpener.FindStringSubmatch(lines[i])
			if match == nil {
				continue
			}
			size, end := 0, i+1
			for ; end < len(lines) && strings.TrimSpace(lines[end]) != match[1]; end++ {
				size += len(lines[end]) + 1
			}
			if size > heredocPipeSafeBytes {
				rel, _ := filepath.Rel(root, path)
				offenders = append(offenders,
					fmt.Sprintf("%s:%d: <<%s carries %d bytes", rel, i+1, match[1], size))
			}
			i = end
		}
	}
	for _, target := range []string{"lib", "templates", "terminals", "scripts", "ghostty", "bin"} {
		err := filepath.Walk(filepath.Join(root, target), func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				report(path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", target, err)
		}
	}
	report(filepath.Join(root, "wrapper.sh")) // ships from the repo root, not lib/
	if len(offenders) > 0 {
		t.Fatalf("here-documents past what a pipe holds deadlock under bash 5.3:\n%s",
			strings.Join(offenders, "\n"))
	}
}

// bin/ also holds the built Go binary, whose bytes would match anything.
func isShellSource(path string, data []byte) bool {
	if strings.HasSuffix(path, ".sh") {
		return true
	}
	if filepath.Ext(path) != "" {
		return false
	}
	return bytes.HasPrefix(data, []byte("#!"))
}

// tmux runs a `run-shell` command under /bin/sh, which is bash in POSIX mode on
// macOS — no process substitution. These files parse there and must keep doing
// so: the heredoc-deadlock fix reaches for `< <(printf …)` freely, and one of
// those in a file on this list turns a click into a parse error. The list is
// explicit rather than derived from git, so it still guards a checked-out commit.
var shellFilesParsedByBinSh = []string{
	"lib/ai-loading.sh",
	"lib/ai-select-tui.sh",
	"lib/ai-tools.sh",
	"lib/attention.sh",
	"lib/auto-switch.sh",
	"lib/claude-accounts.sh",
	"lib/claude-configs.sh",
	"lib/config-tui.sh",
	"lib/ghostty-config.sh",
	"lib/input.sh",
	"lib/install.sh",
	"lib/keep-awake.sh",
	"lib/loading.sh",
	"lib/menu-tui.sh",
	"lib/notification-setup.sh",
	"lib/process.sh",
	"lib/project-actions.sh",
	"lib/projects.sh",
	"lib/screenshot.sh",
	"lib/settings-json.sh",
	"lib/setup.sh",
	"lib/statusline-setup.sh",
	"lib/statusline.sh",
	"lib/subagent-statusline.sh",
	"lib/tab-title-watcher.sh",
	"lib/theme.sh",
	"lib/tmux-session.sh",
	"lib/tui.sh",
}

func TestShellCodeThatParsesUnderBinSh_keepsParsingThere(t *testing.T) {
	root := repoRoot(t)
	for _, name := range shellFilesParsedByBinSh {
		if err := exec.Command("/bin/sh", "-n", filepath.Join(root, name)).Run(); err != nil {
			t.Errorf("%s no longer parses under /bin/sh: %v", name, err)
		}
	}
}
