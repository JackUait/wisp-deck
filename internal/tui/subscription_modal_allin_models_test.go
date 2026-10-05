package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/allin"
	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

// allInSubscriptionMenu opens the Subscriptions modal on the generated All-In
// profile, with two logins so the roster has rows to check off.
func allInSubscriptionMenu(t *testing.T) (*MainMenuModel, []allin.Row) {
	t.Helper()
	m := newBareSubscriptionMenu(t)
	m.addSubscriptionLogin("Work") // two sources: creates the All-In profile
	if allin.ProfileFile(m.claudeConfigsList) == "" {
		t.Fatal("setup: All-In profile was not created")
	}

	m.openSubscriptionModal()
	index := -1
	for i, profile := range m.subscriptionProfiles() {
		if profile.Provider.Key == claudeconfig.AllInProvider.Key {
			index = i
		}
	}
	if index < 0 {
		t.Fatalf("All-In profile is not listed: %+v", m.subscriptionProfiles())
	}
	m.selectSubscriptionProfile(index)
	if got := m.subscriptionModal.profileCursor; got != index {
		t.Fatalf("cursor is on %d, want the All-In row %d", got, index)
	}
	if !m.subscriptionModalOnAllIn() {
		t.Fatal("the All-In row does not report itself as All-In")
	}

	rows := allin.Roster(m.allInEnv())
	if len(rows) < 2 {
		t.Fatalf("setup: need at least two roster rows, got %d", len(rows))
	}
	return m, rows
}

func allInPickerModels(t *testing.T, m *MainMenuModel) []string {
	t.Helper()
	file := allin.ProfileFile(m.claudeConfigsList)
	data, err := os.ReadFile(filepath.Join(m.claudeConfigsDir, file))
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		ModelPicker struct {
			Options []struct {
				Model string `json:"model"`
			} `json:"options"`
		} `json:"modelPicker"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(settings.ModelPicker.Options))
	for _, option := range settings.ModelPicker.Options {
		out = append(out, option.Model)
	}
	return out
}

func allInDetail(t *testing.T, m *MainMenuModel) string {
	t.Helper()
	lines := m.subscriptionDetailLines(m.subscriptionDetailPaneWidth(), m.subscriptionModalBodyHeight())
	return stripAnsi(strings.Join(lines, "\n"))
}

// The four alias rows are inert for All-In: cycleSubscriptionMapping returns
// early on an empty model list, so the pane offered nothing but "(none)".
func TestSubscriptionModal_allInPaneListsEveryPickerRowAsACheckbox(t *testing.T) {
	m, rows := allInSubscriptionMenu(t)

	detail := allInDetail(t, m)
	if strings.Contains(detail, "(none)") {
		t.Fatalf("All-In still renders the inert alias rows:\n%s", detail)
	}
	for _, row := range rows {
		if !strings.Contains(detail, "[x] "+row.Label) {
			t.Fatalf("row %q is not checked off in the pane:\n%s", row.Label, detail)
		}
	}
}

func TestSubscriptionModal_togglingAnAllInRowHidesItFromThePicker(t *testing.T) {
	m, rows := allInSubscriptionMenu(t)
	gone := rows[0]

	m.subscriptionModal.detailCursor = subscriptionDetailAllInBase
	m.activateSubscriptionDetail()

	if m.subscriptionModal.err != nil {
		t.Fatalf("toggling the first row failed: %v", m.subscriptionModal.err)
	}
	if !allin.LoadHidden(allin.HiddenFile(m.claudeConfigsList))[allin.BareModel(gone.Model)] {
		t.Fatalf("row %q was not written to the hidden file", gone.Model)
	}
	for _, model := range allInPickerModels(t, m) {
		if model == gone.Model {
			t.Fatalf("hidden row %q is still in the generated picker", gone.Model)
		}
	}
	if detail := allInDetail(t, m); !strings.Contains(detail, "[ ] "+gone.Label) {
		t.Fatalf("row %q did not render as unchecked:\n%s", gone.Label, detail)
	}
}

func TestSubscriptionModal_togglingAnAllInRowBackRestoresIt(t *testing.T) {
	m, rows := allInSubscriptionMenu(t)
	back := rows[1]

	m.subscriptionModal.detailCursor = subscriptionDetailAllInBase + 1
	m.activateSubscriptionDetail()
	m.activateSubscriptionDetail()

	if m.subscriptionModal.err != nil {
		t.Fatalf("toggling back failed: %v", m.subscriptionModal.err)
	}
	if allin.LoadHidden(allin.HiddenFile(m.claudeConfigsList))[allin.BareModel(back.Model)] {
		t.Fatalf("row %q is still hidden after being toggled back", back.Model)
	}
	found := false
	for _, model := range allInPickerModels(t, m) {
		if model == back.Model {
			found = true
		}
	}
	if !found {
		t.Fatalf("row %q did not come back to the picker", back.Model)
	}
}

// replaceBuiltInOptions leaves no built-in row behind, so an empty picker is a
// session with no way to change model at all.
func TestSubscriptionModal_refusesToHideTheLastVisibleAllInRow(t *testing.T) {
	m, rows := allInSubscriptionMenu(t)

	for i := 0; i < len(rows)-1; i++ {
		m.subscriptionModal.detailCursor = subscriptionDetailAllInBase + i
		m.activateSubscriptionDetail()
		if m.subscriptionModal.err != nil {
			t.Fatalf("hiding row %d failed: %v", i, m.subscriptionModal.err)
		}
	}

	last := rows[len(rows)-1]
	m.subscriptionModal.detailCursor = subscriptionDetailAllInBase + len(rows) - 1
	m.activateSubscriptionDetail()

	if m.subscriptionModal.err == nil {
		t.Fatal("hiding the last visible row was allowed")
	}
	if allin.LoadHidden(allin.HiddenFile(m.claudeConfigsList))[allin.BareModel(last.Model)] {
		t.Fatalf("the last row %q was hidden anyway", last.Model)
	}
	if models := allInPickerModels(t, m); len(models) != 1 || models[0] != last.Model {
		t.Fatalf("picker = %v, want only %q", models, last.Model)
	}
}

func TestSubscriptionModal_spaceTogglesAnAllInRow(t *testing.T) {
	m, rows := allInSubscriptionMenu(t)
	m.subscriptionModal.detailCursor = subscriptionDetailAllInBase
	m.subscriptionModal.pane = subscriptionDetailsPane

	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeySpace})

	if !allin.LoadHidden(allin.HiddenFile(m.claudeConfigsList))[allin.BareModel(rows[0].Model)] {
		t.Fatalf("space did not hide %q", rows[0].Model)
	}
}

func TestSubscriptionModal_clickingAnAllInRowTogglesIt(t *testing.T) {
	m, rows := allInSubscriptionMenu(t)
	row := rows[1]

	x, y := subscriptionCardCell(t, m, row.Label)
	target := m.subscriptionModalTarget(x, y)
	if target.kind != subscriptionHitField || target.index != subscriptionDetailAllInBase+1 {
		t.Fatalf("hit test on %q returned %+v", row.Label, target)
	}

	updated, _ := m.Update(subscriptionScreenMouse(m, x, y, tea.MouseActionPress, tea.MouseButtonLeft))
	next, ok := updated.(*MainMenuModel)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	m = next

	if !allin.LoadHidden(allin.HiddenFile(m.claudeConfigsList))[allin.BareModel(row.Model)] {
		t.Fatalf("clicking %q did not hide it", row.Label)
	}
}

// The checklist is as tall as the roster, so the pane's scroll arithmetic has
// to grow with it or the cursor stops tracking past the first few rows.
func TestSubscriptionDetailCursorLine_followsTheAllInChecklist(t *testing.T) {
	m, rows := allInSubscriptionMenu(t)

	for i := range rows {
		m.subscriptionModal.detailCursor = subscriptionDetailAllInBase + i
		if got, want := m.subscriptionDetailCursorLine(), 8+i; got != want {
			t.Fatalf("row %d sits on line %d, want %d", i, got, want)
		}
	}

	m.subscriptionModal.detailCursor = subscriptionDetailRename
	if got, want := m.subscriptionDetailCursorLine(), 8+len(rows); got <= want {
		t.Fatalf("the action row is on line %d, want it below the %d-row checklist", got, len(rows))
	}
}

// The checklist belongs to All-In alone: every other provider keeps the four
// alias mappings it actually uses.
func TestSubscriptionModal_aGatewayProfileStillShowsTheFourAliases(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.selectSubscriptionProfile(1) // the first real subscription, never All-In

	if m.subscriptionModalOnAllIn() {
		t.Fatalf("a gateway profile reports itself as All-In: %+v", m.subscriptionModalProfile())
	}
	rows := m.subscriptionDetailRows()
	for _, row := range rows {
		if row >= subscriptionDetailAllInBase {
			t.Fatalf("a gateway profile grew checklist rows: %v", rows)
		}
	}
	if detail := allInDetail(t, m); !strings.Contains(detail, "Opus") {
		t.Fatalf("a gateway profile lost its alias rows:\n%s", detail)
	}
}
