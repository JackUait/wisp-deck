package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeAccountSwitchCmd_Registered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"claude-account-switch"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if cmd.Name() != "claude-account-switch" {
		t.Errorf("resolved to %q", cmd.Name())
	}
}

func TestAccountSwitch_selectResultJSON_differentDirWritesPointer(t *testing.T) {
	dir := t.TempDir()
	ptr := filepath.Join(dir, "claude-account")

	got, err := selectResultJSON(ptr, "work-max", "")
	if err != nil {
		t.Fatalf("selectResultJSON: %v", err)
	}
	if got != `{"selected":true,"dir":"work-max","changed":true}` {
		t.Fatalf("json = %s", got)
	}
	data, err := os.ReadFile(ptr)
	if err != nil {
		t.Fatalf("pointer not written: %v", err)
	}
	if strings.TrimSpace(string(data)) != "work-max" {
		t.Fatalf("pointer = %q", data)
	}
}

func TestAccountSwitch_selectResultJSON_sameDirUnchanged(t *testing.T) {
	dir := t.TempDir()
	ptr := filepath.Join(dir, "claude-account")

	got, err := selectResultJSON(ptr, "work-max", "work-max")
	if err != nil {
		t.Fatalf("selectResultJSON: %v", err)
	}
	if got != `{"selected":true,"dir":"work-max","changed":false}` {
		t.Fatalf("json = %s", got)
	}
}

func TestAccountSwitch_selectResultJSON_defaultRemovesPointer(t *testing.T) {
	dir := t.TempDir()
	ptr := filepath.Join(dir, "claude-account")
	if err := os.WriteFile(ptr, []byte("work-max\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := selectResultJSON(ptr, "", "work-max")
	if err != nil {
		t.Fatalf("selectResultJSON: %v", err)
	}
	if got != `{"selected":true,"dir":"","changed":true}` {
		t.Fatalf("json = %s", got)
	}
	if _, err := os.Stat(ptr); !os.IsNotExist(err) {
		t.Fatalf("pointer not removed when selecting Default")
	}
}

func TestAccountSwitch_cancelResultJSON(t *testing.T) {
	if got := cancelResultJSON(); got != `{"selected":false}` {
		t.Fatalf("json = %s", got)
	}
}

func TestAccountSwitch_loadSwitchRows_orderAndCursor(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	ptr := filepath.Join(dir, "claude-account")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	if err := os.WriteFile(list, []byte("Work Max:work-max\nPersonal:personal\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ptr, []byte("personal\n"), 0644); err != nil {
		t.Fatal(err)
	}

	rows, cursor := loadSwitchRows(list, defLabel, ptr)
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if rows[0].Label != "Default" || rows[0].Dir != "" {
		t.Fatalf("row0 = %+v, want Default/\"\"", rows[0])
	}
	if rows[1].Label != "Work Max" || rows[1].Dir != "work-max" {
		t.Fatalf("row1 = %+v", rows[1])
	}
	if rows[2].Label != "Personal" || rows[2].Dir != "personal" {
		t.Fatalf("row2 = %+v", rows[2])
	}
	if cursor != 2 {
		t.Fatalf("cursor = %d, want 2 (active personal)", cursor)
	}
}

func TestAccountSwitch_loadSwitchRows_defaultActiveCursorZero(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	ptr := filepath.Join(dir, "claude-account")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	if err := os.WriteFile(list, []byte("Work Max:work-max\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// No pointer file => Default active.

	rows, cursor := loadSwitchRows(list, defLabel, ptr)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if cursor != 0 {
		t.Fatalf("cursor = %d, want 0 (Default active)", cursor)
	}
}

func TestAccountSwitch_loadSwitchRows_customDefaultLabel(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	ptr := filepath.Join(dir, "claude-account")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	if err := os.WriteFile(defLabel, []byte("My Keychain\n"), 0644); err != nil {
		t.Fatal(err)
	}

	rows, _ := loadSwitchRows(list, defLabel, ptr)
	if rows[0].Label != "My Keychain" {
		t.Fatalf("row0 label = %q, want My Keychain", rows[0].Label)
	}
}
