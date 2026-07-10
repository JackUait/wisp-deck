package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Settings menu exposes a "Keep awake" toggle. When on, a working agent
// holds the kernel sleep veto so a closed lid does not suspend the machine.
// It persists to keep_awake= in the settings file, which the bash watcher reads
// live on every poll tick.

func TestKeepAwake_defaultsOff(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	if m.KeepAwakeEnabled() {
		t.Error("keep-awake must default to off: it takes standing root and defeats the lid switch")
	}
}

func TestCycleKeepAwake_togglesAndPersists(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings")
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSettingsFile(settings)

	m.CycleKeepAwake()
	if !m.KeepAwakeEnabled() {
		t.Fatal("expected keep-awake on after first toggle")
	}
	data, _ := os.ReadFile(settings)
	if !strings.Contains(string(data), "keep_awake=on") {
		t.Fatalf("expected keep_awake=on persisted, got:\n%s", string(data))
	}

	m.CycleKeepAwake()
	if m.KeepAwakeEnabled() {
		t.Fatal("expected keep-awake off after second toggle")
	}
	data, _ = os.ReadFile(settings)
	if !strings.Contains(string(data), "keep_awake=off") {
		t.Fatalf("expected keep_awake=off persisted, got:\n%s", string(data))
	}
}

func TestSetKeepAwake_recordsInitial(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetKeepAwake("on")
	if !m.KeepAwakeEnabled() {
		t.Error("SetKeepAwake(\"on\") did not enable")
	}
	m.SetKeepAwake("garbage")
	if m.KeepAwakeEnabled() {
		t.Error("unrecognized value must read as off, not on")
	}
}

// The row must be reachable by keyboard: present in the visual order, and
// ↵/←/→ on it must hit the toggle rather than a neighbouring row's handler.
func TestKeepAwakeRow_isNavigableAndTogglesOnEnter(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSettingsFile(filepath.Join(t.TempDir(), "settings"))

	found := false
	for _, idx := range m.settingsItemOrder() {
		if idx == rowKeepAwake {
			found = true
		}
	}
	if !found {
		t.Fatal("rowKeepAwake missing from settingsItemOrder — unreachable by keyboard")
	}

	m.settingsSelected = rowKeepAwake
	m.settingsEnter()
	if !m.KeepAwakeEnabled() {
		t.Error("enter on the keep-awake row did not toggle it")
	}
	m.settingsValueRight()
	if m.KeepAwakeEnabled() {
		t.Error("right-arrow on the keep-awake row did not toggle it")
	}
	m.settingsValueLeft()
	if !m.KeepAwakeEnabled() {
		t.Error("left-arrow on the keep-awake row did not toggle it")
	}
}

// Every row index must render exactly one entry; a row added to the constants
// but not to a section silently vanishes from the menu.
func TestKeepAwakeRow_countMatchesOrder(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	if got, want := len(m.settingsItemOrder()), m.settingsItemCount(); got != want {
		t.Errorf("settingsItemOrder has %d rows, settingsItemCount says %d", got, want)
	}
}
