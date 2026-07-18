package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestSubscriptionModal_wideRenderShowsInventoryAndDetails(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.SetActiveClaudeConfig("openai-gpt.json")
	m.openSubscriptionModal()

	view := stripAnsi(m.View())
	for _, want := range []string{
		"Subscriptions",
		"PROFILES",
		"Standard Claude",
		"Zhipu GLM",
		"Xiaomi MiMo",
		"OpenAI GPT",
		"Provider",
		"OpenAI / ChatGPT",
		"Authentication",
		"codex login",
		"MODEL ROUTING",
		"Opus",
		"Sonnet",
		"Haiku",
		"Fable",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

func TestSubscriptionModal_overlayDimsSettingsBackdrop(t *testing.T) {
	withTrueColor(t)
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()

	view := m.View()
	if !strings.Contains(view, "\x1b[2;") {
		t.Fatalf("overlay backdrop is not faint-dimmed:\n%q", view)
	}
	lines := strings.Split(stripAnsi(view), "\n")
	if len(lines) != m.height {
		t.Fatalf("overlay height = %d, want terminal height %d", len(lines), m.height)
	}
	left, top, width, _ := m.subscriptionModalLayout()
	if left <= 0 || top <= 0 || width >= m.width {
		t.Fatalf("card geometry = left %d top %d width %d in %dx%d", left, top, width, m.width, m.height)
	}
	if !strings.Contains(lines[top], "Subscriptions") {
		t.Fatalf("card title not placed at computed top %d: %q", top, lines[top])
	}
}

func TestSubscriptionModal_cardLinesMatchGeometry(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	_, _, width, height := m.subscriptionModalLayout()
	lines := strings.Split(m.renderSubscriptionModalCard(), "\n")
	if len(lines) != height {
		t.Fatalf("card height = %d, want %d", len(lines), height)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got != width {
			t.Errorf("line %d width = %d, want %d: %q", i, got, width, stripAnsi(line))
		}
	}
}

func TestSubscriptionModal_activeAndCursorMarkersDiffer(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.SetActiveClaudeConfig("openai-gpt.json")
	m.openSubscriptionModal()
	m.subscriptionModal.profileCursor = 2 // Xiaomi MiMo preview

	card := stripAnsi(m.renderSubscriptionModalCard())
	var activeLine, cursorLine string
	for _, line := range strings.Split(card, "\n") {
		switch {
		case strings.Contains(line, "OpenAI GPT"):
			activeLine = line
		case strings.Contains(line, "Xiaomi MiMo"):
			cursorLine = line
		}
	}
	if !strings.Contains(activeLine, "●") {
		t.Fatalf("active profile has no active marker: %q", activeLine)
	}
	if !strings.Contains(cursorLine, "▌") {
		t.Fatalf("preview profile has no cursor marker: %q", cursorLine)
	}
	if strings.Contains(cursorLine, "●") {
		t.Fatalf("preview marker falsely looks active: %q", cursorLine)
	}
}

func TestSubscriptionModal_compactRenderDrillsIntoDetails(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.width = 60
	m.openSubscriptionModal()

	list := stripAnsi(m.renderSubscriptionModalCard())
	if !strings.Contains(list, "PROFILES") {
		t.Fatalf("compact list missing profile heading:\n%s", list)
	}
	if strings.Contains(list, "MODEL ROUTING") {
		t.Fatalf("compact list rendered details simultaneously:\n%s", list)
	}

	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRight})
	details := stripAnsi(m.renderSubscriptionModalCard())
	if !strings.Contains(details, "PROFILE DETAILS") {
		t.Fatalf("Right did not drill into compact details:\n%s", details)
	}
	if strings.Contains(details, "PROFILES") {
		t.Fatalf("compact details still rendered list pane:\n%s", details)
	}
}

func TestSubscriptionModal_shortTerminalKeepsTitleAndFooter(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.width, m.height = 80, 14
	m.openSubscriptionModal()

	view := stripAnsi(m.View())
	lines := strings.Split(view, "\n")
	if len(lines) != 14 {
		t.Fatalf("view height = %d, want 14", len(lines))
	}
	if !strings.Contains(view, "Subscriptions") {
		t.Fatalf("short view lost title:\n%s", view)
	}
	if !strings.Contains(view, "Esc close") {
		t.Fatalf("short view lost fixed footer:\n%s", view)
	}
}

func TestSubscriptionModal_keyEditorIsVisibleAndMasked(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(2) // Xiaomi MiMo
	m.subscriptionModal.pane = subscriptionDetailsPane
	m.beginSubscriptionKeyEdit()

	card := stripAnsi(m.renderSubscriptionModalCard())
	if !strings.Contains(card, "EDIT API KEY") {
		t.Fatalf("key editor title is missing:\n%s", card)
	}
	if !strings.Contains(card, "••••") {
		t.Fatalf("key editor does not show masked input:\n%s", card)
	}
	if strings.Contains(card, "sk-test") {
		t.Fatalf("key editor exposed the API key:\n%s", card)
	}
}

func TestSubscriptionModal_discardConfirmationIsVisible(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(2)
	m.subscriptionModal.draft.dirty = true

	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	card := stripAnsi(m.renderSubscriptionModalCard())
	if !strings.Contains(card, "DISCARD UNSAVED CHANGES?") {
		t.Fatalf("discard confirmation is missing:\n%s", card)
	}
	if !strings.Contains(card, "[ Discard ]") {
		t.Fatalf("discard confirmation lacks its action hint:\n%s", card)
	}
}

func TestSubscriptionModal_detailCursorIsVisible(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(2)
	m.subscriptionModal.pane = subscriptionDetailsPane
	m.subscriptionModal.detailCursor = subscriptionDetailOpus

	card := stripAnsi(m.renderSubscriptionModalCard())
	for _, line := range strings.Split(card, "\n") {
		if strings.Contains(line, "Opus") {
			if !strings.Contains(line, "▌") {
				t.Fatalf("focused detail row has no cursor marker: %q", line)
			}
			return
		}
	}
	t.Fatalf("Opus row is missing:\n%s", card)
}

func TestSubscriptionModal_wideActionsShareOneLine(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(2)
	m.subscriptionModal.pane = subscriptionDetailsPane

	card := stripAnsi(m.renderSubscriptionModalCard())
	for _, line := range strings.Split(card, "\n") {
		if !strings.Contains(line, "[ Use profile ]") {
			continue
		}
		for _, want := range []string{"[ Rename ]", "[ Delete ]", "[ Save changes ]"} {
			if !strings.Contains(line, want) {
				t.Fatalf("wide action row is missing %q: %q", want, line)
			}
		}
		return
	}
	t.Fatalf("wide action row is missing:\n%s", card)
}

func TestSubscriptionModal_wideInlineSaveScrollTargetsSharedActionRow(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.width = 100
	m.height = 12
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(2)
	m.subscriptionModal.pane = subscriptionDetailsPane
	m.subscriptionModal.detailCursor = subscriptionDetailSave

	m.ensureSubscriptionDetailVisible()

	if got, want := m.subscriptionModal.detailOffset, 11; got != want {
		t.Fatalf("detail offset = %d, want %d for inline action row", got, want)
	}
}

func TestSubscriptionModal_narrowCardNeverExceedsGeometry(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.width = 40
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(2)
	m.subscriptionModal.pane = subscriptionDetailsPane

	_, _, width, _ := m.subscriptionModalLayout()
	for i, line := range strings.Split(m.renderSubscriptionModalCard(), "\n") {
		if got := lipgloss.Width(line); got != width {
			t.Errorf("narrow line %d width = %d, want %d: %q", i, got, width, stripAnsi(line))
		}
	}
}

func TestSubscriptionModal_ultraNarrowTitleFitsGeometry(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.width = 20
	m.openSubscriptionModal()

	_, _, width, _ := m.subscriptionModalLayout()
	for i, line := range strings.Split(m.renderSubscriptionModalCard(), "\n") {
		if got := lipgloss.Width(line); got != width {
			t.Errorf("ultra-narrow line %d width = %d, want %d: %q", i, got, width, stripAnsi(line))
		}
	}
}

func TestSubscriptionModal_wideFooterFollowsDetailsPane(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.subscriptionModal.pane = subscriptionDetailsPane

	card := stripAnsi(m.renderSubscriptionModalCard())
	if !strings.Contains(card, "↑↓ setting") {
		t.Fatalf("wide details footer describes profile navigation:\n%s", card)
	}
}

func TestSubscriptionModal_wideProfilesFooterAdvertisesRightDetails(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()

	card := stripAnsi(m.renderSubscriptionModalCard())
	if !strings.Contains(card, "→ details") {
		t.Fatalf("wide profiles footer does not advertise Right navigation:\n%s", card)
	}
}

func TestSubscriptionModal_displaysConfiguredEndpoint(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	path := filepath.Join(m.claudeConfigsDir, "xiaomi-mimo.json")
	if err := os.WriteFile(path, []byte(`{
  "env": {
    "WISP_DECK_SUBSCRIPTION_PROVIDER": "mimo",
    "ANTHROPIC_BASE_URL": "http://localhost:4312",
    "ANTHROPIC_AUTH_TOKEN": "sk-test"
  }
}
`), 0600); err != nil {
		t.Fatal(err)
	}
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(2)

	card := stripAnsi(m.renderSubscriptionModalCard())
	if !strings.Contains(card, "http://localhost:4312") {
		t.Fatalf("details do not show configured endpoint:\n%s", card)
	}
}

func TestSubscriptionModal_compactFooterDescribesCurrentPane(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.width = 60
	m.openSubscriptionModal()
	m.subscriptionModal.pane = subscriptionDetailsPane

	card := stripAnsi(m.renderSubscriptionModalCard())
	if !strings.Contains(card, "Esc profiles") {
		t.Fatalf("compact details footer describes the wrong Esc action:\n%s", card)
	}
}

func TestSubscriptionModal_compactScrollKeepsSaveActionVisible(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.width, m.height = 60, 12
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(2)
	m.subscriptionModal.pane = subscriptionDetailsPane
	m.subscriptionModal.detailCursor = subscriptionDetailSave
	m.ensureSubscriptionDetailVisible()

	card := stripAnsi(m.renderSubscriptionModalCard())
	if !strings.Contains(card, "[ Save changes ]") {
		t.Fatalf("compact detail scroll hid selected Save action:\n%s", card)
	}
}
