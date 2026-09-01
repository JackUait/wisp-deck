package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/wisp-deck/internal/claudeconfig"
	"github.com/jackuait/wisp-deck/internal/featherless"
)

type subscriptionModelPickerState struct {
	models   []featherless.Model
	filtered []featherless.Model
	query    textinput.Model
	cursor   int
	offset   int
	loading  bool
	err      error
}

// featherlessCatalogMsg delivers the catalog fetched off the Update loop.
type featherlessCatalogMsg struct {
	models []featherless.Model
	err    error
}

func (m *MainMenuModel) featherlessCache() string {
	if m.featherlessCachePath != "" {
		return m.featherlessCachePath
	}
	return featherless.DefaultCachePath()
}

// featherlessKey returns any stored Featherless credential, so the catalog is
// fetched authenticated when possible and reports available_on_current_plan.
func (m *MainMenuModel) featherlessKey() string {
	if key := strings.TrimSpace(m.subscriptionModal.draft.apiKey); key != "" {
		return key
	}
	for _, config := range m.claudeConfigs {
		if !m.claudeConfigProvider(config).RemoteCatalog {
			continue
		}
		if key := strings.TrimSpace(claudeconfig.ReadAPIKey(m.claudeConfigsDir, config.File)); key != "" {
			return key
		}
	}
	return ""
}

// openSubscriptionModelPicker enters the picker and starts the fetch. The
// catalog is a ~7MB round trip, so it never runs inside Update.
func (m *MainMenuModel) openSubscriptionModelPicker() tea.Cmd {
	query := textinput.New()
	query.Placeholder = "search 15,000+ tool-calling models"
	query.Width = 36
	query.Focus()

	m.subscriptionModal.mode = subscriptionPickModel
	m.subscriptionModal.picker = subscriptionModelPickerState{query: query, loading: true}
	m.subscriptionModal.err = nil

	path := m.featherlessCache()
	key := m.featherlessKey()
	return func() tea.Msg {
		if models, fetchedAt, ok := featherless.LoadCache(path); ok && !featherless.Stale(fetchedAt) {
			return featherlessCatalogMsg{models: models}
		}
		models, err := featherless.Fetch(context.Background(), key)
		if err != nil {
			// A stale list beats no list: the picker stays usable offline.
			if cached, _, ok := featherless.LoadCache(path); ok {
				return featherlessCatalogMsg{models: cached}
			}
			return featherlessCatalogMsg{err: err}
		}
		_ = featherless.SaveCache(path, models)
		return featherlessCatalogMsg{models: models}
	}
}

// applyFeatherlessCatalog fills the picker from a delivered catalog.
func (m *MainMenuModel) applyFeatherlessCatalog(msg featherlessCatalogMsg) {
	picker := &m.subscriptionModal.picker
	picker.loading = false
	if msg.err != nil {
		picker.err = msg.err
		return
	}
	picker.err = nil
	picker.models = msg.models
	picker.filtered = featherless.Search(msg.models, picker.query.Value())
	picker.cursor = 0
	picker.offset = 0
}

func (m *MainMenuModel) updateSubscriptionModelPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	picker := &m.subscriptionModal.picker
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.subscriptionModal.mode = subscriptionBrowse
		picker.query.Blur()
		return m, nil
	case tea.KeyUp:
		if picker.cursor > 0 {
			picker.cursor--
		}
		return m, nil
	case tea.KeyDown:
		if picker.cursor < len(picker.filtered)-1 {
			picker.cursor++
		}
		return m, nil
	case tea.KeyEnter:
		if picker.cursor < 0 || picker.cursor >= len(picker.filtered) {
			return m, nil
		}
		model := picker.filtered[picker.cursor]
		if !model.OnPlan {
			picker.err = fmt.Errorf("%s is not available on this Featherless plan", model.ID)
			return m, nil
		}
		picker.query.Blur()
		m.applyFeatherlessPick(model)
		m.subscriptionModal.mode = subscriptionBrowse
		return m, nil
	}

	var cmd tea.Cmd
	picker.query, cmd = picker.query.Update(msg)
	picker.filtered = featherless.Search(picker.models, picker.query.Value())
	// The list shrank under the cursor; leaving it where it was would highlight
	// a row that is no longer there.
	picker.cursor = 0
	picker.offset = 0
	return m, cmd
}

// applyFeatherlessPick writes the chosen model onto the draft. The window comes
// from the catalog because an undeclared one falls back to a flat 200000, which
// strands a smaller model with no way to compact out of it. The images default
// follows the model's own image_input: sending an image to a text-only endpoint
// fails the turn.
func (m *MainMenuModel) applyFeatherlessPick(model featherless.Model) {
	draft := &m.subscriptionModal.draft
	draft.model = model.ID
	draft.window = fmt.Sprintf("%d", model.Context)
	draft.imagesBlocked = !model.ImageInput
	draft.customEdited = true
	draft.dirty = true
	m.subscriptionModal.err = nil
}

// subscriptionModelPickerLines renders the picker: a query field over the
// matching models. Styles are built here rather than held in package vars — a
// style built at init time binds the pre-tty Ascii renderer and comes out
// colorless.
func (m *MainMenuModel) subscriptionModelPickerLines(width, height int) []string {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	accent := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	amber := lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	picker := &m.subscriptionModal.picker

	lines := []string{
		accent.Render("Pick a Featherless model"),
		"",
		modalTruncate(picker.query.View(), width),
		"",
	}
	switch {
	case picker.loading:
		return append(lines, dim.Render("Loading catalog…"))
	case picker.err != nil:
		return append(lines,
			amber.Render(modalTruncate(picker.err.Error(), width)),
			"",
			dim.Render("Esc to go back"))
	case len(picker.filtered) == 0:
		return append(lines, dim.Render("No tool-calling model matches that."))
	}

	rows := make([]string, 0, len(picker.filtered))
	for i, model := range picker.filtered {
		row := fmt.Sprintf("%s  %dK  $%g/$%g per 1M",
			model.ID, model.Context/1024, model.InPerM, model.OutPerM)
		if !model.OnPlan {
			row += "  (not on plan)"
		}
		row = modalTruncate(row, width-2)
		switch {
		case i == picker.cursor:
			rows = append(rows, accent.Render("> "+row))
		case !model.OnPlan:
			rows = append(rows, dim.Render("  "+row))
		default:
			rows = append(rows, "  "+row)
		}
	}
	// Keep the cursor on screen: the list is thousands of rows long.
	visible := height - len(lines)
	if visible < 1 {
		visible = 1
	}
	if picker.cursor < picker.offset {
		picker.offset = picker.cursor
	}
	if picker.cursor >= picker.offset+visible {
		picker.offset = picker.cursor - visible + 1
	}
	return append(lines, modalWindow(rows, picker.offset, visible, width)...)
}
