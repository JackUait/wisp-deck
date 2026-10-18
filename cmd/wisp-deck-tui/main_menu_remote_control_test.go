package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The menu must learn Claude's settings path only from the launcher. Read from
// HOME, any test that walks the Settings rows would rewrite the real file.
func TestBuildMainMenuModel_RemoteControlUsesOnlyTheFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	homeSettings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(homeSettings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(homeSettings, []byte(`{"remoteControlAtStartup": true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	oldFile := mainMenuClaudeSettingsFile
	oldProjects := mainMenuProjectsFile
	t.Cleanup(func() {
		mainMenuClaudeSettingsFile = oldFile
		mainMenuProjectsFile = oldProjects
	})
	mainMenuProjectsFile = filepath.Join(t.TempDir(), "projects")

	mainMenuClaudeSettingsFile = ""
	model, err := buildMainMenuModel()
	if err != nil {
		t.Fatal(err)
	}
	model.CycleRemoteControl()
	if data, _ := os.ReadFile(homeSettings); string(data) != `{"remoteControlAtStartup": true}` {
		t.Fatalf("menu wrote HOME's settings without the flag:\n%s", data)
	}

	flagged := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(flagged, []byte(`{"remoteControlAtStartup": false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	mainMenuClaudeSettingsFile = flagged
	model, err = buildMainMenuModel()
	if err != nil {
		t.Fatal(err)
	}
	model.CycleRemoteControl()
	if !model.RemoteControlEnabled() {
		t.Fatal("menu did not toggle the flagged file")
	}
}
