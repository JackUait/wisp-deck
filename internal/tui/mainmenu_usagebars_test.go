package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Settings menu exposes a "Usage bars" row that chooses which statusline
// usage pills show: 7d only, 5h only, both, or none. It cycles and persists the
// choice to usage_bars= in the settings file, mirroring the Side panel / Theme
// rows.

func TestUsageBars_defaultIs7d(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	if m.UsageBars() != "7d" {
		t.Errorf("default usage bars = %q, want 7d", m.UsageBars())
	}
}

func TestCycleUsageBars_wraps(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	want := []string{"5h", "both", "none", "7d"}
	for _, w := range want {
		m.CycleUsageBars()
		if m.UsageBars() != w {
			t.Fatalf("after cycle expected %q, got %q", w, m.UsageBars())
		}
	}
}

func TestCycleUsageBarsReverse_wraps(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	want := []string{"none", "both", "5h", "7d"}
	for _, w := range want {
		m.CycleUsageBarsReverse()
		if m.UsageBars() != w {
			t.Fatalf("after reverse-cycle expected %q, got %q", w, m.UsageBars())
		}
	}
}

func TestCycleUsageBars_persistsToSettingsFile(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings")
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSettingsFile(settings)
	m.CycleUsageBars() // 7d -> 5h
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("settings file not written: %v", err)
	}
	if !strings.Contains(string(data), "usage_bars=5h") {
		t.Fatalf("expected usage_bars=5h persisted, got:\n%s", string(data))
	}
}

func TestSetUsageBars_recordsInitial(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetUsageBars("both")
	if m.UsageBars() != "both" {
		t.Fatalf("SetUsageBars: got %q, want both", m.UsageBars())
	}
}

func TestRenderSettingsBox_hasUsageBarsRow(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsBox()
	if !strings.Contains(out, "Usage bars") {
		t.Fatalf("settings box should show a Usage bars row:\n%s", out)
	}
	if !strings.Contains(out, "[7d]") {
		t.Fatalf("Usage bars row should show its [7d] value:\n%s", out)
	}
}
