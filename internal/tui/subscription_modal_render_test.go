package tui

import (
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
