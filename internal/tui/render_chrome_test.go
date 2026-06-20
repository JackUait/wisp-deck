package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestRenderTabBar_showsAllTabs(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	_, _, _, lb, rb := m.boxBorders()
	bar := m.renderTabBar(lb, rb)
	for _, label := range []string{"Projects", "Settings", "Stats"} {
		if !strings.Contains(bar, label) {
			t.Errorf("tab bar missing %q: %q", label, bar)
		}
	}
}

func TestRenderTabBar_activeTabAccented(t *testing.T) {
	// Force a real color profile so lipgloss emits styling escapes; otherwise the
	// label/padding distinction is invisible to string matching.
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	_, _, _, lb, rb := m.boxBorders()
	bar := m.renderTabBar(lb, rb)
	// The active (body-focused) tab is wrapped in bold accent brackets.
	want := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true).Render("[Settings]")
	if !strings.Contains(bar, want) {
		t.Errorf("active tab bar missing bracketed bold accent: %q", bar)
	}
	// Inactive tabs are not bracketed.
	if strings.Contains(stripAnsi(bar), "[Projects]") || strings.Contains(stripAnsi(bar), "[Stats]") {
		t.Errorf("inactive tabs should not be bracketed: %q", bar)
	}
}
