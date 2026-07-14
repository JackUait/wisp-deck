package tui

import (
	"testing"
)

func TestMainMenu_TKeySwitchesToStatsTab(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "") // *MainMenuModel
	_, cmd := m.handleRune('t')
	// 't' should not push a new screen (no PushScreenMsg), but may return a load cmd.
	if cmd != nil {
		msg := cmd()
		if _, ok := msg.(PushScreenMsg); ok {
			t.Errorf("'t' should not emit a PushScreenMsg, got %T", msg)
		}
	}
	if m.ActiveTab() != TabStats {
		t.Errorf("after 't' tab = %v, want TabStats", m.ActiveTab())
	}
}

func TestMainMenu_hasStatsActionLabel(t *testing.T) {
	found := false
	for _, a := range actionLabels {
		if a.shortcut == "T" && a.label == "Stats" {
			found = true
		}
	}
	if !found {
		t.Errorf("actionLabels missing {T, Stats}: %+v", actionLabels)
	}
}

func TestMainMenuInit_startsUsageIngestion(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "static")
	cmd := m.Init()

	if cmd == nil {
		t.Fatal("Init returned nil, want background usage ingestion command")
	}
	if !m.statsLoading {
		t.Fatal("Init should mark stats as loading before the Stats tab is opened")
	}
}
