package tui

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/models"
)

// refreshMsg builds the message a completed background detection delivers.
func refreshMsg(byPath map[string][]models.Worktree) worktreesRefreshedMsg {
	return worktreesRefreshedMsg{byPath: byPath}
}

// The ghost tickers are gated on animated mode; the worktree refresh must not
// be, or a static-ghost session never picks up an outside worktree at all.
func TestInit_armsTheWorktreeRefreshWithoutTheAnimatedGhost(t *testing.T) {
	m := newWorktreeMenu()
	m.ghostDisplay = "none"
	m.statsLoaded = true // keep the usage-stats ingest out of the batch
	m.worktreeRefreshEvery = time.Millisecond

	cmds := m.initCmds()
	if len(cmds) != 1 {
		t.Fatalf("got %d init cmds, want just the worktree refresh", len(cmds))
	}
	if _, ok := cmds[0]().(worktreesRefreshedMsg); !ok {
		t.Fatalf("init cmd did not produce a worktree refresh: %T", cmds[0]())
	}
}

// The reported bug: a worktree created in a terminal while the menu sits open
// never appears, because the list was read once at startup.
func TestWorktreeRefresh_showsAWorktreeCreatedOutsideTheMenu(t *testing.T) {
	m := newWorktreeMenu()
	m.expandedWorktrees[2] = true // gamma, which starts with none

	m.Update(refreshMsg(map[string][]models.Worktree{
		"/tmp/gamma": {{Path: "/tmp/gamma--new", Branch: "feat/new"}},
	}))

	if len(m.projects[2].Worktrees) != 1 || m.projects[2].Worktrees[0].Branch != "feat/new" {
		t.Fatalf("gamma worktrees: got %+v, want the new one", m.projects[2].Worktrees)
	}
}

// A refresh that stopped rescheduling itself would fix the list exactly once.
func TestWorktreeRefresh_rearmsItself(t *testing.T) {
	m := newWorktreeMenu()
	m.worktreeRefreshEvery = time.Millisecond

	_, cmd := m.Update(refreshMsg(map[string][]models.Worktree{"/tmp/alpha": nil}))
	if cmd == nil {
		t.Fatal("refresh did not reschedule itself")
	}
	if _, ok := cmd().(worktreesRefreshedMsg); !ok {
		t.Fatalf("rescheduled cmd produced %T, want another refresh", cmd())
	}
}

// Rows shift when a worktree appears above the cursor. Enter launches whatever
// the cursor is on, so it must stay on the same worktree, not the same row.
func TestWorktreeRefresh_keepsTheCursorOnTheSameWorktree(t *testing.T) {
	m := newWorktreeMenu()
	m.expandedWorktrees[0] = true
	m.selectedItem = 2 // alpha's second worktree, fix/y

	if _, _, wtIdx := m.ResolveItem(m.selectedItem); wtIdx != 1 {
		t.Fatalf("precondition: cursor is on worktree %d, want 1", wtIdx)
	}

	m.Update(refreshMsg(map[string][]models.Worktree{
		"/tmp/alpha": {
			{Path: "/tmp/alpha--brand-new", Branch: "feat/brand-new"},
			{Path: "/tmp/alpha--feat", Branch: "feat/x"},
			{Path: "/tmp/alpha--fix", Branch: "fix/y"},
		},
	}))

	itemType, projectIdx, wtIdx := m.ResolveItem(m.selectedItem)
	if itemType != "worktree" || projectIdx != 0 || m.projects[0].Worktrees[wtIdx].Path != "/tmp/alpha--fix" {
		t.Fatalf("cursor moved to %s/%d/%d, want the alpha--fix worktree row", itemType, projectIdx, wtIdx)
	}
}

// The worktree the cursor sits on can be removed from another terminal.
func TestWorktreeRefresh_fallsBackToTheProjectRowWhenTheCursorsWorktreeIsGone(t *testing.T) {
	m := newWorktreeMenu()
	m.expandedWorktrees[0] = true
	m.selectedItem = 2 // alpha's fix/y

	m.Update(refreshMsg(map[string][]models.Worktree{"/tmp/alpha": nil}))

	itemType, projectIdx, _ := m.ResolveItem(m.selectedItem)
	if itemType != "project" || projectIdx != 0 {
		t.Fatalf("cursor landed on %s/%d, want the alpha project row", itemType, projectIdx)
	}
}

// A project expands to its add-worktree row even with no worktrees, so an
// emptied list must not collapse a row the user opened on purpose.
func TestWorktreeRefresh_keepsAProjectExpandedAfterItsLastWorktreeGoes(t *testing.T) {
	m := newWorktreeMenu()
	m.expandedWorktrees[0] = true

	m.Update(refreshMsg(map[string][]models.Worktree{"/tmp/alpha": nil}))

	if !m.IsExpanded(0) {
		t.Error("refresh collapsed alpha")
	}
}

// Rows shifting under an open confirm is how someone deletes the wrong thing.
func TestWorktreeRefresh_isHeldBackWhileDeleteModeIsOpen(t *testing.T) {
	m := newWorktreeMenu()
	m.deleteMode = true

	_, cmd := m.Update(refreshMsg(map[string][]models.Worktree{
		"/tmp/gamma": {{Path: "/tmp/gamma--new", Branch: "feat/new"}},
	}))

	if len(m.projects[2].Worktrees) != 0 {
		t.Errorf("refresh applied under an open delete confirm: %+v", m.projects[2].Worktrees)
	}
	if cmd == nil {
		t.Error("a held-back refresh must still reschedule")
	}
}

func TestWorktreeRefresh_isHeldBackWhileTheAddProjectInputIsOpen(t *testing.T) {
	m := newWorktreeMenu()
	m.inputMode = "add-project"

	m.Update(refreshMsg(map[string][]models.Worktree{
		"/tmp/gamma": {{Path: "/tmp/gamma--new", Branch: "feat/new"}},
	}))

	if len(m.projects[2].Worktrees) != 0 {
		t.Errorf("refresh applied while adding a project: %+v", m.projects[2].Worktrees)
	}
}

// The branch picker is a pushed screen; the menu underneath is mid-flow.
func TestWorktreeRefresh_isHeldBackWhileTheBranchPickerIsOpen(t *testing.T) {
	m := newWorktreeMenu()
	m.worktreePendingProjectIdx = 0

	m.Update(refreshMsg(map[string][]models.Worktree{
		"/tmp/gamma": {{Path: "/tmp/gamma--new", Branch: "feat/new"}},
	}))

	if len(m.projects[2].Worktrees) != 0 {
		t.Errorf("refresh applied under the branch picker: %+v", m.projects[2].Worktrees)
	}
}

// Detection is spawned against a snapshot of the paths; a project deleted
// before the result lands must not have its worktrees written onto a neighbour.
func TestWorktreeRefresh_ignoresAPathThatIsNoLongerAProject(t *testing.T) {
	m := newWorktreeMenu()

	m.Update(refreshMsg(map[string][]models.Worktree{
		"/tmp/deleted": {{Path: "/tmp/deleted--wt", Branch: "gone"}},
		"/tmp/beta":    {{Path: "/tmp/beta--main", Branch: "main"}},
	}))

	for i, proj := range m.projects {
		for _, wt := range proj.Worktrees {
			if wt.Branch == "gone" {
				t.Fatalf("worktrees of a removed project landed on %s (index %d)", proj.Name, i)
			}
		}
	}
}

// A project the round did not report keeps what it had — an absent entry is
// "not measured", not "no worktrees".
func TestWorktreeRefresh_keepsWorktreesOfAProjectTheRoundDidNotReport(t *testing.T) {
	m := newWorktreeMenu()

	m.Update(refreshMsg(map[string][]models.Worktree{"/tmp/beta": nil}))

	if len(m.projects[0].Worktrees) != 2 {
		t.Errorf("alpha lost its worktrees on an unrelated refresh: %+v", m.projects[0].Worktrees)
	}
}

var _ tea.Model = (*MainMenuModel)(nil)

// inertScreen stands in for a pushed screen that ignores everything.
type inertScreen struct{}

func (inertScreen) Init() tea.Cmd                       { return nil }
func (inertScreen) Update(tea.Msg) (tea.Model, tea.Cmd) { return inertScreen{}, nil }
func (inertScreen) View() string                        { return "" }

// AppModel delegates every message to the topmost screen, so a refresh that
// landed while the branch picker was open would be swallowed and the
// self-rescheduling chain would die for the rest of the session.
func TestAppModel_deliversAWorktreeRefreshToTheMenuUnderAPushedScreen(t *testing.T) {
	menu := newWorktreeMenu()
	app := NewAppModel(menu)

	updated, _ := app.Update(PushScreenMsg{Model: inertScreen{}})
	app = updated.(AppModel)

	_, cmd := app.Update(refreshMsg(map[string][]models.Worktree{
		"/tmp/gamma": {{Path: "/tmp/gamma--new", Branch: "feat/new"}},
	}))

	if len(menu.projects[2].Worktrees) != 1 {
		t.Errorf("the menu underneath never saw the refresh: %+v", menu.projects[2].Worktrees)
	}
	if cmd == nil {
		t.Error("the refresh chain died under a pushed screen")
	}
}

// The whole point, against real git: the menu was built when the project had
// no worktrees, one is created from a terminal while it sits open, and the
// next poll must show it without the user relaunching.
func TestWorktreeRefresh_picksUpAWorktreeGitCreatedAfterTheMenuOpened(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-m", "root"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	m := NewMainMenu([]models.Project{{Name: "repo", Path: repo}}, []string{"claude"}, "claude", "none")
	m.width, m.height = 100, 40
	m.statsLoaded = true
	m.worktreeRefreshEvery = time.Millisecond

	cmds := m.initCmds()
	if len(cmds) != 1 {
		t.Fatalf("got %d init cmds, want just the worktree refresh", len(cmds))
	}
	_, cmd := m.Update(cmds[0]())
	if len(m.projects[0].Worktrees) != 0 {
		t.Fatalf("fresh repo already reports worktrees: %+v", m.projects[0].Worktrees)
	}

	add := exec.Command("git", "-C", repo, "worktree", "add", "-b", "outside", filepath.Join(t.TempDir(), "wt"))
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v: %s", err, out)
	}

	m.Update(cmd())

	if len(m.projects[0].Worktrees) != 1 || m.projects[0].Worktrees[0].Branch != "outside" {
		t.Fatalf("worktree created outside the menu never appeared: %+v", m.projects[0].Worktrees)
	}
}
