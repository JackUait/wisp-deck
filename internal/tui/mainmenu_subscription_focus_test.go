package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/models"
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

func TestSubFocus_upFromAIGoesToSubscription(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	m.SetFocus(FocusAI)
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusSubscription {
		t.Errorf("Up from AI = %v, want FocusSubscription", m.Focus())
	}
}

// The PLAN row still renders with a single subscription, but it is not a focus
// stop — the AGENT row is then the top of the ring and ↑ leaves focus put.
func TestSubFocus_upFromAIStaysWhenNoConfigs(t *testing.T) {
	m := subFocusMenu(t, "claude", false)
	m.SetFocus(FocusAI)
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusAI {
		t.Errorf("Up from AI (no configs) = %v, want FocusAI", m.Focus())
	}
}

// Subscriptions are shared across agents, so the PLAN row is a reachable focus
// stop for non-Claude agents too (when a keyed config exists).
func TestSubFocus_upFromAIReachesSubscriptionNonClaude(t *testing.T) {
	m := subFocusMenu(t, "opencode", true)
	m.SetFocus(FocusAI)
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusSubscription {
		t.Errorf("Up from AI (opencode) = %v, want FocusSubscription", m.Focus())
	}
}

// The PLAN/subscription row renders on every tab, so it stays a reachable focus
// stop on Settings and Stats — navigating the ring there must land on it, never
// skip past it.
func TestSubFocus_reachableOnNonProjectTabs(t *testing.T) {
	for _, tab := range []MenuTab{TabSettings, TabStats} {
		m := subFocusMenu(t, "claude", true)
		m.SetActiveTab(tab)
		if !m.subscriptionFocusable() {
			t.Errorf("tab %v: subscription should be focusable now its row renders on every tab", tab)
		}

		// Up from AI must stop on the subscription row.
		m.SetFocus(FocusAI)
		m.Update(tea.KeyMsg{Type: tea.KeyUp})
		if m.Focus() != FocusSubscription {
			t.Errorf("tab %v: Up from AI = %v, want FocusSubscription", tab, m.Focus())
		}

		// Down from the subscription row must return to the AI switcher.
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
		if m.Focus() != FocusAI {
			t.Errorf("tab %v: Down from subscription = %v, want FocusAI", tab, m.Focus())
		}
	}
}

func TestSubFocus_downFromSubscriptionGoesToAI(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	m.SetFocus(FocusSubscription)
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.Focus() != FocusAI {
		t.Errorf("Down from subscription = %v, want FocusAI", m.Focus())
	}
}

// The subscription row sits two stops above the tab bar, behind the AI switcher.
func TestSubFocus_upFromTabsReachesSubscriptionViaAI(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	m.SetFocus(FocusTabs)
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusAI {
		t.Fatalf("Up from tabs = %v, want FocusAI", m.Focus())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusSubscription {
		t.Errorf("Up from AI = %v, want FocusSubscription", m.Focus())
	}
}

// With no account row above it, the subscription row is the top of the ring.
func TestSubFocus_upFromSubscriptionStaysPut(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	m.SetFocus(FocusSubscription)
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusSubscription {
		t.Errorf("Up from subscription = %v, want FocusSubscription (top stop)", m.Focus())
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
	if !strings.Contains(row, iconChevronLeft) || !strings.Contains(row, iconChevronRight) {
		t.Errorf("subscription row missing cycle chevrons when focusable:\n%s", row)
	}
}

func TestSubscriptionRow_noChevronsWhenNoConfigs(t *testing.T) {
	m := subFocusMenu(t, "claude", false)
	row := stripAnsi(m.renderSubscriptionRow("│", "│"))
	if strings.Contains(row, iconChevronLeft) || strings.Contains(row, iconChevronRight) {
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
	m := NewMainMenu(projects, []string{"claude", "opencode"}, "claude", "animated")
	m.SetSize(100, 40)
	dir := t.TempDir()
	writeKeylessConfig(t, dir, "nokey.json")
	m.SetClaudeConfigPaths(filepath.Join(dir, "list"), dir)
	m.SetClaudeConfigs([]ClaudeConfig{{Name: "NoKey", File: "nokey.json"}})

	m.SetFocus(FocusAI)
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusAI {
		t.Errorf("Up from AI with only a keyless config = %v, want FocusAI (not focusable)", m.Focus())
	}
}

// The main-page cycle skips keyless configs and only visits Standard + keyed.
func TestMainSub_cycleSkipsKeylessConfig(t *testing.T) {
	projects := []models.Project{{Name: "a", Path: "/a"}}
	m := NewMainMenu(projects, []string{"claude", "opencode"}, "claude", "animated")
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
	m := NewMainMenu(projects, []string{"claude", "opencode"}, "claude", "animated")
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
