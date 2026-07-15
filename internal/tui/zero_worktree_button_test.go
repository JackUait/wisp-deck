package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/models"
)

// zeroWtMenu builds a rendered menu with two projects: "alpha" (2 worktrees)
// at flat index 0 and "gamma" (no worktrees) at flat index 1. View() is called
// so menuOriginX/Y and menuLines are populated for mouse tests.
func zeroWtMenu(t *testing.T) *MainMenuModel {
	t.Helper()
	projects := []models.Project{
		{
			Name: "alpha",
			Path: "/tmp/alpha",
			Worktrees: []models.Worktree{
				{Path: "/tmp/alpha--feat", Branch: "feat/x"},
				{Path: "/tmp/alpha--fix", Branch: "fix/y"},
			},
		},
		{Name: "gamma", Path: "/tmp/gamma"},
	}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.width = 100
	m.height = 60
	_ = m.View()
	return m
}

// nameRowOf returns the box-relative row of the project's name line (the first
// of its two rows).
func nameRowOf(t *testing.T, m *MainMenuModel, flatIdx int) int {
	t.Helper()
	for boxY := 0; boxY < m.height; boxY++ {
		if m.MapRowToItem(boxY) == flatIdx && m.MapRowToItem(boxY-1) != flatIdx {
			return boxY
		}
	}
	t.Fatalf("could not locate name row for flat index %d", flatIdx)
	return -1
}

// plainRow returns the ANSI-stripped rendered line at the given box row.
func plainRow(t *testing.T, m *MainMenuModel, boxY int) string {
	t.Helper()
	_ = m.View()
	if boxY < 0 || boxY >= len(m.menuLines) {
		t.Fatalf("row %d outside rendered frame of %d lines", boxY, len(m.menuLines))
	}
	return diffAnsiSeq.ReplaceAllString(m.menuLines[boxY], "")
}

// ---------------------------------------------------------------------------
// W key / ToggleWorktrees on a zero-worktree project
// ---------------------------------------------------------------------------

func TestToggleWorktrees_zeroWorktrees_expandsAndFocusesAddRow(t *testing.T) {
	m := zeroWtMenu(t)
	m.selectedItem = 1 // gamma

	m.ToggleWorktreesAtCursor()

	if !m.IsExpanded(1) {
		t.Fatal("expected zero-worktree project to expand")
	}
	itemType, projectIdx, _ := m.ResolveItem(m.selectedItem)
	if itemType != "add-worktree" || projectIdx != 1 {
		t.Errorf("expected cursor on gamma's add-worktree row, got %q project %d", itemType, projectIdx)
	}
}

func TestToggleWorktrees_zeroWorktrees_toggleCollapsesBackToProject(t *testing.T) {
	m := zeroWtMenu(t)
	m.selectedItem = 1
	m.ToggleWorktreesAtCursor() // expand + focus add row
	m.ToggleWorktreesAtCursor() // W from the add row collapses the parent

	if m.IsExpanded(1) {
		t.Fatal("expected second toggle to collapse the project")
	}
	itemType, projectIdx, _ := m.ResolveItem(m.selectedItem)
	if itemType != "project" || projectIdx != 1 {
		t.Errorf("expected cursor back on the gamma project row, got %q project %d", itemType, projectIdx)
	}
}

// ---------------------------------------------------------------------------
// Rendering: the "+ Add worktree" button in the badge slot
// ---------------------------------------------------------------------------

func TestZeroWorktreeButton_visibleOnFocusedRow(t *testing.T) {
	m := zeroWtMenu(t)
	m.selectedItem = 1 // gamma focused, body focus is the default

	row := plainRow(t, m, nameRowOf(t, m, 1))
	if !strings.Contains(row, zeroWorktreeButtonLabel) {
		t.Errorf("focused zero-worktree row should show %q, got %q", zeroWorktreeButtonLabel, row)
	}
}

func TestZeroWorktreeButton_hiddenWhenNotFocused(t *testing.T) {
	m := zeroWtMenu(t)
	m.selectedItem = 0 // cursor elsewhere, no hover

	row := plainRow(t, m, nameRowOf(t, m, 1))
	if strings.Contains(row, zeroWorktreeButtonLabel) {
		t.Errorf("unfocused zero-worktree row should not show the button, got %q", row)
	}
}

func TestZeroWorktreeButton_hiddenWhenBodyNotFocused(t *testing.T) {
	m := zeroWtMenu(t)
	m.selectedItem = 1
	m.focus = FocusAI // selected but body focus is elsewhere

	row := plainRow(t, m, nameRowOf(t, m, 1))
	if strings.Contains(row, zeroWorktreeButtonLabel) {
		t.Errorf("off-focus selected row should not show the button, got %q", row)
	}
}

func TestZeroWorktreeButton_visibleOnHoveredRow(t *testing.T) {
	m := zeroWtMenu(t)
	m.selectedItem = 0 // cursor on alpha; gamma is only hovered
	nameRow := nameRowOf(t, m, 1)

	over := tea.MouseMsg{X: m.menuOriginX + 9, Y: m.menuOriginY + nameRow, Action: tea.MouseActionMotion}
	upd, _ := m.Update(over)
	mm := upd.(*MainMenuModel)

	row := plainRow(t, mm, nameRow)
	if !strings.Contains(row, zeroWorktreeButtonLabel) {
		t.Errorf("hovered zero-worktree row should show %q, got %q", zeroWorktreeButtonLabel, row)
	}
}

func TestZeroWorktreeButton_hiddenOnProjectWithWorktrees(t *testing.T) {
	m := zeroWtMenu(t)
	m.selectedItem = 0 // alpha focused

	row := plainRow(t, m, nameRowOf(t, m, 0))
	if strings.Contains(row, zeroWorktreeButtonLabel) {
		t.Errorf("project with worktrees should keep its badge, not the button, got %q", row)
	}
	if !strings.Contains(row, "2 worktrees") {
		t.Errorf("expected the worktree-count badge on alpha, got %q", row)
	}
}

func TestZeroWorktreeButton_hiddenWhenExpanded(t *testing.T) {
	m := zeroWtMenu(t)
	m.selectedItem = 1
	m.ToggleWorktreesAtCursor() // expand gamma; cursor lands on the add row

	// The project name row itself must drop the button once expanded…
	nameRow := nameRowOf(t, m, 1)
	row := plainRow(t, m, nameRow)
	if strings.Contains(row, zeroWorktreeButtonLabel) {
		t.Errorf("expanded project row should not show the button, got %q", row)
	}
	// …because the add-worktree row below is now the affordance.
	addRow := plainRow(t, m, nameRow+2)
	if !strings.Contains(addRow, "+ Add worktree") {
		t.Errorf("expected the add-worktree row below the expanded project, got %q", addRow)
	}
}

func TestZeroWorktreeButton_hiddenInDeleteMode(t *testing.T) {
	m := zeroWtMenu(t)
	m.selectedItem = 1
	m.enterDeleteMode()

	row := plainRow(t, m, nameRowOf(t, m, 1))
	if strings.Contains(row, zeroWorktreeButtonLabel) {
		t.Errorf("delete mode should not show the add-worktree button, got %q", row)
	}
}

// TestProjectRow_badgeNeverOverflowsRowWidth guards the badge-slot truncation:
// a long project name must shrink to make room for the right-aligned badge
// ("N worktrees" or the add-worktree button) instead of overflowing the box.
func TestProjectRow_badgeNeverOverflowsRowWidth(t *testing.T) {
	long := strings.Repeat("x", 80)
	projects := []models.Project{
		{Name: "with-wt-" + long, Path: "/tmp/a", Worktrees: []models.Worktree{{Path: "/tmp/a--f", Branch: "f"}}},
		{Name: "zero-wt-" + long, Path: "/tmp/b"},
	}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.width = 100
	m.height = 60
	_ = m.View()

	for flat := 0; flat < 2; flat++ {
		m.selectedItem = flat // focused: badge/button rendered
		row := plainRow(t, m, nameRowOf(t, m, flat))
		if w := len([]rune(row)); w != menuBoxWidth {
			t.Errorf("focused row %d should be exactly %d cells wide, got %d: %q", flat, menuBoxWidth, w, row)
		}
	}

	// Hovered-but-unselected rows render the badge through the other branch.
	m.selectedItem = 0
	nameRow := nameRowOf(t, m, 1)
	m.applyHover(hitTarget{region: regionBody, index: 1})
	row := plainRow(t, m, nameRow)
	if w := len([]rune(row)); w != menuBoxWidth {
		t.Errorf("hovered row should be exactly %d cells wide, got %d: %q", menuBoxWidth, w, row)
	}
}

// ---------------------------------------------------------------------------
// Mouse: clicking the button expands + focuses the add row
// ---------------------------------------------------------------------------

func TestZeroWorktreeButton_clickExpandsAndFocusesAddRow(t *testing.T) {
	m := zeroWtMenu(t)
	m.selectedItem = 0 // cursor elsewhere; the pointer does the work
	nameRow := nameRowOf(t, m, 1)

	// Hover the row so the button renders.
	over := tea.MouseMsg{X: m.menuOriginX + 9, Y: m.menuOriginY + nameRow, Action: tea.MouseActionMotion}
	upd, _ := m.Update(over)
	m = upd.(*MainMenuModel)

	// Locate the button's column in the rendered frame.
	row := plainRow(t, m, nameRow)
	col := strings.Index(row, zeroWorktreeButtonLabel)
	if col < 0 {
		t.Fatalf("button not rendered on hovered row: %q", row)
	}

	click := tea.MouseMsg{
		X: m.menuOriginX + col + 2, Y: m.menuOriginY + nameRow,
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	}
	upd, _ = m.Update(click)
	m = upd.(*MainMenuModel)

	if !m.IsExpanded(1) {
		t.Fatal("clicking the button should expand the project")
	}
	itemType, projectIdx, _ := m.ResolveItem(m.selectedItem)
	if itemType != "add-worktree" || projectIdx != 1 {
		t.Errorf("expected cursor on gamma's add-worktree row, got %q project %d", itemType, projectIdx)
	}
}

func TestZeroWorktreeButton_clickOnNameStillSelects(t *testing.T) {
	m := zeroWtMenu(t)
	m.selectedItem = 0
	nameRow := nameRowOf(t, m, 1)

	click := tea.MouseMsg{
		X: m.menuOriginX + 9, Y: m.menuOriginY + nameRow,
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	}
	upd, _ := m.Update(click)
	m = upd.(*MainMenuModel)

	if m.IsExpanded(1) {
		t.Error("clicking the project name should not expand worktrees")
	}
	if m.selectedItem != 1 {
		t.Errorf("clicking the project name should select it, cursor at %d", m.selectedItem)
	}
}
