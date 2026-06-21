package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/ghost-tab/internal/models"
)

// writeKeyedConfig writes a config JSON containing an API key so the config
// counts as "keyed" for main-page filtering.
func writeKeyedConfig(t *testing.T, dir, file string) {
	t.Helper()
	content := `{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-test"}}`
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0600); err != nil {
		t.Fatalf("write keyed config: %v", err)
	}
}

// writeKeylessConfig writes a config JSON with no API key.
func writeKeylessConfig(t *testing.T, dir, file string) {
	t.Helper()
	content := `{"env":{}}`
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0600); err != nil {
		t.Fatalf("write keyless config: %v", err)
	}
}

// subFocusMenu builds a Claude menu with one custom subscription that has an
// API key, so the subscription focus stop is reachable.
func subFocusMenu(t *testing.T, tool string, withConfigs bool) *MainMenuModel {
	t.Helper()
	projects := []models.Project{
		{Name: "alpha", Path: "/tmp/alpha"},
		{Name: "beta", Path: "/tmp/beta"},
	}
	m := NewMainMenu(projects, []string{"claude", "codex"}, tool, "animated")
	m.SetSize(100, 40)
	if withConfigs {
		dir := t.TempDir()
		writeKeyedConfig(t, dir, "work.json")
		m.SetClaudeConfigPaths(filepath.Join(dir, "list"), dir)
		m.SetClaudeConfigs([]ClaudeConfig{{Name: "Work", File: "work.json"}})
	}
	return m
}

func TestSubFocus_downFromAIGoesToSubscription(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	m.SetFocus(FocusAI)
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.Focus() != FocusSubscription {
		t.Errorf("Down from AI = %v, want FocusSubscription", m.Focus())
	}
}

func TestSubFocus_downFromAISkipsWhenNoConfigs(t *testing.T) {
	m := subFocusMenu(t, "claude", false)
	m.SetFocus(FocusAI)
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.Focus() != FocusTabs {
		t.Errorf("Down from AI (no configs) = %v, want FocusTabs", m.Focus())
	}
}

// Subscriptions are shared across agents, so the PLAN row is a reachable focus
// stop for non-Claude agents too (when a keyed config exists).
func TestSubFocus_downFromAIReachesSubscriptionNonClaude(t *testing.T) {
	m := subFocusMenu(t, "codex", true)
	m.SetFocus(FocusAI)
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.Focus() != FocusSubscription {
		t.Errorf("Down from AI (codex) = %v, want FocusSubscription", m.Focus())
	}
}

// The PLAN/subscription row only renders on the Projects tab, so it must not be
// a focus stop on Settings or Stats — otherwise navigating the ring lands on an
// invisible stop.
func TestSubFocus_skippedOnNonProjectTabs(t *testing.T) {
	for _, tab := range []MenuTab{TabSettings, TabStats} {
		m := subFocusMenu(t, "claude", true)
		m.SetActiveTab(tab)
		if m.subscriptionFocusable() {
			t.Errorf("tab %v: subscription should not be focusable when its row is not rendered", tab)
		}

		// Down from AI must skip straight to the tab bar.
		m.SetFocus(FocusAI)
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
		if m.Focus() != FocusTabs {
			t.Errorf("tab %v: Down from AI = %v, want FocusTabs", tab, m.Focus())
		}

		// Up from the tab bar must skip straight back to AI.
		m.SetFocus(FocusTabs)
		m.Update(tea.KeyMsg{Type: tea.KeyUp})
		if m.Focus() != FocusAI {
			t.Errorf("tab %v: Up from tabs = %v, want FocusAI", tab, m.Focus())
		}
	}
}

func TestSubFocus_downFromSubscriptionGoesToTabs(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	m.SetFocus(FocusSubscription)
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.Focus() != FocusTabs {
		t.Errorf("Down from subscription = %v, want FocusTabs", m.Focus())
	}
}

func TestSubFocus_upFromTabsGoesToSubscription(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	m.SetFocus(FocusTabs)
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusSubscription {
		t.Errorf("Up from tabs = %v, want FocusSubscription", m.Focus())
	}
}

func TestSubFocus_upFromTabsSkipsWhenNoConfigs(t *testing.T) {
	m := subFocusMenu(t, "claude", false)
	m.SetFocus(FocusTabs)
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusAI {
		t.Errorf("Up from tabs (no configs) = %v, want FocusAI", m.Focus())
	}
}

func TestSubFocus_upFromSubscriptionGoesToAI(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	m.SetFocus(FocusSubscription)
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusAI {
		t.Errorf("Up from subscription = %v, want FocusAI", m.Focus())
	}
}

func TestSubFocus_rightCyclesSubscription(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	m.SetFocus(FocusSubscription)
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.CurrentClaudeConfigName() != "Work" {
		t.Errorf("Right on subscription = %q, want Work", m.CurrentClaudeConfigName())
	}
}

func TestSubFocus_leftCyclesSubscription(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	m.SetFocus(FocusSubscription)
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.CurrentClaudeConfigName() != "Work" {
		t.Errorf("Left on subscription (wrap) = %q, want Work", m.CurrentClaudeConfigName())
	}
}

func TestSubFocus_rightOnSubscriptionDoesNotCycleAI(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	m.SetFocus(FocusSubscription)
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.CurrentAITool() != "claude" {
		t.Errorf("Right on subscription changed AI to %q, want claude", m.CurrentAITool())
	}
}

func TestSubscriptionRow_showsChevronsWhenFocusable(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	row := stripAnsi(m.renderSubscriptionRow("│", "│"))
	if !strings.Contains(row, "◂") || !strings.Contains(row, "▸") {
		t.Errorf("subscription row missing cycle chevrons when focusable:\n%s", row)
	}
}

func TestSubscriptionRow_noChevronsWhenNoConfigs(t *testing.T) {
	m := subFocusMenu(t, "claude", false)
	row := stripAnsi(m.renderSubscriptionRow("│", "│"))
	if strings.Contains(row, "◂") || strings.Contains(row, "▸") {
		t.Errorf("subscription row should not show chevrons with no custom configs:\n%s", row)
	}
}

func TestSubFocus_helpHintMentionsSubscription(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	m.SetFocus(FocusSubscription)
	hint := m.focusHint()
	if !strings.Contains(strings.ToLower(hint), "subscription") {
		t.Errorf("focus hint for subscription = %q, want it to mention subscription", hint)
	}
}

// --- Key-filtering on the main page ---

// A custom config without an API key is not a reachable main-page focus stop.
func TestMainSub_keylessConfigNotFocusable(t *testing.T) {
	projects := []models.Project{{Name: "a", Path: "/a"}}
	m := NewMainMenu(projects, []string{"claude", "codex"}, "claude", "animated")
	m.SetSize(100, 40)
	dir := t.TempDir()
	writeKeylessConfig(t, dir, "nokey.json")
	m.SetClaudeConfigPaths(filepath.Join(dir, "list"), dir)
	m.SetClaudeConfigs([]ClaudeConfig{{Name: "NoKey", File: "nokey.json"}})

	m.SetFocus(FocusAI)
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.Focus() != FocusTabs {
		t.Errorf("Down from AI with only a keyless config = %v, want FocusTabs (not focusable)", m.Focus())
	}
}

// The main-page cycle skips keyless configs and only visits Standard + keyed.
func TestMainSub_cycleSkipsKeylessConfig(t *testing.T) {
	projects := []models.Project{{Name: "a", Path: "/a"}}
	m := NewMainMenu(projects, []string{"claude", "codex"}, "claude", "animated")
	m.SetSize(100, 40)
	dir := t.TempDir()
	writeKeyedConfig(t, dir, "work.json")
	writeKeylessConfig(t, dir, "nokey.json")
	m.SetClaudeConfigPaths(filepath.Join(dir, "list"), dir)
	// Work (keyed, index 1) then NoKey (keyless, index 2).
	m.SetClaudeConfigs([]ClaudeConfig{
		{Name: "Work", File: "work.json"},
		{Name: "NoKey", File: "nokey.json"},
	})
	m.SetFocus(FocusSubscription)

	// Standard -> Work, then Work -> wrap to Standard (keyless NoKey skipped).
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.CurrentClaudeConfigName() != "Work" {
		t.Fatalf("first Right = %q, want Work", m.CurrentClaudeConfigName())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.CurrentClaudeConfigName() != "Standard Claude" {
		t.Errorf("second Right = %q, want Standard Claude (keyless NoKey skipped)", m.CurrentClaudeConfigName())
	}
}

// Focus is reachable as long as at least one keyed config exists, even when
// other keyless configs are present.
func TestMainSub_focusableWhenAnyKeyedConfigExists(t *testing.T) {
	projects := []models.Project{{Name: "a", Path: "/a"}}
	m := NewMainMenu(projects, []string{"claude", "codex"}, "claude", "animated")
	m.SetSize(100, 40)
	dir := t.TempDir()
	writeKeyedConfig(t, dir, "work.json")
	writeKeylessConfig(t, dir, "nokey.json")
	m.SetClaudeConfigPaths(filepath.Join(dir, "list"), dir)
	m.SetClaudeConfigs([]ClaudeConfig{
		{Name: "NoKey", File: "nokey.json"},
		{Name: "Work", File: "work.json"},
	})
	if !m.subscriptionFocusable() {
		t.Errorf("subscription should be focusable when at least one keyed config exists")
	}
}
