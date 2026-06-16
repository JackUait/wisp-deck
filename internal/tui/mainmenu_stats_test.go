package tui

import (
	"testing"
)

func TestMainMenu_TKeyPushesStatsScreen(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "") // *MainMenuModel
	updated, cmd := m.handleRune('t')
	_ = updated
	if cmd == nil {
		t.Fatal("'t' returned nil cmd, want PushScreenMsg{StatsModel}")
	}
	push, ok := cmd().(PushScreenMsg)
	if !ok {
		t.Fatalf("'t' cmd = %T, want PushScreenMsg", cmd())
	}
	if _, ok := push.Model.(StatsModel); !ok {
		t.Errorf("pushed model = %T, want StatsModel", push.Model)
	}
}

func TestMainMenu_TKeyPassesWindowSizeToStats(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "")
	m.SetSize(120, 50)
	_, cmd := m.handleRune('t')
	sm, ok := cmd().(PushScreenMsg).Model.(StatsModel)
	if !ok {
		t.Fatalf("pushed model is not StatsModel")
	}
	if sm.width != 120 || sm.height != 50 {
		t.Errorf("pushed stats size = %dx%d, want 120x50 (so it centers immediately)", sm.width, sm.height)
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
