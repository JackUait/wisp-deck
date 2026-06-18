package tui

import (
	"strings"
	"testing"
)

func TestRenderTabBar_showsAllTabs(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	_, _, _, lb, rb := m.boxBorders()
	bar := m.renderTabBar(lb, rb)
	for _, label := range []string{"Projects", "Settings", "Stats"} {
		if !strings.Contains(bar, label) {
			t.Errorf("tab bar missing %q: %q", label, bar)
		}
	}
}

func TestRenderTabBar_activeTabAccented(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	_, _, _, lb, rb := m.boxBorders()
	bar := m.renderTabBar(lb, rb)
	// The active tab is wrapped with the block accent glyph U+258C.
	if !strings.Contains(bar, "▌") {
		t.Errorf("active tab bar missing block accent: %q", bar)
	}
}
