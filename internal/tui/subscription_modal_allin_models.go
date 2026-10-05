package tui

import (
	"errors"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/wisp-deck/internal/allin"
	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

// The All-In profile routes by picker row, not by model alias, so its MODEL
// ROUTING block is a checklist of the roster instead of the four Opus/Sonnet/
// Haiku/Fable mappings. Those mappings are inert here — cycleSubscriptionMapping
// returns early on the empty model list an All-In profile has — so the pane
// showed four rows reading "(none)" that nothing could change.
//
// Checking a row off writes the hidden-rows sidecar and refreshes the profile
// immediately, the way 'x' already disables a subscription: it is a property of
// the machine, not of the draft, so it never waits for "Save changes".

// subscriptionAllInRowIndent is what a checklist row spends before its label:
// the two-cell cursor marker plus the four-cell checkbox.
const subscriptionAllInRowIndent = 6

// subscriptionAllInState is the roster the checklist renders, which of its rows
// are hidden, and what each source has left. Reloaded on selection and on every
// toggle — all three follow the machine, never the draft.
type subscriptionAllInState struct {
	rows   []allin.Row
	hidden map[string]bool
	usage  map[string]allin.Quota
}

func (m *MainMenuModel) allInEnv() allin.Env {
	return allin.Env{
		AccountsList:     m.claudeAccountsList,
		AccountsDir:      m.claudeAccountsDir,
		ConfigsList:      m.claudeConfigsList,
		ConfigsDir:       m.claudeConfigsDir,
		DefaultLabelFile: m.claudeDefaultLabelFile,
	}
}

// subscriptionModalOnAllIn reports whether the focused row is the generated
// router profile. It keys on the provider marker rather than the display name,
// which the user can rename.
func (m *MainMenuModel) subscriptionModalOnAllIn() bool {
	if m.subscriptionModalOnAddRow() || m.subscriptionModalOnAddLoginRow() ||
		m.subscriptionModalOnLoginRow() {
		return false
	}
	profile := m.subscriptionModalProfile()
	return !profile.Standard && profile.Provider.Key == claudeconfig.AllInProvider.Key
}

func (m *MainMenuModel) loadAllInChecklist() {
	if !m.subscriptionModalOnAllIn() {
		m.subscriptionModal.allIn = subscriptionAllInState{}
		return
	}
	env := m.allInEnv()
	m.subscriptionModal.allIn = subscriptionAllInState{
		rows:   allin.Roster(env),
		hidden: allin.LoadHidden(allin.HiddenFile(m.claudeConfigsList)),
		usage:  allin.Usage(env, time.Now()),
	}
}

func (m *MainMenuModel) allInVisibleRows() int {
	visible := 0
	for _, row := range m.subscriptionModal.allIn.rows {
		if !m.subscriptionModal.allIn.hidden[allin.BareModel(row.Model)] {
			visible++
		}
	}
	return visible
}

// toggleAllInRow flips one picker row and rewrites the profile. The last
// visible row is refused: the generated picker sets replaceBuiltInOptions, so
// an empty options list is a session with no way to change model at all.
func (m *MainMenuModel) toggleAllInRow(index int) {
	rows := m.subscriptionModal.allIn.rows
	if index < 0 || index >= len(rows) || m.claudeConfigsList == "" {
		return
	}
	row := rows[index]
	if !m.subscriptionModal.allIn.hidden[allin.BareModel(row.Model)] && m.allInVisibleRows() <= 1 {
		m.subscriptionModal.err = errors.New("At least one model must stay in the picker")
		return
	}
	if _, err := allin.ToggleHidden(allin.HiddenFile(m.claudeConfigsList), row.Model); err != nil {
		m.subscriptionModal.err = err
		return
	}
	m.subscriptionModal.err = nil
	m.ensureAllIn()
	m.loadAllInChecklist()
}

// subscriptionAllInRowLabel is one row's label with what its subscription has
// left. Quotas come from the same caches the generated picker is annotated
// from, so the pane and /model always read the same number.
func (m *MainMenuModel) subscriptionAllInRowLabel(row allin.Row) string {
	text := m.subscriptionModal.allIn.usage[allin.SourceKey(row.Model)].Text()
	if text == "" {
		return row.Label
	}
	return row.Label + " · " + text
}

// subscriptionAllInRowText shapes one row's label to the pane. The renderer and
// the mouse hit test both call it: the hit test locates a row by searching the
// rendered line for this exact string, so a label truncated one way and
// searched for the other would never match.
func subscriptionAllInRowText(label string, width int) string {
	budget := width - subscriptionAllInRowIndent
	if budget < 1 {
		budget = 1
	}
	return modalTruncate(label, budget)
}

func (m *MainMenuModel) subscriptionAllInChecklistLines(width int, accent, dim, green lipgloss.Style) []string {
	state := m.subscriptionModal.allIn
	lines := make([]string, 0, len(state.rows)+1)

	for i, row := range state.rows {
		marker := "  "
		if m.subscriptionModal.mode == subscriptionBrowse &&
			m.subscriptionModal.pane == subscriptionDetailsPane &&
			m.subscriptionModal.detailCursor == subscriptionDetailAllInBase+i {
			marker = accent.Render("▌") + " "
		}
		box, style := "[x] ", green
		if state.hidden[allin.BareModel(row.Model)] {
			box, style = "[ ] ", dim
		}
		lines = append(lines, marker+style.Render(box+
			subscriptionAllInRowText(m.subscriptionAllInRowLabel(row), width)))
	}
	return append(lines, dim.Render(modalTruncate("Enter or Space shows or hides a row in /model.", width)))
}
