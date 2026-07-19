package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jackuait/wisp-deck/internal/models"
)

// The main-page subscription switcher was removed (choosing a backend now lives
// in Settings › Subscription and the mid-session account popup), so the header
// focus/cycle tests that lived here are gone. These helpers survive because
// other tests still build a Claude menu carrying a keyed subscription config.

// writeKeyedConfig writes a config JSON containing an API key so the config
// counts as "keyed" (authentication-ready).
func writeKeyedConfig(t *testing.T, dir, file string) {
	t.Helper()
	content := `{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-test"}}`
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0600); err != nil {
		t.Fatalf("write keyed config: %v", err)
	}
}

// subFocusMenu builds a Claude menu with one custom subscription that has an
// API key.
func subFocusMenu(t *testing.T, tool string, withConfigs bool) *MainMenuModel {
	t.Helper()
	projects := []models.Project{
		{Name: "alpha", Path: "/tmp/alpha"},
		{Name: "beta", Path: "/tmp/beta"},
	}
	m := NewMainMenu(projects, []string{"claude", "opencode"}, tool, "animated")
	m.SetSize(100, 40)
	if withConfigs {
		dir := t.TempDir()
		writeKeyedConfig(t, dir, "work.json")
		m.SetClaudeConfigPaths(filepath.Join(dir, "list"), dir)
		m.SetClaudeConfigs([]ClaudeConfig{{Name: "Work", File: "work.json"}})
	}
	return m
}
