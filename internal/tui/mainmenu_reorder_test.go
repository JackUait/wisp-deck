package tui

import (
	"path/filepath"
	"testing"

	"github.com/jackuait/ghost-tab/internal/models"
)

// newReorderMenu creates a MainMenuModel with three projects and no worktrees,
// backed by a real temp file so persist operations succeed.
//
// Flat layout (all collapsed, no worktrees):
//
//	0: project "alpha"
//	1: project "beta"
//	2: project "gamma"
//	3: add-project row
func newReorderMenu(t *testing.T) *MainMenuModel {
	t.Helper()
	dir := t.TempDir()
	projectsFile := filepath.Join(dir, "projects")

	projects := []models.Project{
		{Name: "alpha", Path: "/tmp/alpha"},
		{Name: "beta", Path: "/tmp/beta"},
		{Name: "gamma", Path: "/tmp/gamma"},
	}

	// Pre-populate the file so RewriteProjectsFile can rename into it.
	if err := RewriteProjectsFile(projects, projectsFile); err != nil {
		t.Fatalf("setup: failed to write initial projects file: %v", err)
	}

	m := NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.projectsFile = projectsFile
	return m
}

// ---------------------------------------------------------------------------
// MoveProjectUp
// ---------------------------------------------------------------------------

// TestMoveProjectUp_movesProjectAndCursor verifies that moving the project at
// index 1 up places it at index 0 and the cursor follows it.
func TestMoveProjectUp_movesProjectAndCursor(t *testing.T) {
	m := newReorderMenu(t)
	m.selectedItem = 1 // project "beta" (index 1)

	m.MoveProjectUp()

	// beta should now be first
	if m.projects[0].Name != "beta" {
		t.Errorf("expected projects[0]=%q, got %q", "beta", m.projects[0].Name)
	}
	if m.projects[1].Name != "alpha" {
		t.Errorf("expected projects[1]=%q, got %q", "alpha", m.projects[1].Name)
	}

	// cursor must follow the moved project
	itemType, projectIdx, _ := m.ResolveItem(m.selectedItem)
	if itemType != "project" {
		t.Errorf("expected cursor on project row, got itemType=%q", itemType)
	}
	if projectIdx != 0 {
		t.Errorf("expected cursor on project index 0 (beta after move), got %d", projectIdx)
	}
}

// TestMoveProjectUp_noopAtFirstProject verifies that calling MoveProjectUp
// when the cursor is already on the first project does nothing.
func TestMoveProjectUp_noopAtFirstProject(t *testing.T) {
	m := newReorderMenu(t)
	m.selectedItem = 0 // project "alpha" (index 0, already first)

	m.MoveProjectUp()

	if m.projects[0].Name != "alpha" {
		t.Errorf("expected projects[0] to remain %q, got %q", "alpha", m.projects[0].Name)
	}
	if m.selectedItem != 0 {
		t.Errorf("expected selectedItem to remain 0, got %d", m.selectedItem)
	}
}

// TestMoveProjectUp_swapsExpandState verifies that the expanded state travels
// with the project when it moves.
//
// Setup: project at index 1 (beta) is expanded; project at index 0 (alpha) is not.
// After MoveProjectUp on beta: beta is at index 0 (expanded), alpha is at index 1 (not expanded).
func TestMoveProjectUp_swapsExpandState(t *testing.T) {
	m := newReorderMenu(t)

	// Give beta a worktree so expand state is meaningful
	m.projects[1].Worktrees = []models.Worktree{{Path: "/tmp/beta--feat", Branch: "feat"}}
	// Re-write file to match
	if err := RewriteProjectsFile(m.projects, m.projectsFile); err != nil {
		t.Fatalf("setup: %v", err)
	}
	m.expandedWorktrees[1] = true // beta expanded, alpha not expanded

	m.selectedItem = 1 // cursor on beta

	m.MoveProjectUp()

	// beta is now at index 0 — its expand state should follow
	if !m.expandedWorktrees[0] {
		t.Error("expected expandedWorktrees[0] (beta's new position) to be true")
	}
	// alpha is now at index 1 — it was not expanded, so the key must be absent
	if m.expandedWorktrees[1] {
		t.Error("expected expandedWorktrees[1] (alpha's new position) to be false/absent")
	}
}

// TestMoveProjectUp_noopOnAddProjectRow verifies that MoveProjectUp is a no-op
// when the cursor is on the add-project row (a non-project row).
func TestMoveProjectUp_noopOnAddProjectRow(t *testing.T) {
	m := newReorderMenu(t)
	m.selectedItem = 3 // add-project row (3 projects -> flat index 3)

	itemType, _, _ := m.ResolveItem(3)
	if itemType != "add-project" {
		t.Fatalf("setup: expected add-project at flat index 3, got %q", itemType)
	}

	origOrder := []string{m.projects[0].Name, m.projects[1].Name, m.projects[2].Name}
	m.MoveProjectUp()

	for i, name := range origOrder {
		if m.projects[i].Name != name {
			t.Errorf("expected projects[%d]=%q unchanged, got %q", i, name, m.projects[i].Name)
		}
	}
}

// ---------------------------------------------------------------------------
// MoveProjectDown
// ---------------------------------------------------------------------------

// TestMoveProjectDown_movesProjectAndCursor verifies that moving the project at
// index 0 down places it at index 1 and the cursor follows it.
func TestMoveProjectDown_movesProjectAndCursor(t *testing.T) {
	m := newReorderMenu(t)
	m.selectedItem = 0 // project "alpha" (index 0)

	m.MoveProjectDown()

	// alpha should now be at index 1
	if m.projects[0].Name != "beta" {
		t.Errorf("expected projects[0]=%q, got %q", "beta", m.projects[0].Name)
	}
	if m.projects[1].Name != "alpha" {
		t.Errorf("expected projects[1]=%q, got %q", "alpha", m.projects[1].Name)
	}

	// cursor must follow the moved project
	itemType, projectIdx, _ := m.ResolveItem(m.selectedItem)
	if itemType != "project" {
		t.Errorf("expected cursor on project row, got itemType=%q", itemType)
	}
	if projectIdx != 1 {
		t.Errorf("expected cursor on project index 1 (alpha after move), got %d", projectIdx)
	}
}

// TestMoveProjectDown_noopAtLastProject verifies that calling MoveProjectDown
// when the cursor is already on the last project does nothing.
func TestMoveProjectDown_noopAtLastProject(t *testing.T) {
	m := newReorderMenu(t)
	m.selectedItem = 2 // project "gamma" (index 2, already last)

	m.MoveProjectDown()

	if m.projects[2].Name != "gamma" {
		t.Errorf("expected projects[2] to remain %q, got %q", "gamma", m.projects[2].Name)
	}
	if m.selectedItem != 2 {
		t.Errorf("expected selectedItem to remain 2, got %d", m.selectedItem)
	}
}

// ---------------------------------------------------------------------------
// Move-flash feedback
// ---------------------------------------------------------------------------

// TestMoveProjectUp_setsFlashOnMovedProject verifies that after a successful
// MoveProjectUp the moveFlashIdx is set to the project's new index and
// moveFlashTimer is positive (flash is active).
func TestMoveProjectUp_setsFlashOnMovedProject(t *testing.T) {
	m := newReorderMenu(t)
	m.selectedItem = 1 // project "beta" at index 1

	m.MoveProjectUp()

	// beta moved to index 0 — flash must point at index 0
	if m.moveFlashIdx != 0 {
		t.Errorf("expected moveFlashIdx=0 after MoveProjectUp, got %d", m.moveFlashIdx)
	}
	if m.moveFlashTimer <= 0 {
		t.Errorf("expected moveFlashTimer>0 after MoveProjectUp, got %d", m.moveFlashTimer)
	}
}

// TestMoveProjectDown_setsFlashOnMovedProject verifies that after a successful
// MoveProjectDown the moveFlashIdx is set to the project's new index and
// moveFlashTimer is positive (flash is active).
func TestMoveProjectDown_setsFlashOnMovedProject(t *testing.T) {
	m := newReorderMenu(t)
	m.selectedItem = 0 // project "alpha" at index 0

	m.MoveProjectDown()

	// alpha moved to index 1 — flash must point at index 1
	if m.moveFlashIdx != 1 {
		t.Errorf("expected moveFlashIdx=1 after MoveProjectDown, got %d", m.moveFlashIdx)
	}
	if m.moveFlashTimer <= 0 {
		t.Errorf("expected moveFlashTimer>0 after MoveProjectDown, got %d", m.moveFlashTimer)
	}
}

// TestMoveProjectUp_noFlashWhenAlreadyFirst verifies that when MoveProjectUp is
// a no-op (project is already first), no flash is activated.
func TestMoveProjectUp_noFlashWhenAlreadyFirst(t *testing.T) {
	m := newReorderMenu(t)
	m.selectedItem = 0 // project "alpha" already first

	m.MoveProjectUp()

	if m.moveFlashIdx != -1 {
		t.Errorf("expected moveFlashIdx=-1 (no flash) after no-op MoveProjectUp, got %d", m.moveFlashIdx)
	}
}

// TestMoveProjectDown_noFlashWhenAlreadyLast verifies that when MoveProjectDown is
// a no-op (project is already last), no flash is activated.
func TestMoveProjectDown_noFlashWhenAlreadyLast(t *testing.T) {
	m := newReorderMenu(t)
	m.selectedItem = 2 // project "gamma" already last

	m.MoveProjectDown()

	if m.moveFlashIdx != -1 {
		t.Errorf("expected moveFlashIdx=-1 (no flash) after no-op MoveProjectDown, got %d", m.moveFlashIdx)
	}
}

// TestMoveProjectUp_noFeedbackBanner verifies that MoveProjectUp no longer sets
// the text feedback banner (feedbackMsg should be empty after a successful move).
func TestMoveProjectUp_noFeedbackBanner(t *testing.T) {
	m := newReorderMenu(t)
	m.selectedItem = 1

	m.MoveProjectUp()

	if m.feedbackMsg != "" {
		t.Errorf("expected no feedback banner after MoveProjectUp, got %q", m.feedbackMsg)
	}
}

// TestMoveProjectDown_noFeedbackBanner verifies that MoveProjectDown no longer sets
// the text feedback banner (feedbackMsg should be empty after a successful move).
func TestMoveProjectDown_noFeedbackBanner(t *testing.T) {
	m := newReorderMenu(t)
	m.selectedItem = 0

	m.MoveProjectDown()

	if m.feedbackMsg != "" {
		t.Errorf("expected no feedback banner after MoveProjectDown, got %q", m.feedbackMsg)
	}
}
