package tui

import (
	"strings"
	"testing"

	"github.com/jackuait/ghost-tab/internal/models"
)

func TestActionBarFor_byRowType(t *testing.T) {
	cases := []struct {
		rowType      string
		hasWorktrees bool
		want         []string
		notWant      []string
	}{
		// W Worktrees only appears when the project actually has worktrees.
		{"project", true, []string{"Open", "Worktrees", "Delete"}, nil},
		{"project", false, []string{"Open", "Delete"}, []string{"Worktrees"}},
		{"worktree", false, []string{"Open", "Delete"}, nil},
		// Leading glyph doubles as the keymap: Enter triggers add-project, so the
		// action bar must show ⏎ like the other rows (not a bare "+").
		{"add-project", false, []string{"⏎", "Add project"}, nil},
	}
	for _, c := range cases {
		got := actionBarFor(c.rowType, c.hasWorktrees)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("actionBarFor(%q, %v)=%q missing %q", c.rowType, c.hasWorktrees, got, w)
			}
		}
		for _, nw := range c.notWant {
			if strings.Contains(got, nw) {
				t.Errorf("actionBarFor(%q, %v)=%q should not contain %q", c.rowType, c.hasWorktrees, got, nw)
			}
		}
	}
	if actionBarFor("action", false) != "" {
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
	// The subscription row is shared across agents, so it is present for opencode too.
	m := NewMainMenu(projects, []string{"opencode"}, "opencode", "none")
	layout := m.CalculateLayout(120, 40)
	// Rendered line count for 1 project = 15 (box 14 + help 1), including the
	// subscription row and the add-project hint subtitle row. MenuHeight must equal that.
	if layout.MenuHeight != 15 {
		t.Errorf("MenuHeight = %d, want 15 (must match rendered lines)", layout.MenuHeight)
	}
}

func TestCalculateLayout_emptyStateAddsRow(t *testing.T) {
	// 0 projects: renderMenuBox emits empty-state row plus the add-project hint
	// subtitle → 14 total lines, including the shared subscription row.
	m := NewMainMenu(nil, []string{"opencode"}, "opencode", "none")
	layout := m.CalculateLayout(120, 40)
	if layout.MenuHeight != 14 {
		t.Errorf("MenuHeight (0 proj) = %d, want 14", layout.MenuHeight)
	}
}

func TestMapRowToItem_matchesRenderedLayout(t *testing.T) {
	projects := []models.Project{
		{Name: "alpha", Path: "/tmp/a"},
		{Name: "beta", Path: "/tmp/b"},
	}
	// The subscription row is shared across agents, present for opencode too.
	m := NewMainMenu(projects, []string{"opencode"}, "opencode", "none")
	m.width = 100
	m.height = 60

	// Layout (see render_projects.go): top(0) title(1) subscription(2) switcher-gap(3)
	// tabbar(4) sep(5) blank(6) alpha-name(7) alpha-path(8) beta-name(9) beta-path(10)
	// blank(11) add-project(12) add-hint(13) sep(14) actionbar(15) bottom(16) help(17)
	cases := map[int]int{
		0:  -1, // top border
		2:  -1, // subscription row
		3:  -1, // switcher gap
		4:  -1, // tab bar
		5:  -1, // separator
		6:  -1, // blank spacer
		7:  0,  // alpha name
		8:  0,  // alpha path
		9:  1,  // beta name
		10: 1,  // beta path
		11: -1, // blank spacer before add-project
		12: 2,  // add-project label row (TotalItems-1)
		13: 2,  // add-project hint subtitle row
		14: -1, // separator
		15: -1, // action bar
		16: -1, // bottom border
	}
	for clickY, want := range cases {
		if got := m.MapRowToItem(clickY); got != want {
			t.Errorf("MapRowToItem(%d) = %d, want %d", clickY, got, want)
		}
	}

	// The add-project row must map to the final selectable index.
	addRow := m.MapRowToItem(12)
	if addRow != m.TotalItems()-1 {
		t.Errorf("add-project row = %d, want TotalItems-1=%d", addRow, m.TotalItems()-1)
	}
}
