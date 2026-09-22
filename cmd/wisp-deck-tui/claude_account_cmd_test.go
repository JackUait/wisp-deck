package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeAccountCmd_Registered(t *testing.T) {
	for _, path := range [][]string{
		{"claude-account"},
		{"claude-account", "add"},
		{"claude-account", "remove"},
	} {
		cmd, _, err := rootCmd.Find(path)
		if err != nil {
			t.Fatalf("Find(%v): %v", path, err)
		}
		if cmd.Name() != path[len(path)-1] {
			t.Errorf("Find(%v) resolved to %q", path, cmd.Name())
		}
	}
}

func TestClaudeAccountCmd_AddRemove(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	acctDir := filepath.Join(dir, "claude-accounts")
	ptr := filepath.Join(dir, "claude-account")

	out := execRoot(t, "claude-account", "add", "--list", list, "--accounts-dir", acctDir, "--label", "Work Max")
	if strings.TrimSpace(out) != "work-max" {
		t.Fatalf("add printed %q, want work-max", strings.TrimSpace(out))
	}
	if info, err := os.Stat(filepath.Join(acctDir, "work-max")); err != nil || !info.IsDir() {
		t.Fatal("account dir not created")
	}
	data, _ := os.ReadFile(list)
	if !strings.Contains(string(data), "Work Max:work-max") {
		t.Fatalf("list entry not written: %q", data)
	}

	os.WriteFile(ptr, []byte("work-max\n"), 0644)
	execRoot(t, "claude-account", "remove", "--list", list, "--accounts-dir", acctDir, "--pointer", ptr, "--dir", "work-max")
	if _, err := os.Stat(filepath.Join(acctDir, "work-max")); !os.IsNotExist(err) {
		t.Fatal("account dir not removed")
	}
	if _, err := os.Stat(ptr); !os.IsNotExist(err) {
		t.Fatal("pointer not cleared after removing active account")
	}
}

func TestClaudeAccountCmd_ReconcilePinsEachSlot(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	list := filepath.Join(dir, "claude-accounts.list")
	acctDir := filepath.Join(dir, "claude-accounts")
	emails := filepath.Join(dir, "claude-account-emails")
	os.MkdirAll(home, 0o700)
	os.MkdirAll(filepath.Join(acctDir, "personal"), 0o700)
	os.WriteFile(list, []byte("Personal:personal\n"), 0o644)
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"oauthAccount":{"emailAddress":"work@corp.io"}}`), 0o600)
	os.WriteFile(filepath.Join(acctDir, "personal", ".claude.json"), []byte(`{"oauthAccount":{"emailAddress":"me@gmail.com"}}`), 0o600)

	out := execRoot(t, "claude-account", "reconcile", "--list", list, "--accounts-dir", acctDir,
		"--emails", emails, "--home", home)
	if out != "" {
		t.Fatalf("reconcile must print nothing, it runs in the launch pane: %q", out)
	}
	data, _ := os.ReadFile(emails)
	if string(data) != "default:work@corp.io\npersonal:me@gmail.com\n" {
		t.Fatalf("pins = %q", data)
	}
}
