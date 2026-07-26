package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// With the PLAN row present (wordmark host), the update notice right-aligns on
// the AGENT/title row — directly under the "Wisp Deck" wordmark.

// Without a PLAN row the wordmark sits on the title row, so the notice drops to
// the header spacer row below it.
func TestUpdateNotice_OnSpacerRowWithoutPlanRow(t *testing.T) {
	m := newTestMenu()
	m.CycleAITool("next") // switch to opencode: a non-claude agent hides the PLAN row
	m.SetUpdateVersion("2.24.0")
	lines := strings.Split(m.renderMenuBox(), "\n")

	titleRow := stripAnsi(lines[1])
	spacerRow := stripAnsi(lines[2])

	if !strings.Contains(titleRow, "Wisp Deck") {
		t.Fatalf("expected wordmark on the title row, got %q", titleRow)
	}
	if !strings.Contains(spacerRow, "v2.24.0") || !strings.Contains(spacerRow, "U Update") {
		t.Fatalf("expected update notice on the spacer row, got %q", spacerRow)
	}
	borderChar := strings.LastIndex(spacerRow, "│")
	noticeEnd := strings.Index(spacerRow, iconPillCapRight) + len(iconPillCapRight)
	if trailing := spacerRow[noticeEnd:borderChar]; len(strings.TrimSpace(trailing)) != 0 {
		t.Errorf("expected only whitespace between notice and border, got %q", trailing)
	}
}

// The notice is a chip: rounded caps around a single continuous fill. Idle, that
// fill is the same surface the selected project row uses, so the chip sits in
// the menu's existing material instead of introducing a filled button — this
// interface renders every action as unfilled "KEY Label" accent text, and a
// solid block would also stack a second loud shape under the orange wordmark.
func TestUpdateNotice_IdleChipUsesTheSelectedRowSurface(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(prev)

	m := newTestMenu()
	m.SetUpdateVersion("2.24.0")
	notice := m.updateNotice()

	plain := stripAnsi(notice)
	if !strings.HasPrefix(plain, iconPillCapLeft) {
		t.Errorf("notice does not open with the rounded left cap: %q", plain)
	}
	if !strings.HasSuffix(plain, iconPillCapRight) {
		t.Errorf("notice does not close with the rounded right cap: %q", plain)
	}
	if !strings.Contains(notice, "48;5;"+menuSurface) {
		t.Errorf("idle chip is not on the selected-row surface %s: %q", menuSurface, notice)
	}
	// The theme color may tint text, but must not fill the chip while idle.
	if bg := "48;5;" + string(m.theme.Primary); strings.Contains(notice, bg) {
		t.Errorf("idle chip is filled with the theme color %s; that treatment belongs to hover: %q", bg, notice)
	}
	// The action reads exactly like the action bar's "W Worktrees": accent text.
	if !strings.Contains(notice, "38;5;"+string(m.theme.Accent)) {
		t.Errorf("the U Update action does not use the action-bar accent: %q", notice)
	}
	// "available" is redundant next to a version chip and cost 10 columns.
	if strings.Contains(plain, "available") {
		t.Errorf("expected the wordy notice to be gone, got %q", plain)
	}
	if !strings.Contains(plain, "v2.24.0") || !strings.Contains(plain, "U Update") {
		t.Errorf("notice lost its version or action text: %q", plain)
	}
}

// Hover is where the chip is allowed to be loud: the surface floods with the
// theme color so it reads as armed. The footprint must not move, or the click
// span shifts out from under the cursor that is hovering it.
func TestUpdateNotice_HoverFloodsWithoutResizing(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(prev)

	m := newTestMenu()
	m.SetUpdateVersion("2.24.0")
	idle := m.updateNotice()

	m.hover = hitTarget{region: regionUpdate}
	hovered := m.updateNotice()

	if hovered == idle {
		t.Error("hovering the update chip changed nothing")
	}
	if bg := "48;5;" + string(m.theme.Primary); !strings.Contains(hovered, bg) {
		t.Errorf("hovered chip does not flood with the theme color %s: %q", bg, hovered)
	}
	if got, want := lipgloss.Width(hovered), lipgloss.Width(idle); got != want {
		t.Errorf("hovered chip width = %d, idle = %d; the chip must not resize", got, want)
	}
}

// No update available: no notice anywhere, spacer stays blank.
func TestUpdateNotice_AbsentWithoutUpdate(t *testing.T) {
	m := newTestMenu()
	box := stripAnsi(m.renderMenuBox())
	if strings.Contains(box, "Update") || strings.Contains(box, "available") {
		t.Errorf("expected no update notice without an update, got:\n%s", box)
	}
}

// The old full-width "Update available: ... brew upgrade" row is gone: the
// notice renders exactly once and never mentions brew.
func TestUpdateNotice_ReplacesOldFullWidthRow(t *testing.T) {
	m := newTestMenu()
	m.SetUpdateVersion("2.24.0")
	box := stripAnsi(m.renderMenuBox())
	if strings.Contains(box, "brew upgrade") {
		t.Errorf("old brew-upgrade row still renders:\n%s", box)
	}
	if got := strings.Count(box, "U Update"); got != 1 {
		t.Errorf("expected exactly one update notice, got %d", got)
	}
	// The notice reuses an existing header row, so the body click map must not
	// shift when an update is pending.
	withUpdate := m.MapRowToItem(8)
	m2 := newTestMenu()
	if without := m2.MapRowToItem(8); withUpdate != without {
		t.Errorf("MapRowToItem shifted with update pending: with=%d without=%d", withUpdate, without)
	}
}

// Pressing 'u'/'U' while an update is pending exits with the update action.
func TestUpdateNotice_UKeyTriggersUpdateAction(t *testing.T) {
	for _, r := range []rune{'u', 'U'} {
		m := newTestMenu()
		m.SetUpdateVersion("2.24.0")
		m.handleRune(r)
		res := m.Result()
		if res == nil || res.Action != "update" {
			t.Errorf("handleRune(%q) result = %+v, want action=update", r, res)
		}
	}
}

// Without a pending update, 'u' is a no-op.
func TestUpdateNotice_UKeyNoopWithoutUpdate(t *testing.T) {
	m := newTestMenu()
	m.handleRune('u')
	if res := m.Result(); res != nil {
		t.Errorf("handleRune('u') without update produced result %+v, want none", res)
	}
}

// Clicking the notice registers as regionUpdate and triggers the update action;
// the rest of the row (switcher gap) stays inert.
func TestUpdateNotice_ClickTriggersUpdateAction(t *testing.T) {
	m := newTestMenu()
	m.SetUpdateVersion("2.24.0")
	row := m.updateNoticeRowIndex()
	if row < 0 {
		t.Fatal("expected a notice row with an update pending")
	}
	start, end := m.updateNoticeSpan()

	if got := m.HitTest(start+1, row); got.region != regionUpdate {
		t.Fatalf("HitTest(%d,%d) = %v, want regionUpdate", start+1, row, got.region)
	}
	if got := m.HitTest(start-2, row); got.region == regionUpdate {
		t.Errorf("HitTest left of the notice registered as regionUpdate")
	}
	if got := m.HitTest(end+1, row); got.region == regionUpdate {
		t.Errorf("HitTest right of the notice registered as regionUpdate")
	}

	m.clickTarget(hitTarget{region: regionUpdate})
	res := m.Result()
	if res == nil || res.Action != "update" {
		t.Fatalf("click result = %+v, want action=update", res)
	}
}

// Without a PLAN row the clickable notice moves to the spacer row.
func TestUpdateNotice_ClickRowFollowsWordmark(t *testing.T) {
	m := newTestMenu()
	m.CycleAITool("next") // opencode: no PLAN row, wordmark on title row
	m.SetUpdateVersion("2.24.0")
	if got, want := m.updateNoticeRowIndex(), m.titleRowIndex()+1; got != want {
		t.Errorf("notice row = %d, want spacer row %d", got, want)
	}
	m2 := newTestMenu() // claude: PLAN row removed, so notice sits on the spacer too
	m2.SetUpdateVersion("2.24.0")
	if got, want := m2.updateNoticeRowIndex(), m2.titleRowIndex()+1; got != want {
		t.Errorf("notice row = %d, want spacer row %d", got, want)
	}
	m3 := newTestMenu()
	if got := m3.updateNoticeRowIndex(); got != -1 {
		t.Errorf("notice row without update = %d, want -1", got)
	}
}

// The notice renders on the Settings and Stats tabs too — the header chrome is
// shared across tabs.
func TestUpdateNotice_ShowsOnAllTabs(t *testing.T) {
	m := newTestMenu()
	m.SetUpdateVersion("2.24.0")

	if box := stripAnsi(m.renderSettingsBox()); !strings.Contains(box, "U Update") {
		t.Errorf("expected update notice on the Settings tab")
	}
	if box := stripAnsi(m.renderStatsBox()); !strings.Contains(box, "U Update") {
		t.Errorf("expected update notice on the Stats tab")
	}
}
