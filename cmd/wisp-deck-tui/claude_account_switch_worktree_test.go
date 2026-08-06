package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/jackuait/wisp-deck/internal/claudeaccount"
)

// worktreeRowsInput is the switcher input for a project with three checkouts,
// the session running the middle one. Shared by the tests below so they all
// describe the same session.
func worktreeRowsInput(t *testing.T) switchRowsInput {
	t.Helper()
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	if err := os.WriteFile(list, []byte("Work:work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return switchRowsInput{
		listFile:   list,
		active:     "",
		activeTool: "claude",
		tools:      []string{"codex"},
		worktrees: []worktreeEntry{
			{Branch: "main", Path: "/repo"},
			{Branch: "feat-ledger-pill", Path: "/wt/repo--feat-ledger-pill"},
			{Branch: "fix-attention", Path: "/wt/repo--fix-attention"},
		},
		activeWorktree: "/wt/repo--feat-ledger-pill",
	}
}

// The project's checkouts land after the agent rows, main first (git's first
// porcelain block), each carrying its path as the row's identity.
func TestAccountSwitch_buildSwitchRows_appendsWorktreeRowsAfterAgents(t *testing.T) {
	rows, _ := buildSwitchRows(worktreeRowsInput(t))

	var got []string
	for _, r := range rows {
		if r.Worktree != "" {
			got = append(got, r.Label+"="+r.Worktree)
		}
	}
	want := []string{
		"main=/repo",
		"feat-ledger-pill=/wt/repo--feat-ledger-pill",
		"fix-attention=/wt/repo--fix-attention",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("worktree rows = %v, want %v", got, want)
	}
	// They must come last — the agent rows lead, the checkouts follow.
	last := len(rows) - len(want)
	for i, r := range rows[:last] {
		if r.Worktree != "" {
			t.Fatalf("row %d is a worktree row before the agent rows: %+v", i, r)
		}
	}
}

// The checkout the session runs is marked Active. Unlike every other row group,
// this dot is independent of the cursor: the cursor still opens on the running
// account, so a worktree row's dot cannot be derived from it.
func TestAccountSwitch_buildSwitchRows_marksTheRunningWorktree(t *testing.T) {
	rows, cursor := buildSwitchRows(worktreeRowsInput(t))

	if rows[cursor].Worktree != "" {
		t.Fatalf("cursor opened on a worktree row (%+v), want the running account", rows[cursor])
	}
	var active []string
	for _, r := range rows {
		if r.Worktree != "" && r.Active {
			active = append(active, r.Worktree)
		}
	}
	if len(active) != 1 || active[0] != "/wt/repo--feat-ledger-pill" {
		t.Fatalf("active worktree rows = %v, want exactly the running checkout", active)
	}
}

// A project with only its main checkout has nothing to switch to, so the group
// is omitted entirely — no rows, and (below) no header or separator either.
func TestAccountSwitch_buildSwitchRows_omitsTheGroupForASingleCheckout(t *testing.T) {
	in := worktreeRowsInput(t)
	in.worktrees = []worktreeEntry{{Branch: "main", Path: "/repo"}}
	in.activeWorktree = "/repo"

	rows, _ := buildSwitchRows(in)
	for _, r := range rows {
		if r.Worktree != "" {
			t.Fatalf("single-checkout project produced a worktree row: %+v", r)
		}
	}
}

// A worktree choice reports as "worktree:<path>" so the bash side can tell it
// from an account dir, an agent, or a subscription.
func TestAccountSwitch_switchResultValue_worktreeRow(t *testing.T) {
	row := switchRow{Label: "main", Worktree: "/repo"}
	if got := switchResultValue(row); got != "worktree:/repo" {
		t.Fatalf("worktree row result = %q, want worktree:/repo", got)
	}
}

// The checkouts render as their own group: a blank separator, a non-selectable
// "Worktree" header, then the branch rows indented beneath it — and the running
// checkout carries a dot of its own, alongside the running account's.
func TestAccountSwitch_innerLines_rendersTheWorktreeGroup(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	rows, cursor := buildSwitchRows(worktreeRowsInput(t))
	lines := newAccountSwitchModel(rows, cursor, "").innerLines()

	headerIdx := -1
	for i, l := range lines {
		if strings.Contains(l, "Worktree") {
			headerIdx = i
			break
		}
	}
	if headerIdx < 0 {
		t.Fatalf("no Worktree group header:\n%s", strings.Join(lines, "\n"))
	}
	if strings.TrimSpace(lines[headerIdx-1]) != "" {
		t.Errorf("Worktree header must follow a blank separator, got %q", lines[headerIdx-1])
	}
	if !strings.Contains(lines[headerIdx], worktreeRowGlyph()) {
		t.Errorf("Worktree header must carry the branch glyph, got %q", lines[headerIdx])
	}
	for i, branch := range []string{"main", "feat-ledger-pill", "fix-attention"} {
		line := lines[headerIdx+1+i]
		if !strings.Contains(line, branch) {
			t.Fatalf("line %d = %q, want the %s row", headerIdx+1+i, line, branch)
		}
		if !strings.HasPrefix(line, "    ") {
			t.Errorf("%s row must nest under the header, got %q", branch, line)
		}
	}
	// Two dots: the running account and the running checkout.
	dots := strings.Count(strings.Join(lines, "\n"), "●")
	if dots != 2 {
		t.Fatalf("active dots = %d, want 2 (running account + running checkout):\n%s",
			dots, strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[headerIdx+2], "●") {
		t.Errorf("the running checkout must carry the dot, got %q", lines[headerIdx+2])
	}
}

// With a single checkout the group leaves no trace — no header and no trailing
// blank line pretending a group is coming.
func TestAccountSwitch_innerLines_noWorktreeGroupForASingleCheckout(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	in := worktreeRowsInput(t)
	in.worktrees = []worktreeEntry{{Branch: "main", Path: "/repo"}}
	in.activeWorktree = "/repo"
	rows, cursor := buildSwitchRows(in)

	lines := newAccountSwitchModel(rows, cursor, "").innerLines()
	if strings.Contains(strings.Join(lines, "\n"), "Worktree") {
		t.Fatalf("single-checkout card must not render the group:\n%s", strings.Join(lines, "\n"))
	}
	// header, Default, Work, codex, blank, help
	if len(lines) != 6 {
		t.Fatalf("expected 6 lines, got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}
}

// A click on a worktree row selects THAT row. Two group headers and a blank
// separator now sit between the card's first line and the checkouts, so a
// mapping that assumes one header lands the click rows off.
func TestAccountSwitch_mouse_clickSelectsWorktreeRowBelowBothHeaders(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	rows, cursor := buildSwitchRows(worktreeRowsInput(t))
	m := newAccountSwitchModel(rows, cursor, "")
	m.width, m.height = 80, 30

	entries := m.displayEntries()
	firstRowY, cardLeft, _ := accountSwitchLayout(m.width, m.height, len(entries), m.contentWidth())

	// The last display entry that is a real row is "fix-attention".
	wantIdx := -1
	clickY := 0
	for i, e := range entries {
		if e >= 0 && rows[e].Worktree == "/wt/repo--fix-attention" {
			wantIdx, clickY = e, firstRowY+i
		}
	}
	if wantIdx < 0 {
		t.Fatal("fix-attention has no display entry")
	}

	updated, cmd := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
		X: cardLeft + 2, Y: clickY,
	})
	got := updated.(accountSwitchModel)
	if !got.chosen {
		t.Fatal("click on a worktree row did not choose it")
	}
	if got.cursor != wantIdx {
		t.Fatalf("chose row %d (%+v), want %d (fix-attention)", got.cursor, rows[got.cursor], wantIdx)
	}
	if !isQuitCmd(t, cmd) {
		t.Fatal("choosing a worktree row must quit the popup")
	}
}

// Clicking either group header is inert — a header is not a row, and mapping it
// to one would switch the session on a stray click.
func TestAccountSwitch_mouse_clickOnWorktreeHeaderIsInert(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	rows, cursor := buildSwitchRows(worktreeRowsInput(t))
	m := newAccountSwitchModel(rows, cursor, "")
	m.width, m.height = 80, 30

	entries := m.displayEntries()
	firstRowY, cardLeft, _ := accountSwitchLayout(m.width, m.height, len(entries), m.contentWidth())
	headerY := -1
	for i, e := range entries {
		if e == entryWorktreeHeader {
			headerY = firstRowY + i
		}
	}
	if headerY < 0 {
		t.Fatal("no Worktree header display entry")
	}

	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
		X: cardLeft + 2, Y: headerY,
	})
	if got := updated.(accountSwitchModel); got.chosen {
		t.Fatalf("clicking the Worktree header chose row %+v", rows[got.cursor])
	}
}

// Arrow navigation reaches the checkouts: they are ordinary selectable rows, so
// pressing down from the last agent row must step into the group.
func TestAccountSwitch_keys_downReachesTheWorktreeRows(t *testing.T) {
	rows, _ := buildSwitchRows(worktreeRowsInput(t))
	m := newAccountSwitchModel(rows, 0, "")

	for i := 0; i < len(rows); i++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(accountSwitchModel)
	}
	if rows[m.cursor].Worktree != "/wt/repo--fix-attention" {
		t.Fatalf("cursor after walking down = %+v, want the last checkout", rows[m.cursor])
	}
}

// The modal moves the agent, the login, the backend AND the checkout, so it is
// titled for all four. The legacy claude-only popup keeps its own wording.
func TestAccountSwitch_titleIsSwitch(t *testing.T) {
	rows, cursor := buildSwitchRows(worktreeRowsInput(t))
	if got := newAccountSwitchModel(rows, cursor, "").titleText(); got != "Switch" {
		t.Fatalf("title = %q, want Switch", got)
	}
	legacy := newAccountSwitchModel([]switchRow{{Label: "Default"}}, 0, "")
	if got := legacy.titleText(); got != "Switch Claude login" {
		t.Fatalf("account-only title = %q, want Switch Claude login", got)
	}
}

// --measure sizes the tmux popup. With the worktree group present the measured
// card must still be exactly what View renders, headers and separator included.
func TestAccountSwitch_measureCoversTheWorktreeGroup(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	rows, cursor := buildSwitchRows(worktreeRowsInput(t))
	m := newAccountSwitchModel(rows, cursor, "")

	wantH := len(m.innerLines()) + accountSwitchPadY + accountSwitchPadBottom + 2*accountSwitchBorder
	entries := m.displayEntries()
	gotH := accountSwitchHeader + len(entries) + accountSwitchFooter +
		accountSwitchPadY + accountSwitchPadBottom + 2*accountSwitchBorder
	if gotH != wantH {
		t.Fatalf("layout height = %d, rendered height = %d — the display entries and innerLines disagree", gotH, wantH)
	}

	m.width, m.height = 120, 40
	view := strings.Split(m.View(), "\n")
	firstRowY, _, _ := accountSwitchLayout(m.width, m.height, len(entries), m.contentWidth())
	for i, e := range entries {
		if e < 0 {
			continue
		}
		if !strings.Contains(view[firstRowY+i], rows[e].Label) {
			t.Fatalf("screen line %d = %q, want row %d (%s)",
				firstRowY+i, view[firstRowY+i], e, rows[e].Label)
		}
	}
}

func TestAccountSwitch_worktreeFlagsRegistered(t *testing.T) {
	for _, name := range []string{"worktrees", "active-worktree"} {
		if claudeAccountSwitchCmd.Flags().Lookup(name) == nil {
			t.Fatalf("claude-account-switch must register --%s", name)
		}
	}
}

// The worktree list arrives as a file of "branch:path" lines, split on the
// FIRST colon: git forbids ':' in a ref name, so everything after it is the
// path, however many colons the path itself holds.
func TestAccountSwitch_loadWorktrees_splitsOnTheFirstColon(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "worktrees")
	body := "main:/repo\nfeat:/wt/odd:name\n\n# comment\n(detached):/wt/head\n"
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	got := loadWorktrees(file)
	want := []worktreeEntry{
		{Branch: "main", Path: "/repo"},
		{Branch: "feat", Path: "/wt/odd:name"},
		{Branch: "(detached)", Path: "/wt/head"},
	}
	if len(got) != len(want) {
		t.Fatalf("loadWorktrees = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if loadWorktrees("") != nil || loadWorktrees(filepath.Join(dir, "absent")) != nil {
		t.Fatal("an absent worktree file must yield no rows, not an error")
	}
}

// A worktree row's hue must never collide with a login's, or a checkout would
// read as an account.
func TestAccountSwitch_worktreeRowColorIsOutsideTheAccountPalette(t *testing.T) {
	color := worktreeRowColor()
	for _, c := range claudeaccount.Palette {
		if c == color {
			t.Fatalf("worktree hue %d collides with the account palette", color)
		}
	}
}
