package tui

import (
	"strings"
	"testing"

	"github.com/jackuait/ghost-tab/internal/models"
)

func TestActionBarFor_byRowType(t *testing.T) {
	cases := map[string][]string{
		"project":     {"Open", "Worktrees", "Delete"},
		"worktree":    {"Open", "Delete"},
		"add-project": {"Add project"},
	}
	for rowType, wantWords := range cases {
		got := actionBarFor(rowType)
		for _, w := range wantWords {
			if !strings.Contains(got, w) {
				t.Errorf("actionBarFor(%q)=%q missing %q", rowType, got, w)
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
	// Non-Claude tool: no subscription row, so layout matches the documented rows.
	m := NewMainMenu(projects, []string{"codex"}, "codex", "none")
	layout := m.CalculateLayout(120, 40)
	// Rendered line count for 1 project = 13 (box 12 + help 1).
	// MenuHeight must equal that; old formula (+4 action rows) gives 14.
	if layout.MenuHeight != 13 {
		t.Errorf("MenuHeight = %d, want 13 (must match rendered lines)", layout.MenuHeight)
	}
}

func TestCalculateLayout_emptyStateAddsRow(t *testing.T) {
	// 0 projects: renderMenuBox emits empty-state row → 12 total lines.
	// Non-Claude tool: no subscription row.
	m := NewMainMenu(nil, []string{"codex"}, "codex", "none")
	layout := m.CalculateLayout(120, 40)
	if layout.MenuHeight != 12 {
		t.Errorf("MenuHeight (0 proj) = %d, want 12", layout.MenuHeight)
	}
}

func TestMapRowToItem_matchesRenderedLayout(t *testing.T) {
	projects := []models.Project{
		{Name: "alpha", Path: "/tmp/a"},
		{Name: "beta", Path: "/tmp/b"},
	}
	// Non-Claude tool: no subscription row, so the documented rows hold.
	m := NewMainMenu(projects, []string{"codex"}, "codex", "none")
	m.width = 100
	m.height = 60

	// Layout (see render_projects.go): top(0) title(1) tabbar(2) sep(3)
	// blank(4) alpha-name(5) alpha-path(6) beta-name(7) beta-path(8)
	// blank(9) add-project(10) sep(11) actionbar(12) bottom(13) help(14)
	cases := map[int]int{
		0:  -1, // top border
		2:  -1, // tab bar
		3:  -1, // separator
		4:  -1, // blank spacer
		5:  0,  // alpha name
		6:  0,  // alpha path
		7:  1,  // beta name
		8:  1,  // beta path
		9:  -1, // blank spacer before add-project
		10: 2,  // add-project row (TotalItems-1)
		11: -1, // separator
		12: -1, // action bar
		13: -1, // bottom border
	}
	for clickY, want := range cases {
		if got := m.MapRowToItem(clickY); got != want {
			t.Errorf("MapRowToItem(%d) = %d, want %d", clickY, got, want)
		}
	}

	// The add-project row must map to the final selectable index.
	addRow := m.MapRowToItem(10)
	if addRow != m.TotalItems()-1 {
		t.Errorf("add-project row = %d, want TotalItems-1=%d", addRow, m.TotalItems()-1)
	}
}
