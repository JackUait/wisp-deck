package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func aKey() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}} }

func TestAbout_opensOnAInSettings(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	m.focus = FocusBody

	m.Update(aKey())

	if !m.aboutOpen {
		t.Fatalf("pressing 'a' on the Settings tab should open About")
	}
	if m.inputMode != "" {
		t.Errorf("pressing 'a' on Settings should not enter add-project mode, got inputMode=%q", m.inputMode)
	}

	view := m.View()
	for _, want := range []string{"Made by Evgeniy Pyatkov (@jackuait)", "@that_ai_guy"} {
		if !strings.Contains(view, want) {
			t.Errorf("About panel missing %q:\n%s", want, view)
		}
	}
}

func TestAbout_aOnProjectsStillAddsProject(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabProjects)
	m.focus = FocusBody

	m.Update(aKey())

	if m.aboutOpen {
		t.Errorf("'a' on the Projects tab must not open About")
	}
	if m.inputMode != "add-project" {
		t.Errorf("'a' on Projects should enter add-project, got inputMode=%q", m.inputMode)
	}
}

func TestAbout_escCloses(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	m.focus = FocusBody
	m.Update(aKey())
	if !m.aboutOpen {
		t.Fatalf("precondition: About should be open")
	}

	m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if m.aboutOpen {
		t.Errorf("Esc should close the About panel")
	}
}

// openAboutSized opens the About card on a size-initialized model, so View()
// composites the full-screen overlay (width/height come from WindowSizeMsg in
// the real app).
func openAboutSized(t *testing.T, w, h int) *MainMenuModel {
	t.Helper()
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	m.focus = FocusBody
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m.Update(aKey())
	if !m.aboutOpen {
		t.Fatalf("precondition: About should be open")
	}
	return m
}

// The About card must render as a centered overlay modal over the dimmed
// menu — like the account-switch popup — not as a panel appended below the box.
func TestAbout_rendersAsCenteredOverlayModal(t *testing.T) {
	m := openAboutSized(t, 100, 40)

	view := stripAnsi(m.View())
	lines := strings.Split(view, "\n")
	if len(lines) != 40 {
		t.Fatalf("overlay view must fill the terminal exactly: got %d lines, want 40", len(lines))
	}

	titleLine := -1
	for i, l := range lines {
		if strings.Contains(l, "╭─ About Wisp Deck ─") {
			titleLine = i
			break
		}
	}
	if titleLine < 0 {
		t.Fatalf("card title must be embedded in the top border (╭─ About Wisp Deck ─…):\n%s", view)
	}
	// Centered: the card's border must not hug the terminal's left edge or top.
	if indent := len(lines[titleLine]) - len(strings.TrimLeft(lines[titleLine], " ")); indent < 5 {
		t.Errorf("card should be horizontally centered, top border indent = %d:\n%s", indent, view)
	}
	if titleLine < 3 {
		t.Errorf("card should be vertically centered, top border at line %d:\n%s", titleLine, view)
	}

	// The menu shows through dimmed behind the card.
	if !strings.Contains(view, "Settings") {
		t.Errorf("dimmed menu backdrop should show through behind the card:\n%s", view)
	}
	if !strings.Contains(view, "Esc close") {
		t.Errorf("card footer missing 'Esc close':\n%s", view)
	}
}

func TestAbout_clickOutsideCardCloses(t *testing.T) {
	m := openAboutSized(t, 100, 40)

	m.Update(tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})

	if m.aboutOpen {
		t.Errorf("a click outside the card must close the About overlay")
	}
}

func TestAbout_clickInsideCardKeepsOpen(t *testing.T) {
	m := openAboutSized(t, 100, 40)

	left, top, w, h := m.aboutCardLayout()
	m.Update(tea.MouseMsg{X: left + w/2, Y: top + h/2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})

	if !m.aboutOpen {
		t.Errorf("a click on the card itself must not close the About overlay")
	}
}

// While the About overlay is open, clicks must not fall through to the menu
// underneath (e.g. toggling a settings row behind the modal).
func TestAbout_clickDoesNotFallThroughToMenu(t *testing.T) {
	m := openAboutSized(t, 100, 40)

	before := m.Result()
	m.Update(tea.MouseMsg{X: m.menuOriginX + 2, Y: m.menuOriginY + 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})

	if m.Result() != before {
		t.Errorf("click under the overlay must not activate menu targets")
	}
}

func TestAbout_showsAppVersion(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetAppVersion("2.23.0")
	m.SetActiveTab(TabSettings)
	m.focus = FocusBody
	m.Update(aKey())

	if view := m.View(); !strings.Contains(view, "Version 2.23.0") {
		t.Errorf("About panel missing the app version:\n%s", view)
	}
}

func TestAbout_omitsVersionLineWhenUnset(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	m.focus = FocusBody
	m.Update(aKey())

	if view := m.View(); strings.Contains(view, "Version") {
		t.Errorf("About panel should not render a Version line without a version:\n%s", view)
	}
}
