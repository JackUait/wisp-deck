package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// spyBridge stands in for gptbridge.ChatGPTBridge so a test can see the
// shutdown the launch wrapper owes it.
type spyBridge struct {
	mu     sync.Mutex
	closes int
	events *[]string
}

func (b *spyBridge) Endpoint() (string, string, error) {
	return "http://127.0.0.1:1", "sk-wisp-test", nil
}

func (b *spyBridge) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closes++
	if b.events != nil {
		*b.events = append(*b.events, "close")
	}
}

func (b *spyBridge) closeCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closes
}

func allInBridgeFixture(t *testing.T) (settings string, configsDir string) {
	t.Helper()
	dir := t.TempDir()
	settings = filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(settings,
		[]byte(`{"env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return settings, filepath.Join(dir, "configs")
}

// A leaked app-server is a 220MB process per launch on a machine that runs
// fifteen sessions, so the shutdown has to run on the ordinary exit route.
func TestClaudeAllIn_closes_the_chatgpt_bridge_when_the_child_exits(t *testing.T) {
	settings, configs := allInBridgeFixture(t)
	bridge := &spyBridge{}
	command := newClaudeAllInCommandWithBridge(
		func([]string) error { return nil }, func(int) {},
		func(string, func() string) allinBridge { return bridge })
	command.SetArgs([]string{"--settings", settings, "--configs-dir", configs, "--", "true"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if bridge.closeCount() != 1 {
		t.Fatalf("bridge closed %d times; want 1", bridge.closeCount())
	}
}

// The child's exit code is propagated with os.Exit in production, which runs no
// deferred function. So the shutdown has to happen BEFORE the exit call, not in
// a defer around it.
func TestClaudeAllIn_closes_the_chatgpt_bridge_before_it_propagates_an_exit_code(t *testing.T) {
	settings, configs := allInBridgeFixture(t)
	var events []string
	bridge := &spyBridge{events: &events}
	command := newClaudeAllInCommandWithBridge(
		func([]string) error { return exitCodeError(7) },
		func(code int) { events = append(events, "exit") },
		func(string, func() string) allinBridge { return bridge })
	command.SetArgs([]string{"--settings", settings, "--configs-dir", configs, "--", "true"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0] != "close" || events[1] != "exit" {
		t.Fatalf("events %v; want the bridge closed before the exit code is propagated", events)
	}
}

// Every early return runs the child unwrapped — an unreadable overlay, one that
// names no endpoint, one already pointing at loopback. None of them may leave a
// bridge behind.
func TestClaudeAllIn_closes_the_chatgpt_bridge_when_the_launch_is_left_unwrapped(t *testing.T) {
	bridge := &spyBridge{}
	ran := false
	command := newClaudeAllInCommandWithBridge(
		func([]string) error { ran = true; return nil }, func(int) {},
		func(string, func() string) allinBridge { return bridge })
	command.SetArgs([]string{"--settings", filepath.Join(t.TempDir(), "absent.json"), "--", "true"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("child never ran")
	}
	if bridge.closeCount() != 1 {
		t.Fatalf("bridge closed %d times on the unwrapped path; want 1", bridge.closeCount())
	}
}

// wrapper.sh stamps WISP_DECK_CODEX_CMD into the tmux session environment, and
// claude-allin inherits it. Without it the bridge has nothing to exec and every
// ChatGPT row answers 400.
func TestClaudeAllIn_takes_the_codex_path_from_the_session_environment(t *testing.T) {
	settings, configs := allInBridgeFixture(t)
	t.Setenv("WISP_DECK_CODEX_CMD", "/opt/Codex App/codex")
	var got string
	command := newClaudeAllInCommandWithBridge(
		func([]string) error { return nil }, func(int) {},
		func(path string, _ func() string) allinBridge { got = path; return &spyBridge{} })
	command.SetArgs([]string{"--settings", settings, "--configs-dir", configs, "--", "true"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if got != "/opt/Codex App/codex" {
		t.Fatalf("bridge was built with codex path %q", got)
	}
}

// A relative value cannot be trusted to name Codex — it would resolve against
// whatever directory the pane happens to sit in. Reported as absent, which the
// bridge answers with a 400 naming Codex rather than execing something else.
func TestClaudeAllIn_ignores_a_relative_codex_path(t *testing.T) {
	settings, configs := allInBridgeFixture(t)
	t.Setenv("WISP_DECK_CODEX_CMD", "codex")
	t.Setenv("PATH", t.TempDir())
	got := "unset"
	command := newClaudeAllInCommandWithBridge(
		func([]string) error { return nil }, func(int) {},
		func(path string, _ func() string) allinBridge { got = path; return &spyBridge{} })
	command.SetArgs([]string{"--settings", settings, "--configs-dir", configs, "--", "true"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("bridge was built with codex path %q; want the relative value dropped", got)
	}
}

// A tab opened while Codex was being reinstalled has no WISP_DECK_CODEX_CMD.
// Every GPT row in that pane answered 400 until a relaunch; the bridge must
// find Codex on PATH instead, and keep looking on later turns.
func TestClaudeAllIn_finds_codex_when_the_session_environment_has_none(t *testing.T) {
	settings, configs := allInBridgeFixture(t)
	bin := t.TempDir()
	codex := filepath.Join(bin, "codex")
	if err := os.WriteFile(codex, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WISP_DECK_CODEX_CMD", "")
	t.Setenv("PATH", bin)
	var got string
	var lookup func() string
	command := newClaudeAllInCommandWithBridge(
		func([]string) error { return nil }, func(int) {},
		func(path string, find func() string) allinBridge { got, lookup = path, find; return &spyBridge{} })
	command.SetArgs([]string{"--settings", settings, "--configs-dir", configs, "--", "true"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if got != codex {
		t.Fatalf("bridge was built with codex path %q; want %q", got, codex)
	}
	if lookup == nil || lookup() != codex {
		t.Fatal("bridge has no lookup to find a Codex installed after launch")
	}
}
