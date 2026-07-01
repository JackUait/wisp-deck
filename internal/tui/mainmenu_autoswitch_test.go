package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettings_showsAccountSwitchingRow(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsForTest()
	if !strings.Contains(out, "Account switching") {
		t.Fatalf("settings panel should show an Account switching row:\n%s", out)
	}
}

func TestSettings_accountSwitchingReflectsFlagFile(t *testing.T) {
	dir := t.TempDir()
	flag := filepath.Join(dir, "auto-switch-accounts")
	if err := os.WriteFile(flag, []byte("on\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetAutoSwitchFile(flag)
	if !m.AutoSwitchEnabled() {
		t.Error("expected auto-switch enabled from flag file")
	}
	m.SetActiveTab(TabSettings)
	if !strings.Contains(m.renderSettingsForTest(), "[On]") {
		t.Error("row should render [On] when the flag is on")
	}
}

func TestSettings_accountSwitchingToggleTogglesAndPersists(t *testing.T) {
	dir := t.TempDir()
	flag := filepath.Join(dir, "auto-switch-accounts")
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetAutoSwitchFile(flag) // defaults off (no file)
	if m.AutoSwitchEnabled() {
		t.Fatal("should default off")
	}

	m.SetActiveTab(TabSettings)
	m.settingsSelected = m.autoSwitchRowIndex()
	if _, _ = m.settingsEnter(); !m.AutoSwitchEnabled() {
		t.Fatal("enter on the row should turn it on")
	}
	// Persisted to the shared flag file (read by wrapper.sh / lib/auto-switch.sh).
	data, err := os.ReadFile(flag)
	if err != nil || strings.TrimSpace(string(data)) != "on" {
		t.Fatalf("flag file = %q err=%v, want on", string(data), err)
	}
	// Toggling again turns it back off.
	if _, _ = m.settingsEnter(); m.AutoSwitchEnabled() {
		t.Fatal("second enter should turn it off")
	}
	data, _ = os.ReadFile(flag)
	if strings.TrimSpace(string(data)) != "off" {
		t.Errorf("flag file = %q, want off", string(data))
	}
}

func TestSettings_accountSwitchingClickToggles(t *testing.T) {
	dir := t.TempDir()
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetAutoSwitchFile(filepath.Join(dir, "auto-switch-accounts"))
	m.SetActiveTab(TabSettings)
	// A click on the Account-switching row toggles it (not the Login action).
	if _, _ = m.clickSettings(m.autoSwitchRowIndex()); !m.AutoSwitchEnabled() {
		t.Error("clicking the Account switching row should toggle it on")
	}
}

func TestSettings_accountSwitchingArrowKeysToggle(t *testing.T) {
	dir := t.TempDir()
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetAutoSwitchFile(filepath.Join(dir, "auto-switch-accounts"))
	m.SetActiveTab(TabSettings)
	m.settingsSelected = m.autoSwitchRowIndex()
	m.settingsValueRight()
	if !m.AutoSwitchEnabled() {
		t.Error("right arrow should toggle the row on")
	}
	m.settingsValueLeft()
	if m.AutoSwitchEnabled() {
		t.Error("left arrow should toggle the row off")
	}
}
