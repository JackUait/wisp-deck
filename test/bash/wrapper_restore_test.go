package bash_test

import (
	"os"
	"path/filepath"
	"testing"
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
		"lazygit":       "#!/bin/bash\nexit 0\n",
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
	assertContains(t, got, "claude --resume sid-42")

	if _, err := os.Stat(filepath.Join(confDir, "restore-queue")); err == nil {
		t.Error("queue entry must be consumed exactly once (file should be gone)")
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
	// Record every select-layout invocation's args; other subcommands no-op.
	mocks := map[string]string{
		"tmux":          "#!/bin/bash\nif [ \"$1\" = \"select-layout\" ]; then printf '%s\\n' \"$*\" >> \"$GT_LAYOUT_REC\"; fi\nexit 0\n",
		"claude":        "#!/bin/bash\nexit 0\n",
		"lazygit":       "#!/bin/bash\nexit 0\n",
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

	env := buildEnv(t, nil, "HOME="+home, "GT_LAYOUT_REC="+layoutRec)
	_, code := runBashScript(t, "wrapper.sh", nil, env)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(layoutRec)
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
		"lazygit":       "#!/bin/bash\nexit 0\n",
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

	env := buildEnv(t, nil, "HOME="+home, "GT_LAYOUT_REC="+layoutRec)
	_, code := runBashScript(t, "wrapper.sh", nil, env)
	assertExitCode(t, code, 0)

	if _, err := os.Stat(layoutRec); err == nil {
		t.Error("select-layout must not run for an entry without a layout field")
	}
}
