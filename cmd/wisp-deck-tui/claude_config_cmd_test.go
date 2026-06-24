package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeConfigCmd_Registered(t *testing.T) {
	for _, path := range [][]string{
		{"claude-config"},
		{"claude-config", "add"},
		{"claude-config", "rename"},
		{"claude-config", "delete"},
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

func execRoot(t *testing.T, args ...string) string {
	t.Helper()
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(args)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(%v): %v\noutput: %s", args, err, buf.String())
	}
	return buf.String()
}

func TestClaudeConfigCmd_AddRenameDelete(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-configs.list")
	cfgDir := filepath.Join(dir, "claude-configs")
	ptr := filepath.Join(dir, "claude-config")

	out := execRoot(t, "claude-config", "add", "--list", list, "--dir", cfgDir, "--name", "My Work")
	if strings.TrimSpace(out) != "my-work.json" {
		t.Fatalf("add printed %q, want my-work.json", strings.TrimSpace(out))
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "my-work.json")); err != nil {
		t.Fatal("config file not created")
	}

	execRoot(t, "claude-config", "rename", "--list", list, "--file", "my-work.json", "--name", "Day Job")
	data, _ := os.ReadFile(list)
	if !strings.Contains(string(data), "Day Job:my-work.json") {
		t.Fatalf("rename not applied: %q", data)
	}

	os.WriteFile(ptr, []byte("my-work.json\n"), 0644)
	execRoot(t, "claude-config", "delete", "--list", list, "--dir", cfgDir, "--pointer", ptr, "--file", "my-work.json")
	if _, err := os.Stat(filepath.Join(cfgDir, "my-work.json")); !os.IsNotExist(err) {
		t.Fatal("config file not deleted")
	}
	if _, err := os.Stat(ptr); !os.IsNotExist(err) {
		t.Fatal("pointer not cleared after deleting active config")
	}
}
