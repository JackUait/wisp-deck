package tui

import (
	"testing"

	"github.com/jackuait/ghost-tab/internal/models"
)

// newWorktreeMenu creates a MainMenuModel with two projects each having worktrees,
// plus a third project with no worktrees, for cursor/expand testing.
//
// Flat layout when all collapsed:
//
//	0: project "alpha"   (2 worktrees)
//	1: project "beta"    (1 worktree)
//	2: project "gamma"   (no worktrees)
//	3: add-project row
func newWorktreeMenu() *MainMenuModel {
	projects := []models.Project{
		{
			Name: "alpha",
			Path: "/tmp/alpha",
			Worktrees: []models.Worktree{
				{Path: "/tmp/alpha--feat", Branch: "feat/x"},
				{Path: "/tmp/alpha--fix", Branch: "fix/y"},
			},
		},
		{
			Name: "beta",
			Path: "/tmp/beta",
			Worktrees: []models.Worktree{
				{Path: "/tmp/beta--main", Branch: "main"},
			},
		},
		{
			Name:      "gamma",
			Path:      "/tmp/gamma",
			Worktrees: nil,
		},
	}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.width = 100
	m.height = 40
	return m
}

// ---------------------------------------------------------------------------
// ToggleAllWorktrees — cursor must stay on the same logical item after expand
// ---------------------------------------------------------------------------

// TestToggleAllWorktrees_ExpandPreservesCursorOnProject verifies that when the
// cursor is on a project row and all worktrees expand, the cursor stays on
// that same project row (not on a row inserted above it).
func TestToggleAllWorktrees_ExpandPreservesCursorOnProject(t *testing.T) {
	m := newWorktreeMenu()

	// Cursor on project "beta" (flat index 1 when all collapsed)
	m.selectedItem = 1
	m.ToggleAllWorktrees()

	// After expanding, "alpha" gains 3 rows (2 worktrees + add-worktree) above beta.
	// Without fix: cursor would still be 1 → now points at first worktree of alpha.
	// With fix:    cursor should move to beta's new flat index = 0+1 + 3 = 4.
	itemType, projectIdx, _ := m.ResolveItem(m.selectedItem)
	if itemType != "project" {
		t.Errorf("expected cursor on a project row, got itemType=%q at flat index %d", itemType, m.selectedItem)
	}
	if projectIdx != 1 {
		t.Errorf("expected cursor on project index 1 (beta), got projectIdx=%d", projectIdx)
	}
}

// TestToggleAllWorktrees_ExpandPreservesCursorOnAddProject verifies that when
// the cursor is on the add-project row, expanding worktrees keeps the cursor
// on that same row.
func TestToggleAllWorktrees_ExpandPreservesCursorOnAddProject(t *testing.T) {
	m := newWorktreeMenu()

	// Cursor on the add-project row (flat index 3 when collapsed: 3 projects = 0,1,2 → add-project at 3)
	m.selectedItem = 3
	m.ToggleAllWorktrees()

	// After expanding both alpha (3 sub-rows) and beta (2 sub-rows), total shift = 5.
	// Without anchoring: cursor at 3 → points into alpha's worktree rows.
	// With anchoring:    cursor should still be on the add-project row.
	itemType, _, _ := m.ResolveItem(m.selectedItem)
	if itemType != "add-project" {
		t.Errorf("expected cursor on add-project row, got itemType=%q at flat index %d", itemType, m.selectedItem)
	}
}

// TestToggleAllWorktrees_ExpandPreservesCursorOnFirstProject verifies that the
// cursor stays on project "alpha" (index 0) when it was already there before
// expanding. Alpha is at flat index 0 both before and after, so no adjustment
// is needed — but the anchor logic must not break this case.
func TestToggleAllWorktrees_ExpandPreservesCursorOnFirstProject(t *testing.T) {
	m := newWorktreeMenu()

	m.selectedItem = 0 // project alpha
	m.ToggleAllWorktrees()

	itemType, projectIdx, _ := m.ResolveItem(m.selectedItem)
	if itemType != "project" {
		t.Errorf("expected cursor on project row, got itemType=%q", itemType)
	}
	if projectIdx != 0 {
		t.Errorf("expected project index 0 (alpha), got %d", projectIdx)
	}
}

// ---------------------------------------------------------------------------
// ToggleAllWorktrees — cursor must stay on the same logical item after collapse
// ---------------------------------------------------------------------------

// TestToggleAllWorktrees_CollapsePreservesCursorOnProject verifies that when
// all worktrees are expanded and the cursor is on a project row, collapsing
// keeps the cursor on that project.
func TestToggleAllWorktrees_CollapsePreservesCursorOnProject(t *testing.T) {
	m := newWorktreeMenu()

	// Expand all first
	m.ToggleAllWorktrees()

	// Move cursor to project "beta".
	// After expand: alpha(0), wt1(1), wt2(2), add-wt(3), beta(4), ...
	betaFlatIdx := m.projectToFlatIndex(1)
	m.selectedItem = betaFlatIdx

	// Now collapse all
	m.ToggleAllWorktrees()

	itemType, projectIdx, _ := m.ResolveItem(m.selectedItem)
	if itemType != "project" {
		t.Errorf("expected cursor on project row after collapse, got itemType=%q at flat index %d", itemType, m.selectedItem)
	}
	if projectIdx != 1 {
		t.Errorf("expected project index 1 (beta), got %d", projectIdx)
	}
}

// TestToggleAllWorktrees_CollapsePreservesCursorOnAddProject verifies that when
// all worktrees are expanded and the cursor is on the add-project row,
// collapsing keeps the cursor on that row.
func TestToggleAllWorktrees_CollapsePreservesCursorOnAddProject(t *testing.T) {
	m := newWorktreeMenu()

	// Expand all first
	m.ToggleAllWorktrees()

	// Move cursor to the add-project row (the final selectable item).
	total := m.TotalItems()
	var addProjectFlat int
	for i := 0; i < total; i++ {
		iType, _, _ := m.ResolveItem(i)
		if iType == "add-project" {
			addProjectFlat = i
			break
		}
	}
	m.selectedItem = addProjectFlat

	// Collapse all
	m.ToggleAllWorktrees()

	itemType, _, _ := m.ResolveItem(m.selectedItem)
	if itemType != "add-project" {
		t.Errorf("expected cursor on add-project row after collapse, got itemType=%q at flat index %d", itemType, m.selectedItem)
	}
}

// TestToggleAllWorktrees_CollapseSnapsWorktreeCursorToProject verifies that
// when cursor is on a worktree row and all worktrees collapse, the cursor
// snaps to the parent project row.
func TestToggleAllWorktrees_CollapseSnapsWorktreeCursorToProject(t *testing.T) {
	m := newWorktreeMenu()

	// Expand all
	m.ToggleAllWorktrees()

	// Put cursor on the first worktree of "alpha" (flat index 1 after expand)
	m.selectedItem = 1 // alpha's first worktree
	itemType, _, _ := m.ResolveItem(m.selectedItem)
	if itemType != "worktree" {
		t.Fatalf("setup: expected worktree at flat index 1, got %q", itemType)
	}

	// Collapse all
	m.ToggleAllWorktrees()

	// Cursor should be snapped to project "alpha" (flat index 0)
	itemType, projectIdx, _ := m.ResolveItem(m.selectedItem)
	if itemType != "project" {
		t.Errorf("expected cursor snapped to project row, got itemType=%q at flat index %d", itemType, m.selectedItem)
	}
	if projectIdx != 0 {
		t.Errorf("expected project index 0 (alpha), got %d", projectIdx)
	}
}

// ---------------------------------------------------------------------------
// ToggleWorktrees (per-project) — cursor must be preserved on expand
// ---------------------------------------------------------------------------

// TestToggleWorktrees_ExpandPreservesCursorOnLaterProject verifies that
// when a project above the cursor expands, the cursor stays on the same
// logical item (project "beta" in this case).
func TestToggleWorktrees_ExpandPreservesCursorOnLaterProject(t *testing.T) {
	m := newWorktreeMenu()

	// Cursor on project "beta" (flat index 1)
	m.selectedItem = 1

	// Expand project "alpha" (index 0) — inserts 3 rows above beta
	m.ToggleWorktrees(0)

	itemType, projectIdx, _ := m.ResolveItem(m.selectedItem)
	if itemType != "project" {
		t.Errorf("expected cursor on project row, got itemType=%q at flat index %d", itemType, m.selectedItem)
	}
	if projectIdx != 1 {
		t.Errorf("expected project index 1 (beta), got %d", projectIdx)
	}
}

// ---------------------------------------------------------------------------
// w key — cursor-scoped expand: only toggles the project under the cursor
// ---------------------------------------------------------------------------

// TestWKey_TogglesOnlyCursorProject verifies that pressing 'w' when the cursor
// is on project "beta" expands only "beta", not "alpha".
func TestWKey_TogglesOnlyCursorProject(t *testing.T) {
	m := newWorktreeMenu()

	// Cursor on project "beta" (flat index 1)
	m.selectedItem = 1

	m.ToggleWorktreesAtCursor()

	if m.IsExpanded(0) {
		t.Error("expected project alpha (index 0) to remain collapsed")
	}
	if !m.IsExpanded(1) {
		t.Error("expected project beta (index 1) to be expanded")
	}
}

// TestWKey_TogglesOnlyAlphaWhenCursorOnAlpha verifies that pressing 'w' on
// "alpha" expands only "alpha".
func TestWKey_TogglesOnlyAlphaWhenCursorOnAlpha(t *testing.T) {
	m := newWorktreeMenu()

	// Cursor on project "alpha" (flat index 0)
	m.selectedItem = 0

	m.ToggleWorktreesAtCursor()

	if !m.IsExpanded(0) {
		t.Error("expected project alpha (index 0) to be expanded")
	}
	if m.IsExpanded(1) {
		t.Error("expected project beta (index 1) to remain collapsed")
	}
}

// TestWKey_CollapsesExpandedProjectAtCursor verifies that pressing 'w' again
// on an already-expanded project collapses it.
func TestWKey_CollapsesExpandedProjectAtCursor(t *testing.T) {
	m := newWorktreeMenu()

	// Expand alpha via ToggleWorktrees
	m.expandedWorktrees[0] = true
	// Cursor on project alpha
	m.selectedItem = 0

	m.ToggleWorktreesAtCursor()

	if m.IsExpanded(0) {
		t.Error("expected project alpha to be collapsed after second toggle")
	}
}

// TestWKey_NoOpOnProjectWithNoWorktrees verifies that pressing 'w' when the
// cursor is on a project without worktrees is a no-op (no expand, no crash).
func TestWKey_NoOpOnProjectWithNoWorktrees(t *testing.T) {
	m := newWorktreeMenu()

	// Cursor on project "gamma" (flat index 2, no worktrees)
	m.selectedItem = 2

	m.ToggleWorktreesAtCursor()

	if m.IsExpanded(2) {
		t.Error("expected project gamma (no worktrees) to remain unexpanded")
	}
}

// TestWKey_NoOpOnAddProjectRow verifies that pressing 'w' when the cursor is on
// the add-project row is a no-op.
func TestWKey_NoOpOnAddProjectRow(t *testing.T) {
	m := newWorktreeMenu()

	// Cursor on the add-project row (flat index 3)
	m.selectedItem = 3
	itemType, _, _ := m.ResolveItem(3)
	if itemType != "add-project" {
		t.Fatalf("setup: expected add-project at flat index 3, got %q", itemType)
	}

	m.ToggleWorktreesAtCursor()

	// Nothing should be expanded
	if m.IsExpanded(0) || m.IsExpanded(1) || m.IsExpanded(2) {
		t.Error("expected no projects to be expanded after w on action row")
	}
}

// TestWKey_CursorStaysOnProjectAfterExpand verifies that after pressing 'w'
// to expand the cursor's project, the cursor still points at that project row.
func TestWKey_CursorStaysOnProjectAfterExpand(t *testing.T) {
	m := newWorktreeMenu()

	// Cursor on project "beta" (flat index 1)
	m.selectedItem = 1

	m.ToggleWorktreesAtCursor()

	// After expanding beta, its flat index hasn't changed (nothing above it changed)
	itemType, projectIdx, _ := m.ResolveItem(m.selectedItem)
	if itemType != "project" {
		t.Errorf("expected cursor still on project row, got itemType=%q", itemType)
	}
	if projectIdx != 1 {
		t.Errorf("expected cursor on project beta (index 1), got %d", projectIdx)
	}
}

// TestWKey_CursorStaysOnWorktreeProjectAfterExpand verifies that pressing 'w'
// on a project that is already expanded (cursor on a worktree row belonging to
// that project) collapses it and snaps cursor to the project row.
func TestWKey_CursorOnWorktreeRow_CollapsesAndSnaps(t *testing.T) {
	m := newWorktreeMenu()

	// Expand alpha first
	m.expandedWorktrees[0] = true
	// Cursor on the first worktree of alpha (flat index 1)
	m.selectedItem = 1
	itemType, _, _ := m.ResolveItem(1)
	if itemType != "worktree" {
		t.Fatalf("setup: expected worktree at index 1, got %q", itemType)
	}

	m.ToggleWorktreesAtCursor()

	// Should have collapsed alpha and snapped cursor to project row
	if m.IsExpanded(0) {
		t.Error("expected alpha to be collapsed")
	}
	itemType, projectIdx, _ := m.ResolveItem(m.selectedItem)
	if itemType != "project" {
		t.Errorf("expected cursor on project row after collapse, got %q at flat %d", itemType, m.selectedItem)
	}
	if projectIdx != 0 {
		t.Errorf("expected cursor on alpha (index 0), got %d", projectIdx)
	}
}

// TestWKey_CursorOnAddWorktreeRow_Collapses verifies that pressing 'w' when
// the cursor is on the "add-worktree" item of an expanded project collapses
// that project.
func TestWKey_CursorOnAddWorktreeRow_Collapses(t *testing.T) {
	m := newWorktreeMenu()

	// Expand alpha (2 worktrees → add-worktree at flat index 3)
	m.expandedWorktrees[0] = true
	// add-worktree for alpha is at flat index 3 (0=alpha, 1=wt1, 2=wt2, 3=add-wt)
	m.selectedItem = 3
	itemType, _, _ := m.ResolveItem(3)
	if itemType != "add-worktree" {
		t.Fatalf("setup: expected add-worktree at flat index 3, got %q", itemType)
	}

	m.ToggleWorktreesAtCursor()

	if m.IsExpanded(0) {
		t.Error("expected alpha to be collapsed after w on add-worktree row")
	}
}
