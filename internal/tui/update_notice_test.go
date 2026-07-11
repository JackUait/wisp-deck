package tui

import (
	"strings"
	"testing"
)

// With the PLAN row present (wordmark host), the update notice right-aligns on
// the AGENT/title row — directly under the "Wisp Deck" wordmark.
func TestUpdateNotice_UnderWordmarkOnTitleRow(t *testing.T) {
	m := newTestMenu()
	m.SetUpdateVersion("2.24.0")
	lines := strings.Split(m.renderMenuBox(), "\n")

	planRow := stripAnsi(lines[1])
	titleRow := stripAnsi(lines[2])

	if !strings.Contains(planRow, "Wisp Deck") {
		t.Fatalf("expected wordmark on the PLAN row, got %q", planRow)
	}
	if !strings.Contains(titleRow, "v2.24.0 available") {
		t.Fatalf("expected update notice on the AGENT row, got %q", titleRow)
	}
	if !strings.Contains(titleRow, "U Update") {
		t.Errorf("expected the update button on the AGENT row, got %q", titleRow)
	}
	// Right-aligned: only whitespace between the notice's end and the border.
	borderChar := strings.LastIndex(titleRow, "│")
	noticeEnd := strings.Index(titleRow, "U Update") + len("U Update")
	trailing := titleRow[noticeEnd:borderChar]
	if len(strings.TrimSpace(trailing)) != 0 {
		t.Errorf("expected only whitespace between notice and border, got %q", trailing)
	}
	// The AGENT switcher stays on the left of the same row.
	agentIdx := strings.Index(titleRow, "Claude Code")
	noticeIdx := strings.Index(titleRow, "v2.24.0")
	if agentIdx < 0 || noticeIdx < 0 || agentIdx > noticeIdx {
		t.Errorf("expected AGENT switcher left of the notice, got %q", titleRow)
	}
}

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
	if !strings.Contains(spacerRow, "v2.24.0 available") || !strings.Contains(spacerRow, "U Update") {
		t.Fatalf("expected update notice on the spacer row, got %q", spacerRow)
	}
	borderChar := strings.LastIndex(spacerRow, "│")
	noticeEnd := strings.Index(spacerRow, "U Update") + len("U Update")
	if trailing := spacerRow[noticeEnd:borderChar]; len(strings.TrimSpace(trailing)) != 0 {
		t.Errorf("expected only whitespace between notice and border, got %q", trailing)
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
	m2 := newTestMenu() // PLAN row present: notice shares the title row
	m2.SetUpdateVersion("2.24.0")
	if got, want := m2.updateNoticeRowIndex(), m2.titleRowIndex(); got != want {
		t.Errorf("notice row = %d, want title row %d", got, want)
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
