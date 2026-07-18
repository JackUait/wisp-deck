package bash_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestWrapperInteractive_pops_restore_queue_into_current_window runs the real
// wrapper.sh with no arguments and a pending restore-queue entry, and
// verifies the window takes over that entry instead of showing the picker:
// it reaches new-session directly, forces the tool, stamps the project path,
// applies the resume launch flag, and consumes the queue entry.
//
// wrapper.sh line 2 resets PATH to start with "$HOME/.local/bin", so mocks
// must live there and HOME must be overridden to our temp dir.
func TestWrapperInteractive_pops_restore_queue_into_current_window(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}

	recPath := filepath.Join(home, "rec")

	mocks := map[string]string{
		"tmux":          "#!/bin/bash\nif [ \"$1\" = \"new-session\" ]; then printf '%s\\n' \"$*\" > \"$GT_REC\"; exit 0; fi\nexit 0\n",
		"claude":        "#!/bin/bash\nexit 0\n",
		"wisp-deck-tui": "#!/bin/bash\nexit 0\n",
		"sysctl":        "#!/bin/bash\necho \"{ sec = 12345, usec = 1 } Thu Jul  2 01:01:01 2026\"\n",
	}
	for name, body := range mocks {
		p := filepath.Join(binDir, name)
		if err := os.WriteFile(p, []byte(body), 0755); err != nil {
			t.Fatalf("write mock %s: %v", name, err)
		}
	}

	projDir := filepath.Join(home, "proj")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}

	confDir := filepath.Join(home, ".config", "wisp-deck")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatalf("mkdir conf: %v", err)
	}
	// Queue for the current boot with one pending entry; the restore gate has
	// already run this boot (marker matches), so only the pop path is active.
	if err := os.WriteFile(filepath.Join(confDir, "restore-queue"),
		[]byte("12345|"+projDir+"|claude|sid-42\n"), 0644); err != nil {
		t.Fatalf("write queue: %v", err)
	}
	// This launch is a chain-spawned tab: it holds the chain ticket issued by
	// the previous tab's restore_advance. Without it, popping would hijack a
	// user-opened tab (see restore_chain_ticket_test.go).
	if err := os.WriteFile(filepath.Join(confDir, "restore-chain-ticket"),
		[]byte(strconv.FormatInt(time.Now().Unix(), 10)+"\n"), 0644); err != nil {
		t.Fatalf("write ticket: %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "last-restore-boot"),
		[]byte("12345\n"), 0644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	env := buildEnv(t, nil, "HOME="+home, "GT_REC="+recPath)

	_, code := runBashScript(t, "wrapper.sh", nil, env)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatalf("new-session was never invoked (queue entry not restored): %v", err)
	}
	got := string(data)
	assertContains(t, got, "WISP_DECK=1")
	assertContains(t, got, "WISP_DECK_TOOL=claude")
	assertContains(t, got, "WISP_DECK_PATH="+projDir)
	// The entry's own conversation is resumed — not `claude -c`, which would
	// open the same (most recent) conversation in every tab of the project.
	assertContains(t, normalizeShellEscapedSpaces(got), "claude --resume sid-42")

	if _, err := os.Stat(filepath.Join(confDir, "restore-queue")); err == nil {
		t.Error("queue entry must be consumed exactly once (file should be gone)")
	}
}

func TestWrapperRestoreCodexWiresDurableIdentityAndToolSpecificSession(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	recPath := filepath.Join(home, "rec")
	mocks := map[string]string{
		"tmux":          "#!/bin/bash\nif [ \"$1\" = \"new-session\" ]; then printf '%s\\n' \"$*\" > \"$GT_REC\"; exit 0; fi\nexit 0\n",
		"claude":        "#!/bin/bash\nexit 0\n",
		"codex":         "#!/bin/bash\nexit 0\n",
		"wisp-deck-tui": "#!/bin/bash\nexit 0\n",
		"sysctl":        "#!/bin/bash\necho \"{ sec = 12345, usec = 1 } Thu Jul  2 01:01:01 2026\"\n",
	}
	for name, body := range mocks {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0o755); err != nil {
			t.Fatalf("write mock %s: %v", name, err)
		}
	}
	projDir := filepath.Join(home, "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	confDir := filepath.Join(home, ".config", "wisp-deck")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "restore-queue"),
		[]byte("12345|"+projDir+"|codex|"+codexSessionA+"|||prior.codex\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "last-restore-boot"), []byte("12345\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedChainTicket(t, confDir)

	_, code := runBashScript(t, "wrapper.sh", nil,
		buildEnv(t, nil, "HOME="+home, "GT_REC="+recPath))
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatal(err)
	}
	got := normalizeShellEscapedSpaces(string(data))
	assertContains(t, got, "WISP_DECK_TOOL=codex")
	assertContains(t, got, "WISP_DECK_CODEX_SESSION="+codexSessionA)
	assertContains(t, got, "WISP_DECK_CLAUDE_SESSION=")
	assertNotContains(t, got, "WISP_DECK_CLAUDE_SESSION="+codexSessionA)
	identityPrefix := filepath.Join(confDir, "session-identities", "dev-proj-")
	assertContains(t, got, "WISP_DECK_CODEX_SESSION_FILE="+identityPrefix)
	assertContains(t, got, "--session-file "+identityPrefix)
	assertContains(t, got, "--resume-session "+codexSessionA)

	identityDir := filepath.Join(confDir, "session-identities")
	info, err := os.Stat(identityDir)
	if err != nil {
		t.Fatal(err)
	}
	if gotMode := info.Mode().Perm(); gotMode != 0o700 {
		t.Fatalf("identity directory mode = %#o, want 0700", gotMode)
	}
	if !strings.Contains(got, ".codex") {
		t.Fatalf("Codex identity path lacks .codex suffix: %q", got)
	}
}

// TestWrapperRestore_applies_captured_layout verifies that when the popped
// queue entry carries a window_layout field, the wrapper replays it with
// `tmux select-layout` after building the panes — reproducing the exact pane
// positions the session had when it was closed.
func TestWrapperRestore_applies_captured_layout(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}

	layoutRec := filepath.Join(home, "layout-rec")
	// Record every select-layout invocation's args; answer the layout
	// watcher's window-size probe so it applies; other subcommands no-op.
	mocks := map[string]string{
		"tmux":          "#!/bin/bash\nif [ \"$1\" = \"select-layout\" ]; then printf '%s\\n' \"$*\" >> \"$GT_LAYOUT_REC\"; fi\nif [ \"$1\" = \"display-message\" ]; then echo 204x50; fi\nexit 0\n",
		"claude":        "#!/bin/bash\nexit 0\n",
		"wisp-deck-tui": "#!/bin/bash\nexit 0\n",
		"sysctl":        "#!/bin/bash\necho \"{ sec = 12345, usec = 1 } Thu Jul  2 01:01:01 2026\"\n",
	}
	for name, body := range mocks {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0755); err != nil {
			t.Fatalf("write mock %s: %v", name, err)
		}
	}

	projDir := filepath.Join(home, "proj")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	confDir := filepath.Join(home, ".config", "wisp-deck")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatalf("mkdir conf: %v", err)
	}

	layout := "bdba,204x50,0,0{152x50,0,0,1,51x50,153,0,2}"
	if err := os.WriteFile(filepath.Join(confDir, "restore-queue"),
		[]byte("12345|"+projDir+"|claude|sid-42|"+layout+"\n"), 0644); err != nil {
		t.Fatalf("write queue: %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "last-restore-boot"),
		[]byte("12345\n"), 0644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	seedChainTicket(t, confDir)

	env := buildEnv(t, nil, "HOME="+home, "GT_LAYOUT_REC="+layoutRec)
	_, code := runBashScript(t, "wrapper.sh", nil, env)
	assertExitCode(t, code, 0)

	// The replay runs in a backgrounded watcher (new-session blocks in real
	// life), so give it a moment past the wrapper's own exit.
	deadline := time.Now().Add(5 * time.Second)
	var data []byte
	var err error
	for time.Now().Before(deadline) {
		data, err = os.ReadFile(layoutRec)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("select-layout was never invoked (captured layout not replayed): %v", err)
	}
	assertContains(t, string(data), layout)
}

// TestWrapperRestore_skips_layout_when_empty verifies backward compatibility:
// an old-format queue entry without a layout field must NOT trigger
// select-layout, leaving the default pane split in place.
func TestWrapperRestore_skips_layout_when_empty(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}

	layoutRec := filepath.Join(home, "layout-rec")
	mocks := map[string]string{
		"tmux":          "#!/bin/bash\nif [ \"$1\" = \"select-layout\" ]; then printf '%s\\n' \"$*\" >> \"$GT_LAYOUT_REC\"; fi\nexit 0\n",
		"claude":        "#!/bin/bash\nexit 0\n",
		"wisp-deck-tui": "#!/bin/bash\nexit 0\n",
		"sysctl":        "#!/bin/bash\necho \"{ sec = 12345, usec = 1 } Thu Jul  2 01:01:01 2026\"\n",
	}
	for name, body := range mocks {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0755); err != nil {
			t.Fatalf("write mock %s: %v", name, err)
		}
	}

	projDir := filepath.Join(home, "proj")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	confDir := filepath.Join(home, ".config", "wisp-deck")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatalf("mkdir conf: %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "restore-queue"),
		[]byte("12345|"+projDir+"|claude|sid-42\n"), 0644); err != nil {
		t.Fatalf("write queue: %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "last-restore-boot"),
		[]byte("12345\n"), 0644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	seedChainTicket(t, confDir)

	env := buildEnv(t, nil, "HOME="+home, "GT_LAYOUT_REC="+layoutRec)
	_, code := runBashScript(t, "wrapper.sh", nil, env)
	assertExitCode(t, code, 0)

	if _, err := os.Stat(layoutRec); err == nil {
		t.Error("select-layout must not run for an entry without a layout field")
	}
}

// TestWrapperRestore_skips_entry_whose_conversation_is_already_open is the
// last-line duplicate defense at the wrapper level: even if the queue somehow
// carries an entry for a conversation that is ALREADY running in an alive
// Wisp Deck session (a rebuilt queue, a duplicated snapshot line — any
// upstream failure), the wrapper must refuse it and take the next entry.
// It also verifies the restored sid is stamped into the session environment
// at creation (new-session -e), which is what makes this defense work for
// tabs restored moments ago whose statusline hasn't stamped the sid yet.
func TestWrapperRestore_skips_entry_whose_conversation_is_already_open(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}

	recPath := filepath.Join(home, "rec")
	tmuxMock := `#!/bin/bash
case "$1" in
  new-session) printf '%s\n' "$*" > "$GT_REC"; exit 0 ;;
  list-sessions) echo "dev-open-1"; exit 0 ;;
  show-environment) printf 'WISP_DECK=1\nWISP_DECK_CLAUDE_SESSION=sid-42\n'; exit 0 ;;
esac
exit 0
`
	mocks := map[string]string{
		"tmux":          tmuxMock,
		"claude":        "#!/bin/bash\nexit 0\n",
		"wisp-deck-tui": "#!/bin/bash\nexit 0\n",
		"sysctl":        "#!/bin/bash\necho \"{ sec = 12345, usec = 1 } Thu Jul  2 01:01:01 2026\"\n",
		"osascript":     "#!/bin/bash\nexit 1\n",
		"open":          "#!/bin/bash\nexit 0\n",
	}
	for name, body := range mocks {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0755); err != nil {
			t.Fatalf("write mock %s: %v", name, err)
		}
	}

	projDir := filepath.Join(home, "proj")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	confDir := filepath.Join(home, ".config", "wisp-deck")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatalf("mkdir conf: %v", err)
	}
	// Entry 1's conversation (sid-42) is already open per the tmux mock;
	// entry 2 (sid-43) is fresh and must be the one restored.
	if err := os.WriteFile(filepath.Join(confDir, "restore-queue"),
		[]byte("12345|"+projDir+"|claude|sid-42|\n12345|"+projDir+"|claude|sid-43|\n"), 0644); err != nil {
		t.Fatalf("write queue: %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "last-restore-boot"),
		[]byte("12345\n"), 0644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	seedChainTicket(t, confDir)

	env := buildEnv(t, nil, "HOME="+home, "GT_REC="+recPath)
	_, code := runBashScript(t, "wrapper.sh", nil, env)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatalf("new-session was never invoked: %v", err)
	}
	got := string(data)
	assertContains(t, normalizeShellEscapedSpaces(got), "claude --resume sid-43")
	assertNotContains(t, got, "sid-42")
	// The restored sid must be stamped into the session env at creation.
	assertContains(t, got, "WISP_DECK_CLAUDE_SESSION=sid-43")

	if _, err := os.Stat(filepath.Join(confDir, "restore-queue")); err == nil {
		t.Error("both entries must be consumed (one refused, one restored)")
	}
}
