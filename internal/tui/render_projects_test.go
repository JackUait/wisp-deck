package tui

import (
	"strings"
	"testing"

	"github.com/jackuait/wisp-deck/internal/models"
)

func TestActionBarFor_byRowType(t *testing.T) {
	cases := []struct {
		rowType string
		want    []string
	}{
		// W is always meaningful on a project row: it toggles existing worktrees
		// or expands straight to the add-worktree row when there are none.
		{"project", []string{"Open", "Worktrees", "Delete"}},
		{"worktree", []string{"Open", "Delete"}},
		// Leading glyph doubles as the keymap: Enter triggers add-project, so the
		// action bar must show ⏎ like the other rows (not a bare "+").
		{"add-project", []string{"⏎", "Add project"}},
	}
	for _, c := range cases {
		got := actionBarFor(c.rowType)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("actionBarFor(%q)=%q missing %q", c.rowType, got, w)
			}
		}
	}
	if actionBarFor("action") != "" {
		t.Errorf("actionBarFor(action) should be empty")
	}
}

func TestRenderMenuBox_hasTabBarAndAddRow(t *testing.T) {
	projects := []models.Project{{Name: "blok", Path: "/tmp/blok"}}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "none")
	out := m.renderMenuBox()
	if !strings.Contains(out, "Projects") {
		t.Errorf("menu box missing tab bar")
	}
	if !strings.Contains(out, "Add project") {
		t.Errorf("menu box missing + Add project row")
	}
	// The old action stack labels must be gone from the projects body.
	if strings.Contains(out, "Plain terminal") || strings.Contains(out, "Open once") {
		t.Errorf("old action rows should not render in projects body: %q", out)
	}
}

func TestAddProjectRow_isSelectable(t *testing.T) {
	projects := []models.Project{{Name: "blok", Path: "/tmp/blok"}}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "none")
	last := m.TotalItems() - 1
	itemType, _, _ := m.ResolveItem(last)
	if itemType != "add-project" {
		t.Errorf("last item type = %q, want add-project", itemType)
	}
}

// TestMapRowToItem_matchesRenderedLayout verifies that click-row → item mapping
// stays in sync with the redesigned projects body: an extra tab-bar row near
// the top, no action rows, and the add-project row mapped to its flat index.
func TestRenderMenuBox_emptyState(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	out := m.renderMenuBox()
	if !strings.Contains(out, "No projects yet") {
		t.Errorf("empty state missing prompt: %q", out)
	}
	if !strings.Contains(out, "press A to add") {
		t.Errorf("empty state missing 'press A to add' suffix: %q", out)
	}
	if !strings.Contains(out, "Add project") {
		t.Errorf("empty state should still offer add row")
	}
}

func TestCalculateLayout_accountsForTabBar(t *testing.T) {
	projects := []models.Project{{Name: "a", Path: "/tmp/a"}}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "none")
	layout := m.CalculateLayout(120, 40)
	// Rendered line count for 1 project = 14 (box 13 + help 1), including the
	// add-project hint subtitle row (no PLAN row). MenuHeight must equal that.
	if layout.MenuHeight != 14 {
		t.Errorf("MenuHeight = %d, want 14 (must match rendered lines)", layout.MenuHeight)
	}
}

func TestCalculateLayout_emptyStateAddsRow(t *testing.T) {
	// 0 projects: renderMenuBox emits empty-state row plus the add-project hint
	// subtitle → 13 total lines (no PLAN row).
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	layout := m.CalculateLayout(120, 40)
	if layout.MenuHeight != 13 {
		t.Errorf("MenuHeight (0 proj) = %d, want 13", layout.MenuHeight)
	}
}

func TestMapRowToItem_matchesRenderedLayout(t *testing.T) {
	projects := []models.Project{
		{Name: "alpha", Path: "/tmp/a"},
		{Name: "beta", Path: "/tmp/b"},
	}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.width = 100
	m.height = 60

	// Layout (see render_projects.go), PLAN row removed: top(0) title(1) switcher-gap(2)
	// tabbar(3) sep(4) blank(5) alpha-name(6) alpha-path(7) beta-name(8) beta-path(9)
	// blank(10) add-project(11) add-hint(12) sep(13) actionbar(14) bottom(15) help(16)
	cases := map[int]int{
		0:  -1, // top border
		1:  -1, // title row
		2:  -1, // switcher gap
		3:  -1, // tab bar
		4:  -1, // separator
		5:  -1, // blank spacer
		6:  0,  // alpha name
		7:  0,  // alpha path
		8:  1,  // beta name
		9:  1,  // beta path
		10: -1, // blank spacer before add-project
		11: 2,  // add-project label row (TotalItems-1)
		12: 2,  // add-project hint subtitle row
		13: -1, // separator
		14: -1, // action bar
		15: -1, // bottom border
	}
	for clickY, want := range cases {
		if got := m.MapRowToItem(clickY); got != want {
			t.Errorf("MapRowToItem(%d) = %d, want %d", clickY, got, want)
		}
	}

	// The add-project row must map to the final selectable index.
	addRow := m.MapRowToItem(11)
	if addRow != m.TotalItems()-1 {
		t.Errorf("add-project row = %d, want TotalItems-1=%d", addRow, m.TotalItems()-1)
	}
}
