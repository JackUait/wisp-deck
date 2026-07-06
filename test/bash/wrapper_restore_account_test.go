package bash_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Regression for the post-reboot incident: sessions had been writing their
// transcripts to one Claude config dir (~/.claude), the active login was then
// switched to an isolated account dir, and the Mac rebooted. The restore
// queue-build validated transcripts only against ~/.claude while the restored
// tab launched claude under the account dir — so `--resume` either got blanked
// (transcript "missing") or failed at startup, and the `-c` fallback opened a
// stale conversation from the other store. With conversation state shared
// across logins (merged + symlinked before the restore gate runs), the exact
// sid survives queue-build and is resumable under any login.
func TestWrapperRestore_resumes_sid_recorded_in_account_store(t *testing.T) {
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
	// Prior-boot snapshot pointing at a conversation whose transcript was
	// written under the "personal" account's isolated config dir — NOT under
	// ~/.claude, where queue-build's resumability check looks.
	sid := "sid-acc-1"
	snap := "999|proj|" + projDir + "|claude|ghostty|" + sid + "|\n"
	if err := os.WriteFile(filepath.Join(confDir, "last-session"), []byte(snap), 0644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	slug := regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(projDir, "-")
	writeSharedFile(t,
		filepath.Join(confDir, "claude-accounts", "personal", "projects", slug, sid+".jsonl"),
		`{"type":"assistant"}`)

	env := buildEnv(t, nil, "HOME="+home, "GT_REC="+recPath)
	_, code := runBashScript(t, "wrapper.sh", nil, env)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatalf("new-session was never invoked: %v", err)
	}
	// The tab must resume its own conversation — not fall back to `claude -c`,
	// which opens whatever conversation the launch store considers most recent.
	assertContains(t, string(data), "claude --resume "+sid)

	// And the account store is now a link into the shared store, so the resume
	// works no matter which login the restored tab launches under.
	link := filepath.Join(confDir, "claude-accounts", "personal", "projects")
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("account projects must be symlinked to the shared store after launch")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "projects", slug, sid+".jsonl")); err != nil {
		t.Fatalf("transcript must have been merged into ~/.claude: %v", err)
	}
}
