package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// claudeProjectHash mirrors lib/session-restore.sh's claude_project_dir: every
// non-alphanumeric byte of the project path becomes '-'.
func claudeProjectHash(path string) string {
	var b strings.Builder
	for _, r := range path {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// wrapperOpenMocks writes the minimal mock toolchain wrapper.sh needs to reach
// new-session, recording the new-session args to GT_REC.
func wrapperOpenMocks(t *testing.T, binDir string) {
	t.Helper()
	mocks := map[string]string{
		"tmux":          "#!/bin/bash\nif [ \"$1\" = \"new-session\" ]; then printf '%s\\n' \"$*\" > \"$GT_REC\"; exit 0; fi\nexit 0\n",
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
}

// TestWrapperOpen_starts_fresh_even_with_prior_conversation: starting a session
// for a project (not a restore) must always start a FRESH claude, even when the
// project has resumable conversations. Resuming a prior conversation on open
// belongs to reboot-restore only — a plain open is a new session. So the launch
// must carry neither `--resume` nor a `-c` continue flag.
func TestWrapperOpen_starts_fresh_even_with_prior_conversation(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	wrapperOpenMocks(t, binDir)

	projDir := filepath.Join(home, "proj")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "wisp-deck"), 0755); err != nil {
		t.Fatalf("mkdir conf: %v", err)
	}

	// A resumable transcript (contains a model turn) for this project's cwd.
	txDir := filepath.Join(home, ".claude", "projects", claudeProjectHash(projDir))
	if err := os.MkdirAll(txDir, 0755); err != nil {
		t.Fatalf("mkdir transcripts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(txDir, "conv-99.jsonl"),
		[]byte(`{"type":"assistant","message":{}}`+"\n"), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	env := buildEnv(t, nil, "HOME="+home, "GT_REC="+filepath.Join(home, "rec"))
	_, code := runBashScript(t, "wrapper.sh", []string{projDir}, env)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(filepath.Join(home, "rec"))
	if err != nil {
		t.Fatalf("new-session was never invoked: %v", err)
	}
	got := string(data)
	assertNotContains(t, got, "--resume")
	assertNotContains(t, got, "claude -c")
}

// TestWrapperOpen_fresh_when_no_resumable_conversation: with no resumable
// conversation for the project, opening it must start a fresh claude — no
// `--resume` and no `-c` continue flag.
func TestWrapperOpen_fresh_when_no_resumable_conversation(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	wrapperOpenMocks(t, binDir)

	projDir := filepath.Join(home, "proj")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "wisp-deck"), 0755); err != nil {
		t.Fatalf("mkdir conf: %v", err)
	}

	env := buildEnv(t, nil, "HOME="+home, "GT_REC="+filepath.Join(home, "rec"))
	_, code := runBashScript(t, "wrapper.sh", []string{projDir}, env)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(filepath.Join(home, "rec"))
	if err != nil {
		t.Fatalf("new-session was never invoked: %v", err)
	}
	got := string(data)
	assertNotContains(t, got, "--resume")
	assertNotContains(t, got, "claude -c")
}
