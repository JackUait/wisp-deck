package bash_test

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// End-to-end regression guard for the "draft lost on account switch" bug,
// driven through a REAL tmux server (private socket): the AI pane holds an
// unsent draft with a pasted image when the user switches logins. The old
// respawn-pane -k flow killed the claude process and the draft died with it.
// After the fix, the switch must stash the draft (Esc Esc → history), respawn
// under the new login, and replay the draft — image marker re-pasted as the
// cached PNG's absolute path (which real claude re-attaches as a live chip).
//
// A fake claude emulates the three real-claude behaviors the feature rests on
// (verified live on 2.1.201, see the 2026-07-06 spec): (1) Esc Esc appends the
// draft to the prompt history; (2) the ready frame's empty prompt is "❯" + a
// NO-BREAK space; (3) pasted input reaches the process (logged verbatim here).
// Everything else is real: send-keys, respawn-pane, capture-pane polling,
// load-buffer/paste-buffer -p, the disowned waiter, and the lib under test.
// TestLiveClaude_draft_assumptions (gated) covers the emulated behaviors
// against the real claude binary.
func TestDraftRestore_endToEnd_realTmux_draft_survives_switch(t *testing.T) {
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}
	root := projectRoot(t)
	lib := filepath.Join(root, "lib")
	dir := t.TempDir()
	sock := fmt.Sprintf("wddraft-e2e-%d", os.Getpid())
	tmux := func(args ...string) ([]byte, error) {
		return exec.Command(tmuxBin, append([]string{"-L", sock}, args...)...).CombinedOutput()
	}
	t.Cleanup(func() { _, _ = tmux("kill-server") })

	// One managed login; the pane runs it, the switcher will pick Default.
	cfg := filepath.Join(dir, "cfg")
	writeTempFile(t, cfg, "claude-accounts.list", "Personal:personal\n")
	pointer := filepath.Join(cfg, "claude-account")

	// The draft's pasted image, cached under the OLD login's config root at
	// paste time — replay must paste this exact absolute path.
	sid := "e2e-sid-1111"
	imgPath := filepath.Join(cfg, "claude-accounts", "personal", "image-cache", sid, "1.png")
	writeTempFile(t, filepath.Dir(imgPath), "1.png", "png-bytes")

	hist := filepath.Join(dir, "history.jsonl")
	pasteLog := filepath.Join(dir, "paste.log")
	binDir := filepath.Join(dir, "bin")

	// Fake claude. --stash-emu (the pre-switch pane): raw tty, count ESC
	// bytes, append the draft entry on the Esc PAIR — what real claude does
	// with a non-empty input. Any other argv (the respawned pane, launched by
	// the real resume chain): print the NBSP ready frame, then append every
	// byte of input (the bracketed pastes) to the paste log.
	writeTempFile(t, binDir, "claude", fmt.Sprintf(`#!/bin/bash
if [ "$1" = "--stash-emu" ]; then
  stty raw -echo 2>/dev/null
  esc=0
  while IFS= read -r -n1 c; do
    if [ "$c" = "$(printf '\033')" ]; then
      esc=$((esc+1))
      if [ "$esc" -eq 2 ]; then
        printf '%%s\n' '{"display":"our draft [Image #1] tail","pastedContents":{},"project":%q}' >> %q
      fi
    fi
  done
  exit 0
fi
printf '\342\235\257\302\240\n'
stty raw -echo 2>/dev/null
exec cat >> %q
`, dir, hist, pasteLog))
	// Fake popup: advertises the session flags, then "picks Default" (writes
	// an empty dir to the result file and clears the pointer).
	writeTempFile(t, binDir, "wisp-deck-tui", `#!/bin/bash
if [ "$2" = "--help" ] || [ "$1" = "--help" ]; then
  printf -- 'Flags:\n      --active string\n      --result-file string\n'
  exit 0
fi
rf=""; ptr=""
while [ $# -gt 0 ]; do
  case "$1" in
    --result-file) rf="$2"; shift 2 ;;
    --pointer) ptr="$2"; shift 2 ;;
    *) shift ;;
  esac
done
[ -n "$rf" ] && printf '\n' > "$rf"
[ -n "$ptr" ] && rm -f "$ptr"
exit 0
`)
	// tmux shim so the lib's literal `tmux` hits the private socket.
	writeTempFile(t, binDir, "tmux", fmt.Sprintf("#!/bin/bash\nexec %q -L %q \"$@\"\n", tmuxBin, sock))
	for _, f := range []string{"claude", "wisp-deck-tui", "tmux"} {
		if err := os.Chmod(filepath.Join(binDir, f), 0755); err != nil {
			t.Fatal(err)
		}
	}

	relaunch := writeTempFile(t, dir, "relaunch", strings.Join([]string{
		"tool=claude", "tool_cmd=" + filepath.Join(binDir, "claude"),
		"settings=", "filter=",
		"project_dir=" + dir,
		"accounts_dir=" + filepath.Join(cfg, "claude-accounts"),
		"pointer=" + pointer,
		"list=" + filepath.Join(cfg, "claude-accounts.list"),
		"colors=" + filepath.Join(cfg, "claude-account-colors"),
		"default_label=" + filepath.Join(cfg, "claude-account-default-label"),
		"",
	}, "\n"))

	// Ledger pane 0 + AI pane 1 (stash-emu claude) running "personal", with
	// the session stamped the way wrapper.sh and the statusline would.
	if out, err := tmux("new-session", "-d", "-s", "e2e", "-x", "200", "-y", "50"); err != nil {
		t.Fatalf("new-session: %v: %s", err, out)
	}
	if out, err := tmux("split-window", "-t", "e2e",
		filepath.Join(binDir, "claude")+" --stash-emu; exec bash"); err != nil {
		t.Fatalf("split-window: %v: %s", err, out)
	}
	if out, err := tmux("set-option", "-p", "-t", "e2e:0.1", "@gt_ai", "1"); err != nil {
		t.Fatalf("set-option: %v: %s", err, out)
	}
	for k, v := range map[string]string{
		"WISP_DECK_CLAUDE_ACCOUNT": "personal",
		"WISP_DECK_CLAUDE_SESSION": sid,
	} {
		if out, err := tmux("set-environment", "-t", "e2e", k, v); err != nil {
			t.Fatalf("set-environment %s: %v: %s", k, err, out)
		}
	}

	// display-popup needs an attached client; a pty client provides one.
	attachPtyClient(t, tmuxBin, sock, "e2e")

	if out, err := tmux("set-environment", "-g", "PATH", binDir+":"+os.Getenv("PATH")); err != nil {
		t.Fatalf("set-environment -g PATH: %v: %s", err, out)
	}

	// From the ledger pane: source the real libs and run the real click flow.
	script := fmt.Sprintf(
		"export PATH=%q:$PATH; export WISP_DECK_HISTORY_FILE=%q; source %q; source %q; source %q; source %q; source %q; open_account_switcher tmux %q; echo E2E-RC=$?",
		binDir, hist,
		filepath.Join(lib, "statusline.sh"), filepath.Join(lib, "claude-accounts.sh"),
		filepath.Join(lib, "tmux-session.sh"), filepath.Join(lib, "claude-shared-settings.sh"),
		filepath.Join(lib, "account-switch.sh"), relaunch)
	if out, err := tmux("send-keys", "-t", "e2e:0.0", script, "Enter"); err != nil {
		t.Fatalf("send-keys: %v: %s", err, out)
	}

	// The regression: the replayed draft must land in the RELAUNCHED pane —
	// text segments verbatim and the image marker as the cached PNG's path.
	deadline := time.Now().Add(30 * time.Second)
	for {
		data, _ := os.ReadFile(pasteLog)
		if strings.Contains(string(data), "our draft ") &&
			strings.Contains(string(data), imgPath) &&
			strings.Contains(string(data), " tail") {
			break
		}
		if time.Now().After(deadline) {
			pane0, _ := tmux("capture-pane", "-p", "-t", "e2e:0.0")
			pane1, _ := tmux("capture-pane", "-p", "-t", "e2e:0.1")
			histData, _ := os.ReadFile(hist)
			t.Fatalf("draft was not replayed after the switch (the draft-loss bug):\npaste.log=%q\nhistory=%q\nledger pane:\n%s\nAI pane:\n%s",
				data, histData, pane0, pane1)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// attachPtyClient attaches a throwaway pty client so display-popup has a
// client to draw on; reads and discards its output.
func attachPtyClient(t *testing.T, tmuxBin, sock, session string) {
	t.Helper()
	client := exec.Command(tmuxBin, "-L", sock, "attach", "-t", session)
	client.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(client, &pty.Winsize{Rows: 50, Cols: 200})
	if err != nil {
		t.Fatalf("pty attach: %v", err)
	}
	t.Cleanup(func() { _ = ptmx.Close(); _ = client.Process.Kill() })
	type clientExit struct {
		err    error
		output string
	}
	exited := make(chan clientExit, 1)
	go func() {
		output, _ := io.ReadAll(ptmx)
		exited <- clientExit{err: client.Wait(), output: string(output)}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case result := <-exited:
			t.Fatalf("tmux attach exited before becoming a client: %v: %s", result.err, result.output)
		default:
		}
		out, listErr := exec.Command(tmuxBin, "-L", sock, "list-clients").CombinedOutput()
		if listErr == nil && strings.TrimSpace(string(out)) != "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("tmux client did not attach within 10s: %v: %s", listErr, out)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestLiveClaude_draft_assumptions pins the three REAL-claude behaviors the
// draft-preservation feature depends on, so a claude upgrade that silently
// changes any of them is caught before users lose drafts again. It drives the
// installed `claude` binary in a real tmux session, so it needs a logged-in
// claude and a TTY-capable environment — gated behind an env flag:
//
//	WISP_DECK_LIVE_CLAUDE_E2E=1 go test ./test/bash/ -run TestLiveClaude -v
//
// Run it whenever the claude binary is upgraded (it appends one throwaway
// entry to the real ~/.claude/history.jsonl, scoped to a temp project dir).
func TestLiveClaude_draft_assumptions(t *testing.T) {
	if os.Getenv("WISP_DECK_LIVE_CLAUDE_E2E") != "1" {
		t.Skip("live-claude guard; set WISP_DECK_LIVE_CLAUDE_E2E=1 to run against the installed claude")
	}
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}
	claudeBin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude not available")
	}
	home, _ := os.UserHomeDir()
	hist := filepath.Join(home, ".claude", "history.jsonl")
	dir := t.TempDir()
	// A real PNG (1x1) so a path paste is recognized as an image.
	png := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE, 0x00, 0x00, 0x00,
		0x0C, 0x49, 0x44, 0x41, 0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
		0x00, 0x00, 0x03, 0x00, 0x01, 0x5D, 0xCC, 0xDC, 0x5B, 0x00, 0x00, 0x00,
		0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
	}
	imgPath := filepath.Join(dir, "probe.png")
	if err := os.WriteFile(imgPath, png, 0o644); err != nil {
		t.Fatal(err)
	}

	sock := fmt.Sprintf("wdlive-e2e-%d", os.Getpid())
	tmux := func(args ...string) ([]byte, error) {
		return exec.Command(tmuxBin, append([]string{"-L", sock}, args...)...).CombinedOutput()
	}
	t.Cleanup(func() { _, _ = tmux("kill-server") })
	if out, err := tmux("new-session", "-d", "-s", "live", "-x", "120", "-y", "40",
		"-c", dir, claudeBin+"; exec bash"); err != nil {
		t.Fatalf("new-session: %v: %s", err, out)
	}

	// The temp project dir is never trusted, so claude opens with the trust
	// dialog — accept it ("1. Yes, I trust this folder") before probing.
	trustDeadline := time.Now().Add(45 * time.Second)
	for {
		pane, _ := tmux("capture-pane", "-p", "-t", "live")
		if strings.Contains(string(pane), "trust this folder") {
			_, _ = tmux("send-keys", "-t", "live", "1")
			time.Sleep(300 * time.Millisecond)
			_, _ = tmux("send-keys", "-t", "live", "Enter")
			break
		}
		if strings.Contains(string(pane), "❯") && !strings.Contains(string(pane), "trust") {
			break // no dialog this time — already at the prompt
		}
		if time.Now().After(trustDeadline) {
			t.Fatalf("claude showed neither the trust dialog nor a prompt:\n%s", pane)
		}
		time.Sleep(1 * time.Second)
	}

	// Assumption 2 (NBSP ready frame): the lib's OWN ready-poll — the exact
	// production grep — must see the real claude become ready. A tmux shim on
	// PATH points the lib's literal `tmux` at the private socket.
	shimDir := filepath.Join(dir, "bin")
	writeTempFile(t, shimDir, "tmux", fmt.Sprintf("#!/bin/bash\nexec %q -L %q \"$@\"\n", tmuxBin, sock))
	if err := os.Chmod(filepath.Join(shimDir, "tmux"), 0755); err != nil {
		t.Fatal(err)
	}
	env := buildEnv(t, []string{shimDir})
	out, code := runBashSnippet(t, accountSwitchSnippet(t, "wait_ai_pane_ready tmux live 60"), env)
	if code != 0 {
		pane, _ := tmux("capture-pane", "-p", "-t", "live")
		t.Fatalf("wait_ai_pane_ready never matched the real claude ready frame — the NBSP prompt assumption broke (rc=%d, %s):\n%s", code, out, pane)
	}

	// Assumption 1 (Esc Esc stashes the draft to history.jsonl).
	before := countLines(t, hist)
	marker := fmt.Sprintf("wisp-deck live guard %d", os.Getpid())
	if out, err := tmux("send-keys", "-t", "live", "-l", marker); err != nil {
		t.Fatalf("send-keys: %v: %s", err, out)
	}
	time.Sleep(1 * time.Second)
	_, _ = tmux("send-keys", "-t", "live", "Escape")
	time.Sleep(300 * time.Millisecond)
	_, _ = tmux("send-keys", "-t", "live", "Escape", "Escape")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if countLines(t, hist) > before {
			data, _ := os.ReadFile(hist)
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if !strings.Contains(lines[len(lines)-1], marker) {
				t.Fatalf("history grew but the tail is not our draft — Esc-Esc stash assumption broke: %q", lines[len(lines)-1])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Esc Esc did not append the draft to history.jsonl — the stash assumption broke")
		}
		time.Sleep(300 * time.Millisecond)
	}

	// Assumption 3 (a bracketed-pasted image path becomes an [Image #N] chip).
	loadCmd := exec.Command(tmuxBin, "-L", sock, "load-buffer", "-b", "liveguard", "-")
	loadCmd.Stdin = strings.NewReader(imgPath)
	if out, err := loadCmd.CombinedOutput(); err != nil {
		t.Fatalf("load-buffer: %v: %s", err, out)
	}
	if out, err := tmux("paste-buffer", "-p", "-b", "liveguard", "-t", "live"); err != nil {
		t.Fatalf("paste-buffer: %v: %s", err, out)
	}
	deadline = time.Now().Add(15 * time.Second)
	for {
		pane, _ := tmux("capture-pane", "-p", "-t", "live")
		if strings.Contains(string(pane), "[Image #") {
			break
		}
		if strings.Contains(string(pane), imgPath) {
			t.Fatalf("pasted image path stayed literal text — the path-paste attachment assumption broke:\n%s", pane)
		}
		if time.Now().After(deadline) {
			t.Fatalf("pasted image path produced neither a chip nor literal text:\n%s", pane)
		}
		time.Sleep(500 * time.Millisecond)
	}
	// Clear the chip so nothing lingers in the session we are about to kill.
	_, _ = tmux("send-keys", "-t", "live", "Escape")
	time.Sleep(200 * time.Millisecond)
	_, _ = tmux("send-keys", "-t", "live", "Escape", "Escape")
	time.Sleep(500 * time.Millisecond)
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return len(strings.Split(strings.TrimRight(string(data), "\n"), "\n"))
}
