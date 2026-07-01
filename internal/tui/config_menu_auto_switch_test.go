package tui

import "testing"

// The config menu exposes an "Auto-switch Claude accounts" toggle whose Status
// reflects the current setting so the user can see and flip it.

func findItem(items []ConfigMenuItem, action string) (ConfigMenuItem, bool) {
	for _, it := range items {
		if it.Action == action {
			return it, true
		}
	}
	return ConfigMenuItem{}, false
}

func TestConfigMenu_hasAutoSwitchToggle(t *testing.T) {
	m := NewConfigMenu(ConfigMenuOptions{AutoSwitch: "on"})
	it, ok := findItem(m.items, "toggle-auto-switch")
	if !ok {
		t.Fatal("expected a toggle-auto-switch item")
	}
	if it.Status != "On" {
		t.Errorf("Status = %q, want On", it.Status)
	}
}

func TestConfigMenu_autoSwitchOffStatus(t *testing.T) {
	m := NewConfigMenu(ConfigMenuOptions{AutoSwitch: "off"})
	it, _ := findItem(m.items, "toggle-auto-switch")
	if it.Status != "Off" {
		t.Errorf("Status = %q, want Off", it.Status)
	}
}

func TestConfigMenu_autoSwitchDefaultsOff(t *testing.T) {
	m := NewConfigMenu(ConfigMenuOptions{})
	it, ok := findItem(m.items, "toggle-auto-switch")
	if !ok {
		t.Fatal("expected a toggle-auto-switch item")
	}
	if it.Status != "Off" {
		t.Errorf("Status = %q, want Off when unset", it.Status)
	}
}
