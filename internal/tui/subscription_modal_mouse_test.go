package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

func subscriptionCardCell(t *testing.T, m *MainMenuModel, text string) (x, y int) {
	t.Helper()
	for row, styled := range strings.Split(m.renderSubscriptionModalCard(), "\n") {
		line := stripAnsi(styled)
		if idx := strings.Index(line, text); idx >= 0 {
			return lipgloss.Width(line[:idx]), row
		}
	}
	t.Fatalf("card has no %q:\n%s", text, stripAnsi(m.renderSubscriptionModalCard()))
	return 0, 0
}

func subscriptionScreenMouse(m *MainMenuModel, cardX, cardY int, action tea.MouseAction, button tea.MouseButton) tea.MouseMsg {
	left, top, _, _ := m.subscriptionModalLayout()
	return tea.MouseMsg{
		X:      left + cardX,
		Y:      top + cardY,
		Action: action,
		Button: button,
	}
}

func TestSubscriptionModalHit_profilesMappingsAndActions(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.SetActiveClaudeConfig("openai-gpt.json")
	m.openSubscriptionModal()

	tests := []struct {
		text  string
		kind  subscriptionHitKind
		index int
	}{
		{"Xiaomi MiMo", subscriptionHitProfile, 2},
		{"+ Add profile", subscriptionHitAdd, 0},
		{"Opus", subscriptionHitMapping, 0},
		{"[ Use profile ]", subscriptionHitUse, 0},
		{"[ Rename ]", subscriptionHitRename, 0},
		{"[ Delete ]", subscriptionHitDelete, 0},
		{"[ Save changes ]", subscriptionHitSave, 0},
	}
	for _, test := range tests {
		x, y := subscriptionCardCell(t, m, test.text)
		target := m.subscriptionModalTarget(x, y)
		if target.kind != test.kind || target.index != test.index {
			t.Errorf("%q target = %+v, want kind %v index %d", test.text, target, test.kind, test.index)
		}
	}
}

func TestSubscriptionModalHit_whitespaceIsNotInteractive(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	_, y := subscriptionCardCell(t, m, "Xiaomi MiMo")
	if target := m.subscriptionModalTarget(18, y); target.kind != subscriptionHitNone {
		t.Fatalf("profile-row trailing whitespace target = %+v", target)
	}
}

func TestSubscriptionModalMouse_hoverDoesNotMoveCursor(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	before := m.subscriptionModal.profileCursor
	x, y := subscriptionCardCell(t, m, "Xiaomi MiMo")

	updated, _ := m.Update(subscriptionScreenMouse(m, x, y, tea.MouseActionMotion, tea.MouseButtonNone))
	got := updated.(*MainMenuModel)

	if got.subscriptionModal.profileCursor != before {
		t.Fatalf("hover moved cursor from %d to %d", before, got.subscriptionModal.profileCursor)
	}
	if got.subscriptionModal.hover.kind != subscriptionHitProfile || got.subscriptionModal.hover.index != 2 {
		t.Fatalf("hover target = %+v, want profile 2", got.subscriptionModal.hover)
	}
}

func TestSubscriptionModalMouse_clickProfilePreviewsWithoutActivating(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	x, y := subscriptionCardCell(t, m, "Xiaomi MiMo")

	updated, _ := m.Update(subscriptionScreenMouse(m, x, y, tea.MouseActionPress, tea.MouseButtonLeft))
	got := updated.(*MainMenuModel)

	if got.subscriptionModal.profileCursor != 2 {
		t.Fatalf("click cursor = %d, want 2", got.subscriptionModal.profileCursor)
	}
	if got.CurrentClaudeConfigFile() != "" {
		t.Fatalf("profile click activated %q without Use", got.CurrentClaudeConfigFile())
	}
}

func TestSubscriptionModalMouse_clickChooseProviderPreview(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(len(m.subscriptionProfiles()))
	x, y := subscriptionCardCell(t, m, "[ Choose provider ]")

	if target := m.subscriptionModalTarget(x, y); target.kind != subscriptionHitAdd {
		t.Fatalf("choose-provider target = %+v, want add", target)
	}
	updated, _ := m.Update(subscriptionScreenMouse(m, x, y, tea.MouseActionPress, tea.MouseButtonLeft))
	got := updated.(*MainMenuModel)
	if got.subscriptionModal.mode != subscriptionAddProvider {
		t.Fatalf("choose-provider click mode = %v, want add provider", got.subscriptionModal.mode)
	}
}

func TestSubscriptionModalMouse_clickMappingCyclesDraft(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.SetActiveClaudeConfig("openai-gpt.json")
	m.openSubscriptionModal()
	before := m.subscriptionModal.draft.mappings[0]
	x, y := subscriptionCardCell(t, m, "Opus")

	updated, _ := m.Update(subscriptionScreenMouse(m, x, y, tea.MouseActionPress, tea.MouseButtonLeft))
	got := updated.(*MainMenuModel)

	if got.subscriptionModal.draft.mappings[0] == before {
		t.Fatalf("mapping click left opus at %d", before)
	}
	if !got.subscriptionModal.draft.dirty {
		t.Fatal("mapping click did not dirty draft")
	}
}

func TestSubscriptionModalMouse_clickFooterBackReturnsToProfiles(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(2)
	m.subscriptionModal.pane = subscriptionDetailsPane
	x, y := subscriptionCardCell(t, m, "← back/previous")

	updated, _ := m.Update(subscriptionScreenMouse(m, x, y, tea.MouseActionPress, tea.MouseButtonLeft))
	got := updated.(*MainMenuModel)

	if got.subscriptionModal.pane != subscriptionProfilesPane {
		t.Fatalf("footer back click pane = %v, want profiles", got.subscriptionModal.pane)
	}
}

func TestSubscriptionModalMouse_outsideClickClosesCleanModal(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()

	updated, _ := m.Update(tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	got := updated.(*MainMenuModel)

	if got.subscriptionModal.open {
		t.Fatal("outside click did not close clean modal")
	}
}

func TestSubscriptionModalMouse_outsideClickProtectsDirtyDraft(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.SetActiveClaudeConfig("openai-gpt.json")
	m.openSubscriptionModal()
	m.subscriptionModal.draft.dirty = true

	updated, _ := m.Update(tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	got := updated.(*MainMenuModel)

	if !got.subscriptionModal.open {
		t.Fatal("outside click discarded dirty modal")
	}
	if got.subscriptionModal.mode != subscriptionDiscardConfirm || !got.subscriptionModal.pendingClose {
		t.Fatalf("dirty outside click state = mode %v pendingClose %v", got.subscriptionModal.mode, got.subscriptionModal.pendingClose)
	}
}

func TestSubscriptionModalHit_duplicateNamesResolveRenderedRow(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	if _, err := claudeconfig.AddForProvider(
		m.claudeConfigsList,
		m.claudeConfigsDir,
		"Xiaomi MiMo",
		"mimo",
	); err != nil {
		t.Fatal(err)
	}
	m.SetClaudeConfigs(LoadClaudeConfigsList(m.claudeConfigsList))
	m.openSubscriptionModal()

	seen := 0
	for y, styled := range strings.Split(m.renderSubscriptionModalCard(), "\n") {
		line := stripAnsi(styled)
		x := strings.Index(line, "Xiaomi MiMo")
		if x < 0 {
			continue
		}
		seen++
		if seen != 2 {
			continue
		}
		target := m.subscriptionModalTarget(lipgloss.Width(line[:x]), y)
		if target.kind != subscriptionHitProfile || target.index != 4 {
			t.Fatalf("second duplicate target = %+v, want profile 4", target)
		}
		return
	}
	t.Fatal("second duplicate profile row not rendered")
}

func TestSubscriptionModalMouse_confirmationButtonsMatchKeyboard(t *testing.T) {
	t.Run("delete", func(t *testing.T) {
		m := newSubscriptionModalMenu(t)
		m.openSubscriptionModal()
		m.moveSubscriptionProfile(2)
		m.startSubscriptionDelete()
		x, y := subscriptionCardCell(t, m, "[ Delete ]")

		updated, _ := m.Update(subscriptionScreenMouse(m, x, y, tea.MouseActionPress, tea.MouseButtonLeft))
		got := updated.(*MainMenuModel)
		if cfg := findSubscriptionConfig(got.claudeConfigs, "Xiaomi MiMo"); cfg.File != "" {
			t.Fatalf("mouse-confirmed delete left config %+v", cfg)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		m := newSubscriptionModalMenu(t)
		m.openSubscriptionModal()
		m.moveSubscriptionProfile(2)
		m.startSubscriptionDelete()
		x, y := subscriptionCardCell(t, m, "[ Cancel ]")

		updated, _ := m.Update(subscriptionScreenMouse(m, x, y, tea.MouseActionPress, tea.MouseButtonLeft))
		got := updated.(*MainMenuModel)
		if got.subscriptionModal.mode != subscriptionBrowse {
			t.Fatalf("mouse cancel mode = %v, want browse", got.subscriptionModal.mode)
		}
		if cfg := findSubscriptionConfig(got.claudeConfigs, "Xiaomi MiMo"); cfg.File == "" {
			t.Fatal("mouse cancel deleted the profile")
		}
	})
}

func TestSubscriptionModalMouse_keyInputConfirmKeepsDraft(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(2)
	m.beginSubscriptionKeyEdit()
	m.subscriptionModal.input.SetValue("sk-mouse")
	x, y := subscriptionCardCell(t, m, "[ Keep changes ]")

	updated, _ := m.Update(subscriptionScreenMouse(m, x, y, tea.MouseActionPress, tea.MouseButtonLeft))
	got := updated.(*MainMenuModel)

	if got.subscriptionModal.mode != subscriptionBrowse {
		t.Fatalf("mouse key confirm mode = %v, want browse", got.subscriptionModal.mode)
	}
	if got.subscriptionModal.draft.apiKey != "sk-mouse" ||
		!got.subscriptionModal.draft.keyEdited ||
		!got.subscriptionModal.draft.dirty {
		t.Fatalf("mouse key confirm draft = %+v", got.subscriptionModal.draft)
	}
	if disk := claudeconfig.ReadAPIKey(got.claudeConfigsDir, "xiaomi-mimo.json"); disk != "sk-test" {
		t.Fatalf("mouse key confirm wrote before Save: %q", disk)
	}
}
