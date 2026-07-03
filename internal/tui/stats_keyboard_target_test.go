package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// statsBodyModel returns a MainMenuModel focused on the Stats tab body with data
// loaded, ready to receive arrow-key input.
func statsBodyModel(t *testing.T) *MainMenuModel {
	t.Helper()
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabStats)
	m.SetSize(120, 40)
	m.SetFocus(FocusBody)
	updated, _ := m.Update(statsLoadedMsg{months: statsMonthWithModels()})
	return updated.(*MainMenuModel)
}

// TestStatsMode_arrowKeysTargetMode verifies the user can TARGET a specific mode
// with the keyboard: → always selects Compact, ← always selects Full — unlike 'c'
// which blindly flips. This gives the toggle a keyboard focus target like clicking.
func TestStatsMode_arrowKeysTargetMode(t *testing.T) {
	mm := statsBodyModel(t)
	if mm.statsCompact {
		t.Fatal("should start in full mode")
	}

	// → targets Compact.
	updated, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRight})
	mm = updated.(*MainMenuModel)
	if !mm.statsCompact {
		t.Errorf("→ should target Compact mode")
	}

	// → again stays Compact (targeting, not toggling).
	updated, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRight})
	mm = updated.(*MainMenuModel)
	if !mm.statsCompact {
		t.Errorf("→ again should stay in Compact mode (target, not toggle)")
	}

	// ← targets Full.
	updated, _ = mm.Update(tea.KeyMsg{Type: tea.KeyLeft})
	mm = updated.(*MainMenuModel)
	if mm.statsCompact {
		t.Errorf("← should target Full mode")
	}

	// ← again stays Full.
	updated, _ = mm.Update(tea.KeyMsg{Type: tea.KeyLeft})
	mm = updated.(*MainMenuModel)
	if mm.statsCompact {
		t.Errorf("← again should stay in Full mode (target, not toggle)")
	}
}
