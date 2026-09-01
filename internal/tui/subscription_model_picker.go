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
		models, err := featherless.LoadOrFetch(path, func() ([]featherless.Model, error) {
			return featherless.Fetch(context.Background(), key)
		})
		if err != nil {
			return featherlessCatalogMsg{err: err}
		}
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
		// A pick made mid-add moves on to naming; applyFeatherlessPick sets
		// that mode itself, so only an edit of an existing profile returns to
		// browsing here.
		adding := m.subscriptionModal.draft.file == ""
		m.applyFeatherlessPick(model)
		if !adding {
			m.subscriptionModal.mode = subscriptionBrowse
		}
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
	// Mid-add there is no profile to write onto yet, so the pick is held and
	// applied by addSubscriptionProfile once the file exists.
	if m.subscriptionModal.draft.file == "" {
		m.subscriptionModal.pendingModel = &model
		m.subscriptionModal.input = m.newSubscriptionNameInput(featherlessProfileName(model.ID))
		m.enterSubscriptionLifecycle(subscriptionAddName)
		m.subscriptionModal.err = nil
		return
	}
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
		// A window this small still runs, just not with MCP servers loaded, so
		// the row says what the choice costs instead of hiding the model.
		if !claudeconfig.WindowFitsMCP(model.Context) {
			row += "  (needs MCP off)"
		}
		row = modalTruncate(row, width-2)
		// ▌ is the modal's cursor marker everywhere else, and the query field
		// above already renders a "> " prompt of its own.
		switch {
		case i == picker.cursor:
			rows = append(rows, accent.Render("▌")+" "+accent.Render(row))
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

// featherlessProfileName derives a profile name from a model id. Ids are
// namespaced (owner/name) and the owner is noise in a profile list.
func featherlessProfileName(modelID string) string {
	name := modelID
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	return "Featherless " + name
}

// applyPendingSubscriptionModel writes a model picked before the profile
// existed. The window comes from the catalog for the same reason it does on an
// edit, and a sibling profile's key is reused so a second model on the same
// account is not a second key to type.
func (m *MainMenuModel) applyPendingSubscriptionModel(file string) error {
	pick := m.subscriptionModal.pendingModel
	if pick == nil {
		return nil
	}
	// A Featherless model id means nothing to another gateway, and its key even
	// less. Only the provider the pick was made for may receive it.
	if provider, ok := claudeconfig.ProviderByKey(m.subscriptionModal.providerKey); !ok ||
		!provider.RemoteCatalog {
		m.subscriptionModal.pendingModel = nil
		return nil
	}
	if err := claudeconfig.WriteCustomModel(m.claudeConfigsDir, file, pick.ID); err != nil {
		return err
	}
	if err := claudeconfig.WriteCustomContextWindow(
		m.claudeConfigsDir, file, fmt.Sprintf("%d", pick.Context)); err != nil {
		return err
	}
	if err := claudeconfig.WriteImagesBlocked(m.claudeConfigsDir, file, !pick.ImageInput); err != nil {
		return err
	}
	if key := m.featherlessKey(); key != "" {
		if err := claudeconfig.WriteAPIKey(m.claudeConfigsDir, file, key); err != nil {
			return err
		}
	}
	m.subscriptionModal.pendingModel = nil
	return nil
}

// abandonSubscriptionNameInput closes the name prompt and drops any model
// picked for the profile it would have created. Left set, the pick is applied
// to whatever profile is added next.
func (m *MainMenuModel) abandonSubscriptionNameInput() {
	m.subscriptionModal.mode = subscriptionBrowse
	m.subscriptionModal.input.Blur()
	m.subscriptionModal.pendingModel = nil
}
