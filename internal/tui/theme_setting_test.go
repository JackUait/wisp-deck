package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newSettingsMenuForTheme(t *testing.T) (*MainMenuModel, string) {
	t.Helper()
	dir := t.TempDir()
	sf := filepath.Join(dir, "settings")
	m := NewMainMenu(nil, []string{"claude", "opencode"}, "claude", "none")
	m.SetSettingsFile(sf)
	m.SetActiveTab(TabSettings)
	return m, sf
}

func TestThemeSetting_defaultsToAuto(t *testing.T) {
	m, _ := newSettingsMenuForTheme(t)
	if m.themePref != "auto" {
		t.Errorf("default themePref should be auto, got %q", m.themePref)
	}
	// Auto on claude resolves to the orange palette.
	if m.theme.Primary != themes["claude"].Primary {
		t.Errorf("auto/claude should be orange, got %v", m.theme.Primary)
	}
}

func TestThemeSetting_cyclesPresetsAndPersists(t *testing.T) {
	m, sf := newSettingsMenuForTheme(t)
	m.CycleTheme()
	if m.themePref != ThemePresets[1] {
		t.Errorf("after one cycle pref = %q, want %q", m.themePref, ThemePresets[1])
	}
	for m.themePref != "green" {
		m.CycleTheme()
	}
	if m.theme.Name != "green" {
		t.Errorf("live theme should be green, got %q", m.theme.Name)
	}
	data, _ := os.ReadFile(sf)
	if !strings.Contains(string(data), "theme=green") {
		t.Errorf("settings file should contain theme=green, got:\n%s", data)
	}
}

func TestThemeSetting_reverseCycles(t *testing.T) {
	m, _ := newSettingsMenuForTheme(t)
	m.CycleThemeReverse() // auto → last preset
	if m.themePref != ThemePresets[len(ThemePresets)-1] {
		t.Errorf("reverse from auto should wrap to last preset, got %q", m.themePref)
	}
}

func TestThemeSetting_autoFollowsToolSwitch(t *testing.T) {
	m, _ := newSettingsMenuForTheme(t)
	if m.theme.Primary != themes["claude"].Primary {
		t.Fatalf("auto/claude should be orange, got %v", m.theme.Primary)
	}
	m.CycleAITool("next") // → opencode
	if m.theme.Primary != themes["opencode"].Primary {
		t.Errorf("auto theme should follow tool switch to opencode, got %v", m.theme.Primary)
	}
	// A fixed preset ignores the tool.
	for m.themePref != "blue" {
		m.CycleTheme()
	}
	m.CycleAITool("next")
	if m.theme.Name != "blue" {
		t.Errorf("fixed preset should ignore tool switch, got %q", m.theme.Name)
	}
}

func TestThemeSetting_renderShowsThemeRow(t *testing.T) {
	m, _ := newSettingsMenuForTheme(t)
	out := stripAnsi(m.renderSettingsBox())
	if !strings.Contains(out, "Theme") {
		t.Errorf("settings box should show a Theme row:\n%s", out)
	}
	if !strings.Contains(out, "Auto") {
		t.Errorf("default theme state should read [Auto]:\n%s", out)
	}
	for m.themePref != "rose" {
		m.CycleTheme()
	}
	out = stripAnsi(m.renderSettingsBox())
	if !strings.Contains(out, "Rose") {
		t.Errorf("after cycling to rose the row should read [Rose]:\n%s", out)
	}
}

func TestThemeSetting_valueRightAtThemeRowCycles(t *testing.T) {
	m, _ := newSettingsMenuForTheme(t)
	m.settingsSelected = 3 // Theme row
	m.settingsValueRight()
	if m.themePref != ThemePresets[1] {
		t.Errorf("→ on the Theme row should cycle the theme, got %q", m.themePref)
	}
}

func TestThemeSetting_setThemePrefResolves(t *testing.T) {
	m := NewMainMenu(nil, []string{"opencode"}, "opencode", "none")
	m.SetThemePref("cyan")
	if m.theme.Name != "cyan" {
		t.Errorf("SetThemePref(cyan) should set cyan theme, got %q", m.theme.Name)
	}
	m.SetThemePref("auto")
	if m.theme.Name != "opencode" {
		t.Errorf("SetThemePref(auto) on opencode should follow tool, got %q", m.theme.Name)
	}
}
