package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newSettingsMenuForTabBar(t *testing.T) (*MainMenuModel, string) {
	t.Helper()
	dir := t.TempDir()
	sf := filepath.Join(dir, "settings")
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetSettingsFile(sf)
	m.SetActiveTab(TabSettings)
	return m, sf
}

// Large is the mode a fresh install comes up in: the bar that names each tab
// and reports what it is doing. The bash side agrees (tab_view_mode), so a
// session whose settings file predates the row is not left on the old bar.
func TestTabBarSetting_defaultsToLarge(t *testing.T) {
	m, _ := newSettingsMenuForTabBar(t)
	if m.TabBar() != "large" {
		t.Errorf("default tab bar = %q, want large", m.TabBar())
	}
}

func TestTabBarSetting_rejectsAnUnknownMode(t *testing.T) {
	m, _ := newSettingsMenuForTabBar(t)
	m.SetTabBar("enormous")
	if m.TabBar() != "large" {
		t.Errorf("unknown mode = %q, want large", m.TabBar())
	}
}

func TestTabBarSetting_cyclesAndPersists(t *testing.T) {
	m, sf := newSettingsMenuForTabBar(t)
	m.CycleTabBar()
	if m.TabBar() != "compact" {
		t.Fatalf("after one cycle = %q, want compact", m.TabBar())
	}
	data, _ := os.ReadFile(sf)
	if !strings.Contains(string(data), "tab_bar=compact") {
		t.Fatalf("settings file should hold tab_bar=compact, got:\n%s", data)
	}
	m.CycleTabBar()
	if m.TabBar() != "large" {
		t.Fatalf("cycling back = %q, want large", m.TabBar())
	}
}

func TestTabBarSetting_reverseCycles(t *testing.T) {
	m, _ := newSettingsMenuForTabBar(t)
	m.CycleTabBarReverse()
	if m.TabBar() != "compact" {
		t.Errorf("reverse from large = %q, want compact", m.TabBar())
	}
}

func TestTabBarLabel(t *testing.T) {
	for mode, want := range map[string]string{
		"large":   "Large",
		"compact": "Compact",
		"":        "Large",
	} {
		if got := tabBarLabel(mode); got != want {
			t.Errorf("tabBarLabel(%q) = %q, want %q", mode, got, want)
		}
	}
}

// The row is part of Appearance and reachable by keyboard and mouse: an index
// missing from the section grouping is rendered nowhere and cannot be selected.
func TestTabBarSetting_isAnAppearanceRow(t *testing.T) {
	m, _ := newSettingsMenuForTabBar(t)
	var found bool
	for _, s := range m.settingsSections() {
		if s.title != "Appearance" {
			continue
		}
		for _, idx := range s.indices {
			if idx == rowTabBar {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("rowTabBar missing from the Appearance section: %v", m.settingsSections())
	}
	if m.settingsVisualPos(rowTabBar) < 0 {
		t.Fatalf("rowTabBar is not in the navigable order")
	}
}

func TestTabBarSetting_rendersItsRow(t *testing.T) {
	m, _ := newSettingsMenuForTabBar(t)
	out := m.renderSettingsBox()
	if !strings.Contains(out, "Tab bar") {
		t.Fatalf("settings box has no Tab bar row:\n%s", out)
	}
	if !strings.Contains(out, "[Large]") {
		t.Fatalf("settings box does not show the Large state:\n%s", out)
	}
}

// ↵ and the arrow keys all act on the row under the cursor; a handler that
// never learned the new index would toggle whatever row shares its number.
func TestTabBarSetting_respondsToTheRowHandlers(t *testing.T) {
	m, _ := newSettingsMenuForTabBar(t)
	m.settingsSelected = rowTabBar
	m.settingsEnter()
	if m.TabBar() != "compact" {
		t.Fatalf("enter on the row = %q, want compact", m.TabBar())
	}
	m.settingsValueRight()
	if m.TabBar() != "large" {
		t.Fatalf("right on the row = %q, want large", m.TabBar())
	}
	m.settingsValueLeft()
	if m.TabBar() != "compact" {
		t.Fatalf("left on the row = %q, want compact", m.TabBar())
	}
}
