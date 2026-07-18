package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/jackuait/wisp-deck/internal/claudeconfig"
	"github.com/mattn/go-runewidth"
)

const (
	subscriptionModalMaxWidth = 92
	subscriptionModalMinWide  = 64
	subscriptionListWidth     = 28
	subscriptionModalHeight   = 22
)

const (
	subscriptionDetailOpus = iota
	subscriptionDetailSonnet
	subscriptionDetailHaiku
	subscriptionDetailFable
	subscriptionDetailAuth
	subscriptionDetailUse
	subscriptionDetailRename
	subscriptionDetailDelete
	subscriptionDetailSave
)

type subscriptionPane int

const (
	subscriptionProfilesPane subscriptionPane = iota
	subscriptionDetailsPane
)

type subscriptionModalMode int

const (
	subscriptionBrowse subscriptionModalMode = iota
	subscriptionEditKey
	subscriptionAddProvider
	subscriptionAddName
	subscriptionRename
	subscriptionDeleteConfirm
	subscriptionDiscardConfirm
)

type subscriptionHitKind int

const (
	subscriptionHitNone subscriptionHitKind = iota
	subscriptionHitProfile
	subscriptionHitAdd
	subscriptionHitMapping
	subscriptionHitAuth
	subscriptionHitUse
	subscriptionHitSave
	subscriptionHitRename
	subscriptionHitDelete
	subscriptionHitBack
	subscriptionHitProvider
	subscriptionHitConfirm
	subscriptionHitCancel
)

type subscriptionHitTarget struct {
	kind  subscriptionHitKind
	index int
}

type subscriptionDraft struct {
	file      string
	models    []string
	mappings  [4]int
	apiKey    string
	keyEdited bool
	dirty     bool
}

type subscriptionModalState struct {
	open           bool
	pane           subscriptionPane
	mode           subscriptionModalMode
	profileCursor  int
	detailCursor   int
	profileOffset  int
	detailOffset   int
	draft          subscriptionDraft
	input          textinput.Model
	providerKey    string
	providerCursor int
	pendingProfile int
	pendingMode    subscriptionModalMode
	pendingClose   bool
	hover          subscriptionHitTarget
	err            error
}

type subscriptionProfile struct {
	Name     string
	File     string
	Provider claudeconfig.Provider
	Standard bool
	Active   bool
	Ready    bool
}

func (m *MainMenuModel) subscriptionProfiles() []subscriptionProfile {
	active := m.CurrentClaudeConfigFile()
	profiles := []subscriptionProfile{{
		Name:     "Standard Claude",
		Provider: claudeconfig.Provider{Name: "Anthropic / Claude"},
		Standard: true,
		Active:   active == "",
		Ready:    true,
	}}
	for _, config := range m.claudeConfigs {
		profiles = append(profiles, subscriptionProfile{
			Name:     config.Name,
			File:     config.File,
			Provider: m.claudeConfigProvider(config),
			Active:   config.File == active,
			Ready:    m.configReady(config),
		})
	}
	return profiles
}

func (m *MainMenuModel) subscriptionModalProfile() subscriptionProfile {
	profiles := m.subscriptionProfiles()
	if len(profiles) == 0 {
		return subscriptionProfile{}
	}
	cursor := m.subscriptionModal.profileCursor
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(profiles) {
		cursor = len(profiles) - 1
	}
	return profiles[cursor]
}

func (m *MainMenuModel) subscriptionModalOnAddRow() bool {
	return m.subscriptionModal.profileCursor >= len(m.subscriptionProfiles())
}

func (m *MainMenuModel) openSubscriptionModal() {
	active := m.CurrentClaudeConfigFile()
	m.subscriptionModal = subscriptionModalState{
		open:           true,
		pane:           subscriptionProfilesPane,
		mode:           subscriptionBrowse,
		profileCursor:  0,
		pendingProfile: -1,
	}
	for i, profile := range m.subscriptionProfiles() {
		if profile.File == active {
			m.subscriptionModal.profileCursor = i
			break
		}
	}
	m.loadSubscriptionDraft(m.subscriptionModalProfile())
	m.ensureSubscriptionProfileVisible()
}

func (m *MainMenuModel) updateSubscriptionModal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.subscriptionModal.mode == subscriptionEditKey {
		return m.updateSubscriptionKeyInput(msg)
	}
	if m.subscriptionModal.mode == subscriptionAddProvider {
		return m.updateSubscriptionProviderPicker(msg)
	}
	if m.subscriptionModal.mode == subscriptionAddName || m.subscriptionModal.mode == subscriptionRename {
		return m.updateSubscriptionNameInput(msg)
	}
	if m.subscriptionModal.mode == subscriptionDeleteConfirm {
		switch msg.Type {
		case tea.KeyEnter:
			m.deleteSubscriptionProfile()
		case tea.KeyEsc, tea.KeyCtrlC:
			m.subscriptionModal.mode = subscriptionBrowse
		}
		return m, nil
	}
	if m.subscriptionModal.mode == subscriptionDiscardConfirm {
		switch msg.Type {
		case tea.KeyEnter:
			if m.subscriptionModal.pendingClose {
				m.subscriptionModal.open = false
				return m, nil
			}
			pendingProfile := m.subscriptionModal.pendingProfile
			pendingMode := m.subscriptionModal.pendingMode
			m.subscriptionModal.mode = subscriptionBrowse
			m.subscriptionModal.pendingProfile = -1
			m.subscriptionModal.pendingMode = subscriptionBrowse
			m.subscriptionModal.pendingClose = false
			if pendingProfile >= 0 {
				m.subscriptionModal.profileCursor = pendingProfile
			}
			m.loadSubscriptionDraft(m.subscriptionModalProfile())
			m.ensureSubscriptionProfileVisible()
			switch pendingMode {
			case subscriptionAddProvider:
				m.startSubscriptionAdd()
			case subscriptionRename:
				m.startSubscriptionRename()
			case subscriptionDeleteConfirm:
				m.startSubscriptionDelete()
			}
		case tea.KeyEsc, tea.KeyCtrlC:
			m.subscriptionModal.mode = subscriptionBrowse
			m.subscriptionModal.pendingProfile = -1
			m.subscriptionModal.pendingMode = subscriptionBrowse
			m.subscriptionModal.pendingClose = false
		}
		return m, nil
	}

	switch msg.Type {
	case tea.KeyEsc:
		if m.subscriptionModalCompact() &&
			m.subscriptionModal.pane == subscriptionDetailsPane {
			m.subscriptionModal.pane = subscriptionProfilesPane
			return m, nil
		}
		if m.subscriptionModal.draft.dirty {
			m.subscriptionModal.mode = subscriptionDiscardConfirm
			m.subscriptionModal.pendingClose = true
			return m, nil
		}
		m.subscriptionModal.open = false
	case tea.KeyCtrlC:
		if m.subscriptionModal.draft.dirty {
			m.subscriptionModal.mode = subscriptionDiscardConfirm
			m.subscriptionModal.pendingClose = true
			return m, nil
		}
		m.subscriptionModal.open = false
	case tea.KeyTab, tea.KeyShiftTab:
		if m.subscriptionModal.pane == subscriptionProfilesPane {
			m.subscriptionModal.pane = subscriptionDetailsPane
			m.ensureSubscriptionDetailVisible()
		} else {
			m.subscriptionModal.pane = subscriptionProfilesPane
		}
	case tea.KeyUp:
		if m.subscriptionModal.pane == subscriptionDetailsPane {
			m.moveSubscriptionDetail(-1)
		} else {
			m.moveSubscriptionProfile(-1)
		}
	case tea.KeyDown:
		if m.subscriptionModal.pane == subscriptionDetailsPane {
			m.moveSubscriptionDetail(1)
		} else {
			m.moveSubscriptionProfile(1)
		}
	case tea.KeyRight:
		if m.subscriptionModal.pane == subscriptionDetailsPane {
			if !m.moveSubscriptionAction(1) {
				m.cycleSubscriptionMapping("next")
			}
		} else {
			m.subscriptionModal.pane = subscriptionDetailsPane
			m.ensureSubscriptionDetailVisible()
		}
	case tea.KeyLeft:
		if m.subscriptionModal.pane == subscriptionDetailsPane &&
			m.moveSubscriptionAction(-1) {
			break
		}
		if m.subscriptionModal.pane == subscriptionDetailsPane {
			m.subscriptionModal.pane = subscriptionProfilesPane
		}
	case tea.KeyEnter:
		if m.subscriptionModalOnAddRow() {
			m.startSubscriptionAdd()
		} else if m.subscriptionModal.pane == subscriptionProfilesPane &&
			m.subscriptionModalCompact() {
			m.subscriptionModal.pane = subscriptionDetailsPane
			m.ensureSubscriptionDetailVisible()
		} else if m.subscriptionModal.pane == subscriptionDetailsPane {
			return m.activateSubscriptionDetail()
		}
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			switch TranslateRune(msg.Runes[0]) {
			case 'k':
				if m.subscriptionModal.pane == subscriptionDetailsPane {
					m.moveSubscriptionDetail(-1)
				} else {
					m.moveSubscriptionProfile(-1)
				}
			case 'j':
				if m.subscriptionModal.pane == subscriptionDetailsPane {
					m.moveSubscriptionDetail(1)
				} else {
					m.moveSubscriptionProfile(1)
				}
			case 'u':
				m.useSubscriptionProfile()
			case 's':
				m.saveSubscriptionDraft()
			case 'e':
				return m, m.beginSubscriptionKeyEdit()
			case 'a':
				m.startSubscriptionAdd()
			case 'r':
				m.startSubscriptionRename()
			case 'd':
				m.startSubscriptionDelete()
			}
		}
	}
	return m, nil
}

func (m *MainMenuModel) moveSubscriptionProfile(delta int) {
	if m.subscriptionModal.pane != subscriptionProfilesPane {
		return
	}
	last := len(m.subscriptionProfiles()) // final row is Add profile
	next := m.subscriptionModal.profileCursor + delta
	if next < 0 {
		next = 0
	}
	if next > last {
		next = last
	}
	m.selectSubscriptionProfile(next)
}

func (m *MainMenuModel) selectSubscriptionProfile(next int) {
	last := len(m.subscriptionProfiles())
	if next < 0 {
		next = 0
	}
	if next > last {
		next = last
	}
	if next == m.subscriptionModal.profileCursor {
		return
	}
	if m.subscriptionModal.draft.dirty {
		m.subscriptionModal.mode = subscriptionDiscardConfirm
		m.subscriptionModal.pendingProfile = next
		m.subscriptionModal.pendingMode = subscriptionBrowse
		m.subscriptionModal.pendingClose = false
		return
	}
	m.subscriptionModal.profileCursor = next
	if next == len(m.subscriptionProfiles()) {
		m.subscriptionModal.draft = subscriptionDraft{}
		m.subscriptionModal.err = nil
		m.ensureSubscriptionProfileVisible()
		return
	}
	m.loadSubscriptionDraft(m.subscriptionModalProfile())
	m.ensureSubscriptionProfileVisible()
}

func (m *MainMenuModel) subscriptionDetailRows() []int {
	if m.subscriptionModalOnAddRow() {
		return nil
	}
	profile := m.subscriptionModalProfile()
	if profile.Standard {
		return []int{subscriptionDetailUse}
	}
	rows := []int{
		subscriptionDetailOpus,
		subscriptionDetailSonnet,
		subscriptionDetailHaiku,
		subscriptionDetailFable,
	}
	if profile.Provider.Auth == claudeconfig.AuthAPIKey {
		rows = append(rows, subscriptionDetailAuth)
	}
	return append(rows, subscriptionDetailUse)
}

func (m *MainMenuModel) moveSubscriptionDetail(delta int) {
	rows := m.subscriptionDetailRows()
	if len(rows) == 0 {
		return
	}
	if m.subscriptionModal.detailCursor >= subscriptionDetailRename &&
		m.subscriptionModal.detailCursor <= subscriptionDetailSave {
		if delta >= 0 {
			return
		}
		m.subscriptionModal.detailCursor = subscriptionDetailUse
	}
	position := 0
	for i, row := range rows {
		if row == m.subscriptionModal.detailCursor {
			position = i
			break
		}
	}
	position += delta
	if position < 0 {
		position = 0
	}
	if position >= len(rows) {
		position = len(rows) - 1
	}
	m.subscriptionModal.detailCursor = rows[position]
	m.ensureSubscriptionDetailVisible()
}

func (m *MainMenuModel) moveSubscriptionAction(delta int) bool {
	if m.subscriptionModalOnAddRow() || m.subscriptionModalProfile().Standard {
		return false
	}
	actions := [...]int{
		subscriptionDetailUse,
		subscriptionDetailRename,
		subscriptionDetailDelete,
		subscriptionDetailSave,
	}
	position := -1
	for i, action := range actions {
		if action == m.subscriptionModal.detailCursor {
			position = i
			break
		}
	}
	if position < 0 {
		return false
	}
	next := position + delta
	if next < 0 || next >= len(actions) {
		return false
	}
	m.subscriptionModal.detailCursor = actions[next]
	m.ensureSubscriptionDetailVisible()
	return true
}

func (m *MainMenuModel) loadSubscriptionDraft(profile subscriptionProfile) {
	draft := subscriptionDraft{file: profile.File}
	for i := range draft.mappings {
		draft.mappings[i] = -1
	}
	if !profile.Standard {
		draft.models = append([]string(nil), claudeconfig.ProviderModels[profile.Provider.Key]...)
		draft.mappings = claudeconfig.ReadModelMappings(m.claudeConfigsDir, profile.File, draft.models)
		if profile.Provider.Auth == claudeconfig.AuthAPIKey {
			draft.apiKey = claudeconfig.ReadAPIKey(m.claudeConfigsDir, profile.File)
		}
	}
	m.subscriptionModal.draft = draft
	if profile.Standard {
		m.subscriptionModal.detailCursor = subscriptionDetailUse
	} else {
		m.subscriptionModal.detailCursor = subscriptionDetailOpus
	}
	m.subscriptionModal.detailOffset = 0
	m.subscriptionModal.err = nil
}

func (m *MainMenuModel) useSubscriptionProfile() {
	if m.subscriptionModalOnAddRow() {
		return
	}
	profile := m.subscriptionModalProfile()
	if !profile.Ready {
		m.subscriptionModal.err = fmt.Errorf("%s needs an API key before it can be used", profile.Name)
		return
	}
	if err := claudeconfig.SetActive(m.claudeConfigFile, profile.File); err != nil {
		m.subscriptionModal.err = err
		return
	}
	m.SetActiveClaudeConfig(profile.File)
	m.subscriptionModal.err = nil
	m.syncOpenCode()
}

func (m *MainMenuModel) cycleSubscriptionMapping(direction string) {
	if m.subscriptionModalOnAddRow() {
		return
	}
	profile := m.subscriptionModalProfile()
	cursor := m.subscriptionModal.detailCursor
	if profile.Standard || cursor < subscriptionDetailOpus || cursor > subscriptionDetailFable {
		return
	}
	n := len(m.subscriptionModal.draft.models)
	if n == 0 {
		return
	}
	current := m.subscriptionModal.draft.mappings[cursor]
	if direction == "prev" {
		if current <= -1 {
			current = n - 1
		} else {
			current--
		}
	} else {
		if current >= n-1 {
			current = -1
		} else {
			current++
		}
	}
	m.subscriptionModal.draft.mappings[cursor] = current
	m.subscriptionModal.draft.dirty = true
	m.subscriptionModal.err = nil
}

func (m *MainMenuModel) saveSubscriptionDraft() {
	if m.subscriptionModalOnAddRow() {
		return
	}
	profile := m.subscriptionModalProfile()
	draft := &m.subscriptionModal.draft
	if profile.Standard || draft.file == "" || !draft.dirty {
		return
	}
	if err := claudeconfig.WriteModelMappings(m.claudeConfigsDir, draft.file, draft.mappings, draft.models); err != nil {
		m.subscriptionModal.err = err
		return
	}
	if draft.keyEdited {
		if err := claudeconfig.WriteAPIKey(m.claudeConfigsDir, draft.file, strings.TrimSpace(draft.apiKey)); err != nil {
			draft.mappings = claudeconfig.ReadModelMappings(m.claudeConfigsDir, draft.file, draft.models)
			m.subscriptionModal.err = err
			return
		}
	}
	draft.apiKey = claudeconfig.ReadAPIKey(m.claudeConfigsDir, draft.file)
	draft.keyEdited = false
	draft.dirty = false
	m.subscriptionModal.err = nil
	m.syncOpenCode()
}

func (m *MainMenuModel) beginSubscriptionKeyEdit() tea.Cmd {
	if m.subscriptionModalOnAddRow() {
		return nil
	}
	profile := m.subscriptionModalProfile()
	if profile.Standard || profile.Provider.Auth != claudeconfig.AuthAPIKey {
		return nil
	}
	input := textinput.New()
	input.Width = 32
	input.Placeholder = "API key"
	input.EchoMode = textinput.EchoPassword
	input.EchoCharacter = '•'
	input.SetValue(m.subscriptionModal.draft.apiKey)
	input.Focus()
	m.subscriptionModal.input = input
	m.subscriptionModal.mode = subscriptionEditKey
	m.subscriptionModal.err = nil
	return textinput.Blink
}

func (m *MainMenuModel) updateSubscriptionKeyInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.subscriptionModal.mode = subscriptionBrowse
		m.subscriptionModal.input.Blur()
		return m, nil
	case tea.KeyEnter:
		key := strings.TrimSpace(m.subscriptionModal.input.Value())
		if key != m.subscriptionModal.draft.apiKey {
			m.subscriptionModal.draft.apiKey = key
			m.subscriptionModal.draft.keyEdited = true
			m.subscriptionModal.draft.dirty = true
		}
		m.subscriptionModal.mode = subscriptionBrowse
		m.subscriptionModal.input.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.subscriptionModal.input, cmd = m.subscriptionModal.input.Update(msg)
	return m, cmd
}

func (m *MainMenuModel) activateSubscriptionDetail() (tea.Model, tea.Cmd) {
	switch m.subscriptionModal.detailCursor {
	case subscriptionDetailOpus, subscriptionDetailSonnet, subscriptionDetailHaiku, subscriptionDetailFable:
		m.cycleSubscriptionMapping("next")
	case subscriptionDetailAuth:
		return m, m.beginSubscriptionKeyEdit()
	case subscriptionDetailUse:
		m.useSubscriptionProfile()
	case subscriptionDetailRename:
		m.startSubscriptionRename()
	case subscriptionDetailDelete:
		m.startSubscriptionDelete()
	case subscriptionDetailSave:
		m.saveSubscriptionDraft()
	}
	return m, nil
}

func (m *MainMenuModel) startSubscriptionAdd() {
	if m.subscriptionModal.draft.dirty {
		m.subscriptionModal.mode = subscriptionDiscardConfirm
		m.subscriptionModal.pendingProfile = -1
		m.subscriptionModal.pendingMode = subscriptionAddProvider
		m.subscriptionModal.pendingClose = false
		return
	}
	m.subscriptionModal.mode = subscriptionAddProvider
	m.subscriptionModal.providerCursor = 0
	m.subscriptionModal.pane = subscriptionDetailsPane
	m.subscriptionModal.err = nil
}

func (m *MainMenuModel) updateSubscriptionProviderPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.subscriptionModal.mode = subscriptionBrowse
		m.subscriptionModal.pane = subscriptionProfilesPane
	case tea.KeyUp:
		if m.subscriptionModal.providerCursor > 0 {
			m.subscriptionModal.providerCursor--
		}
	case tea.KeyDown:
		if m.subscriptionModal.providerCursor < len(claudeconfig.Providers)-1 {
			m.subscriptionModal.providerCursor++
		}
	case tea.KeyEnter:
		return m, m.beginSubscriptionAddName()
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			switch TranslateRune(msg.Runes[0]) {
			case 'k':
				if m.subscriptionModal.providerCursor > 0 {
					m.subscriptionModal.providerCursor--
				}
			case 'j':
				if m.subscriptionModal.providerCursor < len(claudeconfig.Providers)-1 {
					m.subscriptionModal.providerCursor++
				}
			}
		}
	}
	return m, nil
}

func (m *MainMenuModel) newSubscriptionNameInput(value string) textinput.Model {
	input := textinput.New()
	input.Width = 36
	input.Placeholder = "Profile name"
	input.SetValue(value)
	input.Focus()
	return input
}

func (m *MainMenuModel) beginSubscriptionAddName() tea.Cmd {
	if m.subscriptionModal.providerCursor < 0 ||
		m.subscriptionModal.providerCursor >= len(claudeconfig.Providers) {
		return nil
	}
	provider := claudeconfig.Providers[m.subscriptionModal.providerCursor]
	m.subscriptionModal.providerKey = provider.Key
	m.subscriptionModal.input = m.newSubscriptionNameInput(provider.Name)
	m.subscriptionModal.mode = subscriptionAddName
	m.subscriptionModal.err = nil
	return textinput.Blink
}

func (m *MainMenuModel) startSubscriptionRename() {
	profile := m.subscriptionModalProfile()
	if profile.Standard || profile.File == "" ||
		m.subscriptionModal.profileCursor >= len(m.subscriptionProfiles()) {
		return
	}
	if m.subscriptionModal.draft.dirty {
		m.subscriptionModal.mode = subscriptionDiscardConfirm
		m.subscriptionModal.pendingProfile = -1
		m.subscriptionModal.pendingMode = subscriptionRename
		m.subscriptionModal.pendingClose = false
		return
	}
	m.subscriptionModal.input = m.newSubscriptionNameInput(profile.Name)
	m.subscriptionModal.mode = subscriptionRename
	m.subscriptionModal.pane = subscriptionDetailsPane
	m.subscriptionModal.err = nil
}

func (m *MainMenuModel) updateSubscriptionNameInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.subscriptionModal.mode = subscriptionBrowse
		m.subscriptionModal.input.Blur()
		return m, nil
	case tea.KeyEnter:
		name := strings.TrimSpace(m.subscriptionModal.input.Value())
		if name == "" {
			m.subscriptionModal.err = fmt.Errorf("profile name cannot be empty")
			return m, nil
		}
		if m.subscriptionModal.mode == subscriptionAddName {
			m.addSubscriptionProfile(name)
		} else {
			m.renameSubscriptionProfile(name)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.subscriptionModal.input, cmd = m.subscriptionModal.input.Update(msg)
	return m, cmd
}

func (m *MainMenuModel) reloadSubscriptionConfigs() {
	m.claudeConfigs = LoadClaudeConfigsList(m.claudeConfigsList)
	m.SetActiveClaudeConfig(claudeconfig.GetActive(m.claudeConfigFile))
}

func (m *MainMenuModel) focusSubscriptionFile(file string) {
	m.subscriptionModal.profileCursor = 0
	for i, profile := range m.subscriptionProfiles() {
		if profile.File == file {
			m.subscriptionModal.profileCursor = i
			break
		}
	}
	m.loadSubscriptionDraft(m.subscriptionModalProfile())
	m.ensureSubscriptionProfileVisible()
}

func (m *MainMenuModel) addSubscriptionProfile(name string) {
	file, err := claudeconfig.AddForProvider(
		m.claudeConfigsList,
		m.claudeConfigsDir,
		name,
		m.subscriptionModal.providerKey,
	)
	if err != nil {
		m.subscriptionModal.err = err
		return
	}
	m.reloadSubscriptionConfigs()
	m.focusSubscriptionFile(file)
	m.subscriptionModal.mode = subscriptionBrowse
	m.subscriptionModal.pane = subscriptionProfilesPane
	m.subscriptionModal.input.Blur()
	m.subscriptionModal.err = nil
	m.syncOpenCode()
}

func (m *MainMenuModel) renameSubscriptionProfile(name string) {
	profile := m.subscriptionModalProfile()
	if profile.Standard || profile.File == "" {
		return
	}
	marker := claudeconfig.ReadProviderMarker(m.claudeConfigsDir, profile.File)
	if _, marked := claudeconfig.ProviderByKey(marker); !marked {
		if err := claudeconfig.WriteProviderMarker(
			m.claudeConfigsDir,
			profile.File,
			profile.Provider.Key,
		); err != nil {
			m.subscriptionModal.err = err
			return
		}
	}
	if err := claudeconfig.Rename(m.claudeConfigsList, profile.File, name); err != nil {
		m.subscriptionModal.err = err
		return
	}
	m.reloadSubscriptionConfigs()
	m.focusSubscriptionFile(profile.File)
	m.subscriptionModal.mode = subscriptionBrowse
	m.subscriptionModal.input.Blur()
	m.subscriptionModal.err = nil
	m.syncOpenCode()
}

func (m *MainMenuModel) startSubscriptionDelete() {
	profile := m.subscriptionModalProfile()
	if profile.Standard || profile.File == "" ||
		m.subscriptionModal.profileCursor >= len(m.subscriptionProfiles()) {
		return
	}
	if m.subscriptionModal.draft.dirty {
		m.subscriptionModal.mode = subscriptionDiscardConfirm
		m.subscriptionModal.pendingProfile = -1
		m.subscriptionModal.pendingMode = subscriptionDeleteConfirm
		m.subscriptionModal.pendingClose = false
		return
	}
	m.subscriptionModal.mode = subscriptionDeleteConfirm
	m.subscriptionModal.pane = subscriptionDetailsPane
	m.subscriptionModal.err = nil
}

func (m *MainMenuModel) deleteSubscriptionProfile() {
	profile := m.subscriptionModalProfile()
	if profile.Standard || profile.File == "" {
		m.subscriptionModal.mode = subscriptionBrowse
		return
	}
	wasActive := profile.Active
	oldCursor := m.subscriptionModal.profileCursor
	if err := claudeconfig.Delete(
		m.claudeConfigsList,
		m.claudeConfigsDir,
		m.claudeConfigFile,
		profile.File,
	); err != nil {
		m.subscriptionModal.err = err
		return
	}
	m.reloadSubscriptionConfigs()
	if wasActive {
		m.focusSubscriptionFile("")
	} else {
		profiles := m.subscriptionProfiles()
		if oldCursor >= len(profiles) {
			oldCursor = len(profiles) - 1
		}
		m.subscriptionModal.profileCursor = oldCursor
		m.loadSubscriptionDraft(m.subscriptionModalProfile())
	}
	m.subscriptionModal.mode = subscriptionBrowse
	m.subscriptionModal.pane = subscriptionProfilesPane
	m.subscriptionModal.err = nil
	m.syncOpenCode()
}

func (m *MainMenuModel) subscriptionLifecycleLines(width, height int) []string {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	accent := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	danger := lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	action := func(kind subscriptionHitKind, text string, style lipgloss.Style) string {
		if m.subscriptionModal.hover.kind == kind {
			return style.Reverse(true).Render(text)
		}
		return style.Render(text)
	}
	buttons := func(confirm string, confirmStyle lipgloss.Style, cancel string) string {
		line := ""
		if confirm != "" {
			line = action(subscriptionHitConfirm, confirm, confirmStyle) + "  "
		}
		return line + action(subscriptionHitCancel, cancel, dim)
	}
	var lines []string
	switch m.subscriptionModal.mode {
	case subscriptionEditKey:
		lines = append(lines,
			dim.Bold(true).Render("EDIT API KEY"),
			"",
			"API key",
			m.subscriptionModal.input.View(),
			"",
			buttons("[ Keep changes ]", accent, "[ Cancel ]"),
		)
	case subscriptionAddProvider:
		lines = append(lines, dim.Bold(true).Render("CHOOSE PROVIDER"), "")
		for i, provider := range claudeconfig.Providers {
			marker := "  "
			style := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
			if i == m.subscriptionModal.providerCursor {
				marker = accent.Render("▌") + " "
				style = accent
			}
			lines = append(lines, marker+style.Render(provider.Name))
		}
		lines = append(lines, "", action(subscriptionHitCancel, "[ Cancel ]", dim))
	case subscriptionAddName:
		provider, _ := claudeconfig.ProviderByKey(m.subscriptionModal.providerKey)
		lines = append(lines,
			dim.Bold(true).Render("NEW "+strings.ToUpper(provider.Name)+" PROFILE"),
			"",
			"Profile name",
			m.subscriptionModal.input.View(),
			"",
			buttons("[ Create ]", accent, "[ Cancel ]"),
		)
	case subscriptionRename:
		lines = append(lines,
			dim.Bold(true).Render("RENAME PROFILE"),
			"",
			"Profile name",
			m.subscriptionModal.input.View(),
			"",
			buttons("[ Rename ]", accent, "[ Cancel ]"),
		)
	case subscriptionDeleteConfirm:
		profile := m.subscriptionModalProfile()
		lines = append(lines,
			danger.Render("DELETE "+strings.ToUpper(profile.Name)+"?"),
			"",
			"This removes the profile settings file.",
			"",
			buttons("[ Delete ]", danger, "[ Cancel ]"),
		)
	case subscriptionDiscardConfirm:
		lines = append(lines,
			danger.Render("DISCARD UNSAVED CHANGES?"),
			"",
			"Your saved profile will stay unchanged.",
			"",
			buttons("[ Discard ]", danger, "[ Keep editing ]"),
		)
	}
	if m.subscriptionModal.err != nil {
		lines = append(lines, "", danger.Render(modalTruncate(m.subscriptionModal.err.Error(), width)))
	}
	return modalWindow(lines, 0, height, width)
}

func (m *MainMenuModel) subscriptionModalCompact() bool {
	_, _, width, _ := m.subscriptionModalLayout()
	return width < subscriptionModalMinWide
}

func (m *MainMenuModel) subscriptionModalLayout() (left, top, width, height int) {
	width = subscriptionModalMaxWidth
	if m.width > 0 && width > m.width-4 {
		width = m.width - 4
	}
	if width < 1 {
		width = 1
	}

	height = subscriptionModalHeight
	if m.height > 0 && height > m.height-4 {
		height = m.height - 4
	}
	if height < 6 {
		height = 6
	}
	if m.height > 0 && height > m.height {
		height = m.height
	}

	left = (m.width - width) / 2
	if left < 0 {
		left = 0
	}
	top = (m.height - height) / 2
	if top < 0 {
		top = 0
	}
	return left, top, width, height
}

func (m *MainMenuModel) subscriptionModalBodyHeight() int {
	_, _, _, height := m.subscriptionModalLayout()
	height -= 4
	if height < 1 {
		return 1
	}
	return height
}

func (m *MainMenuModel) subscriptionDetailPaneWidth() int {
	_, _, width, _ := m.subscriptionModalLayout()
	width -= 2 // card borders
	if !m.subscriptionModalCompact() {
		width -= subscriptionListWidth + 1 // profile pane and divider
	}
	if width < 1 {
		return 1
	}
	return width
}

func subscriptionActionsFitOneLine(width int) bool {
	const actions = "[ Use profile ]  [ Rename ]  [ Delete ]  [ Save changes ]"
	return width >= lipgloss.Width(actions)
}

func (m *MainMenuModel) ensureSubscriptionProfileVisible() {
	viewport := m.subscriptionModalBodyHeight() - 1 // fixed PROFILES heading
	if viewport < 1 {
		viewport = 1
	}
	itemCount := len(m.subscriptionProfiles()) + 1 // Add profile
	cursor := m.subscriptionModal.profileCursor
	if cursor < m.subscriptionModal.profileOffset {
		m.subscriptionModal.profileOffset = cursor
	}
	if cursor >= m.subscriptionModal.profileOffset+viewport {
		m.subscriptionModal.profileOffset = cursor - viewport + 1
	}
	maxOffset := itemCount - viewport
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.subscriptionModal.profileOffset > maxOffset {
		m.subscriptionModal.profileOffset = maxOffset
	}
	if m.subscriptionModal.profileOffset < 0 {
		m.subscriptionModal.profileOffset = 0
	}
}

func (m *MainMenuModel) subscriptionDetailCursorLine() int {
	if m.subscriptionModalOnAddRow() {
		return 0
	}
	profile := m.subscriptionModalProfile()
	if profile.Standard {
		return 7
	}
	cursor := m.subscriptionModal.detailCursor
	if cursor >= subscriptionDetailOpus && cursor <= subscriptionDetailFable {
		return 8 + cursor
	}

	line := 12 // first line after the four model mappings
	if profile.Provider.Auth == claudeconfig.AuthAPIKey {
		if cursor == subscriptionDetailAuth {
			return line
		}
		line++
	}
	if m.subscriptionModal.draft.dirty {
		line++
	}
	if m.subscriptionModal.err != nil {
		line++
	}
	line++ // blank line before actions
	if subscriptionActionsFitOneLine(m.subscriptionDetailPaneWidth()) {
		return line
	}
	if m.subscriptionModalCompact() {
		if cursor == subscriptionDetailRename || cursor == subscriptionDetailDelete {
			return line + 1
		}
		if cursor == subscriptionDetailSave {
			return line + 2
		}
	} else if cursor == subscriptionDetailSave {
		return line + 1
	}
	return line
}

func (m *MainMenuModel) ensureSubscriptionDetailVisible() {
	viewport := m.subscriptionModalBodyHeight()
	line := m.subscriptionDetailCursorLine()
	if line < m.subscriptionModal.detailOffset {
		m.subscriptionModal.detailOffset = line
	}
	if line >= m.subscriptionModal.detailOffset+viewport {
		m.subscriptionModal.detailOffset = line - viewport + 1
	}
	if m.subscriptionModal.detailOffset < 0 {
		m.subscriptionModal.detailOffset = 0
	}
}

func modalTruncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return runewidth.Truncate(s, width, "…")
}

func modalPad(s string, width int) string {
	s = ansi.Truncate(s, width, "")
	gap := width - lipgloss.Width(s)
	if gap < 0 {
		gap = 0
	}
	return s + strings.Repeat(" ", gap)
}

func (m *MainMenuModel) subscriptionProfileLines(width, height int) []string {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	accent := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	green := lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	amber := lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	selectionColor := lipgloss.Color("236")
	selectionWash := lipgloss.NewStyle().Background(selectionColor)

	heading := modalPad(dim.Bold(true).Render("PROFILES"), width)
	var items []string
	profiles := m.subscriptionProfiles()
	for i, profile := range profiles {
		focused := i == m.subscriptionModal.profileCursor
		cursor := "  "
		if focused {
			cursor = accent.Render("▌") + " "
		} else if m.subscriptionModal.hover.kind == subscriptionHitProfile &&
			m.subscriptionModal.hover.index == i {
			cursor = dim.Render("▌") + " "
		}
		active := "  "
		if profile.Active {
			active = green.Render("●") + " "
		}
		statusText := "Ready"
		status := green.Render(statusText)
		if !profile.Ready {
			statusText = "Needs key"
			status = amber.Render(statusText)
		}
		nameWidth := width - lipgloss.Width(cursor) - lipgloss.Width(active) - lipgloss.Width(statusText) - 1
		if nameWidth < 1 {
			nameWidth = 1
		}
		name := modalTruncate(profile.Name, nameWidth)
		gap := width - lipgloss.Width(cursor) - lipgloss.Width(active) - lipgloss.Width(name) - lipgloss.Width(status)
		if gap < 1 {
			gap = 1
		}
		if focused {
			cursor = accent.Background(selectionColor).Render("▌") + selectionWash.Render(" ")
			active = selectionWash.Render("  ")
			if profile.Active {
				active = green.Background(selectionColor).Render("●") + selectionWash.Render(" ")
			}
			name = accent.Background(selectionColor).Render(name)
			statusStyle := green
			if !profile.Ready {
				statusStyle = amber
			}
			status = statusStyle.Background(selectionColor).Render(statusText)
			items = append(items, cursor+active+name+selectionWash.Render(strings.Repeat(" ", gap))+status)
			continue
		}
		items = append(items, cursor+active+name+strings.Repeat(" ", gap)+status)
	}

	add := "  + Add profile"
	if m.subscriptionModal.profileCursor == len(profiles) {
		label := "+ Add profile"
		padding := width - lipgloss.Width("▌ ") - lipgloss.Width(label)
		if padding < 0 {
			padding = 0
		}
		add = accent.Background(selectionColor).Render("▌") +
			selectionWash.Render(" ") +
			accent.Background(selectionColor).Render(label) +
			selectionWash.Render(strings.Repeat(" ", padding))
	} else if m.subscriptionModal.hover.kind == subscriptionHitAdd {
		add = dim.Render("▌") + " + Add profile"
	}
	items = append(items, add)
	if height <= 0 {
		return nil
	}
	return append([]string{heading}, modalWindow(items, m.subscriptionModal.profileOffset, height-1, width)...)
}

func modalWindow(lines []string, offset, height, width int) []string {
	if height < 0 {
		height = 0
	}
	maxOffset := len(lines) - height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset < 0 {
		offset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	end := offset + height
	if end > len(lines) {
		end = len(lines)
	}
	out := append([]string(nil), lines[offset:end]...)
	for len(out) < height {
		out = append(out, strings.Repeat(" ", width))
	}
	for i := range out {
		out[i] = modalPad(out[i], width)
	}
	return out
}

func (m *MainMenuModel) subscriptionDetailLines(width, height int) []string {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	label := lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	accent := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	green := lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	amber := lipgloss.NewStyle().Foreground(lipgloss.Color("220"))

	switch m.subscriptionModal.mode {
	case subscriptionEditKey, subscriptionAddProvider, subscriptionAddName,
		subscriptionRename, subscriptionDeleteConfirm, subscriptionDiscardConfirm:
		return m.subscriptionLifecycleLines(width, height)
	}

	if m.subscriptionModalOnAddRow() {
		lines := []string{
			dim.Bold(true).Render("ADD A PROFILE"),
			"",
			"Add a provider profile",
			dim.Render("Choose from every available provider and configure"),
			dim.Render("its authentication and model routes in this overlay."),
			"",
			accent.Render("[ Press Enter to choose provider ]"),
		}
		return modalWindow(lines, 0, height, width)
	}

	profile := m.subscriptionModalProfile()
	lines := []string{dim.Bold(true).Render("PROFILE DETAILS")}
	valueRow := func(row int, name, value string, style lipgloss.Style) string {
		const labelWidth = 15
		marker := "  "
		if m.subscriptionModal.mode == subscriptionBrowse &&
			m.subscriptionModal.pane == subscriptionDetailsPane &&
			m.subscriptionModal.detailCursor == row {
			marker = accent.Render("▌") + " "
		}
		valueWidth := width - labelWidth
		valueWidth -= lipgloss.Width(marker)
		if valueWidth < 1 {
			valueWidth = 1
		}
		return marker + label.Render(fmt.Sprintf("%-14s ", modalTruncate(name, 14))) +
			style.Render(modalTruncate(value, valueWidth))
	}

	lines = append(lines, valueRow(-1, "Profile", profile.Name, accent))
	if profile.Standard {
		lines = append(lines,
			valueRow(-1, "Provider", "Anthropic / Claude", green),
			valueRow(-1, "Authentication", "Claude Code login", green),
			"",
			dim.Render("Uses Claude Code's native models and account."),
			"",
		)
		use := m.subscriptionActionLabel(
			subscriptionHitUse,
			subscriptionDetailUse,
			"[ Use profile ]",
			accent,
			label,
		)
		disabled := lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
		lines = append(lines, m.subscriptionActionLines(
			width,
			use,
			disabled.Render("[ Rename ]"),
			disabled.Render("[ Delete ]"),
			disabled.Render("[ Save changes ]"),
		)...)
		return modalWindow(lines, m.subscriptionModal.detailOffset, height, width)
	}

	auth := "API key"
	authStyle := amber
	authState := "Needs key"
	endpoint := profile.Provider.BaseURL
	if configured := claudeconfig.ReadBaseURL(m.claudeConfigsDir, profile.File); configured != "" {
		endpoint = configured
	}
	if profile.Provider.Auth == claudeconfig.AuthCodexChatGPT {
		auth = "codex login"
		authStyle = green
		authState = "Ready"
		endpoint = "Local Codex bridge"
	} else if profile.Ready {
		authStyle = green
		authState = "Ready · API key"
	}
	lines = append(lines,
		valueRow(-1, "Provider", profile.Provider.Name, green),
		valueRow(-1, "Authentication", auth, authStyle),
		valueRow(-1, "Status", authState, authStyle),
		valueRow(-1, "Endpoint", endpoint, dim),
		"",
		dim.Bold(true).Render("MODEL ROUTING"),
	)

	models := claudeconfig.ProviderModels[profile.Provider.Key]
	mappings := claudeconfig.ReadModelMappings(m.claudeConfigsDir, profile.File, models)
	if m.subscriptionModal.draft.file == profile.File {
		models = m.subscriptionModal.draft.models
		mappings = m.subscriptionModal.draft.mappings
	}
	for i, alias := range claudeconfig.AnthropicAliases {
		value := "(none)"
		style := dim
		if mappings[i] >= 0 && mappings[i] < len(models) {
			value = models[mappings[i]]
			style = green
		}
		lines = append(lines, valueRow(i, strings.ToUpper(alias[:1])+alias[1:], "→ "+value, style))
	}
	if profile.Provider.Auth == claudeconfig.AuthAPIKey {
		keyStatus := "(not set)"
		keyStyle := amber
		if m.subscriptionModal.draft.apiKey != "" {
			keyStatus = "••••••••"
			keyStyle = green
		}
		lines = append(lines, valueRow(subscriptionDetailAuth, "API key", keyStatus, keyStyle))
	}
	if m.subscriptionModal.draft.dirty {
		lines = append(lines, amber.Render("• Unsaved changes"))
	}
	if m.subscriptionModal.err != nil {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("196")).
			Render(modalTruncate(m.subscriptionModal.err.Error(), width)))
	}
	lines = append(lines, "")
	use := m.subscriptionActionLabel(subscriptionHitUse, subscriptionDetailUse, "[ Use profile ]", accent, label)
	rename := m.subscriptionActionLabel(subscriptionHitRename, subscriptionDetailRename, "[ Rename ]", accent, label)
	deleteAction := m.subscriptionActionLabel(subscriptionHitDelete, subscriptionDetailDelete, "[ Delete ]", accent, label)
	save := m.subscriptionActionLabel(subscriptionHitSave, subscriptionDetailSave, "[ Save changes ]", accent, label)
	lines = append(lines, m.subscriptionActionLines(width, use, rename, deleteAction, save)...)
	return modalWindow(lines, m.subscriptionModal.detailOffset, height, width)
}

func (m *MainMenuModel) subscriptionActionLines(width int, use, rename, deleteAction, save string) []string {
	if subscriptionActionsFitOneLine(width) {
		return []string{use + "  " + rename + "  " + deleteAction + "  " + save}
	}
	if m.subscriptionModalCompact() {
		return []string{use, rename + "  " + deleteAction, save}
	}
	return []string{use + "  " + rename + "  " + deleteAction, save}
}

func (m *MainMenuModel) subscriptionLifecycleLabels() (confirm, cancel string) {
	switch m.subscriptionModal.mode {
	case subscriptionEditKey:
		return "[ Keep changes ]", "[ Cancel ]"
	case subscriptionAddProvider:
		return "", "[ Cancel ]"
	case subscriptionAddName:
		return "[ Create ]", "[ Cancel ]"
	case subscriptionRename:
		return "[ Rename ]", "[ Cancel ]"
	case subscriptionDeleteConfirm:
		return "[ Delete ]", "[ Cancel ]"
	case subscriptionDiscardConfirm:
		return "[ Discard ]", "[ Keep editing ]"
	default:
		return "", ""
	}
}

func (m *MainMenuModel) subscriptionActionLabel(
	kind subscriptionHitKind,
	row int,
	text string,
	accent,
	idle lipgloss.Style,
) string {
	selected := row >= 0 &&
		m.subscriptionModal.mode == subscriptionBrowse &&
		m.subscriptionModal.pane == subscriptionDetailsPane &&
		m.subscriptionModal.detailCursor == row
	if m.subscriptionModal.hover.kind == kind || selected {
		return accent.Reverse(true).Render(text)
	}
	return idle.Render(text)
}

func subscriptionVisibleSpan(styledLine, text string) (start, end int, ok bool) {
	plain := diffAnsiSeq.ReplaceAllString(styledLine, "")
	index := strings.Index(plain, text)
	if index < 0 {
		return 0, 0, false
	}
	start = lipgloss.Width(plain[:index])
	end = start + lipgloss.Width(text)
	return start, end, true
}

func (m *MainMenuModel) subscriptionModalTarget(cardX, cardY int) subscriptionHitTarget {
	card := strings.Split(m.renderSubscriptionModalCard(), "\n")
	if cardY < 0 || cardY >= len(card) || cardX < 0 ||
		!frameCellHasGlyph(card, cardX, cardY) {
		return subscriptionHitTarget{}
	}
	line := card[cardY]
	hitText := func(text string) bool {
		start, end, ok := subscriptionVisibleSpan(line, text)
		return ok && cardX >= start && cardX < end
	}

	if m.subscriptionModal.mode != subscriptionBrowse {
		confirm, cancel := m.subscriptionLifecycleLabels()
		if confirm != "" && hitText(confirm) {
			return subscriptionHitTarget{kind: subscriptionHitConfirm}
		}
		if cancel != "" && hitText(cancel) {
			return subscriptionHitTarget{kind: subscriptionHitCancel}
		}
	}

	if m.subscriptionModal.mode == subscriptionAddProvider {
		for i, provider := range claudeconfig.Providers {
			if hitText(provider.Name) {
				return subscriptionHitTarget{kind: subscriptionHitProvider, index: i}
			}
		}
		return subscriptionHitTarget{}
	}
	if m.subscriptionModal.mode != subscriptionBrowse {
		return subscriptionHitTarget{}
	}
	if m.subscriptionModal.pane == subscriptionDetailsPane {
		for _, text := range []string{"← back/previous", "←/Esc profiles"} {
			if hitText(text) {
				return subscriptionHitTarget{kind: subscriptionHitBack}
			}
		}
	}

	compact := m.subscriptionModalCompact()
	listVisible := !compact || m.subscriptionModal.pane == subscriptionProfilesPane
	listEnd := 1 + subscriptionListWidth
	if compact {
		_, _, width, _ := m.subscriptionModalLayout()
		listEnd = width - 1
	}
	if listVisible && cardX < listEnd {
		item := cardY - 2 + m.subscriptionModal.profileOffset
		profiles := m.subscriptionProfiles()
		if item >= 0 && item < len(profiles) && hitText(profiles[item].Name) {
			return subscriptionHitTarget{kind: subscriptionHitProfile, index: item}
		}
		if item == len(profiles) && hitText("+ Add profile") {
			return subscriptionHitTarget{kind: subscriptionHitAdd}
		}
		return subscriptionHitTarget{}
	}

	for i, alias := range claudeconfig.AnthropicAliases {
		display := strings.ToUpper(alias[:1]) + alias[1:]
		if hitText(display) {
			return subscriptionHitTarget{kind: subscriptionHitMapping, index: i}
		}
	}
	if hitText("API key") {
		return subscriptionHitTarget{kind: subscriptionHitAuth}
	}
	for _, button := range []struct {
		text string
		kind subscriptionHitKind
	}{
		{"[ Use profile ]", subscriptionHitUse},
		{"[ Save changes ]", subscriptionHitSave},
		{"[ Rename ]", subscriptionHitRename},
		{"[ Delete ]", subscriptionHitDelete},
	} {
		if hitText(button.text) {
			return subscriptionHitTarget{kind: button.kind}
		}
	}
	return subscriptionHitTarget{}
}

func (m *MainMenuModel) handleSubscriptionModalMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	left, top, width, height := m.subscriptionModalLayout()
	cardX, cardY := msg.X-left, msg.Y-top
	onCard := cardX >= 0 && cardX < width && cardY >= 0 && cardY < height

	if !onCard {
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if m.subscriptionModal.mode != subscriptionBrowse {
				return m, nil
			}
			if m.subscriptionModal.draft.dirty {
				m.subscriptionModal.mode = subscriptionDiscardConfirm
				m.subscriptionModal.pendingClose = true
			} else {
				m.subscriptionModal.open = false
			}
		}
		if msg.Action == tea.MouseActionMotion {
			m.subscriptionModal.hover = subscriptionHitTarget{}
		}
		return m, nil
	}

	if m.subscriptionModal.mode != subscriptionBrowse &&
		m.subscriptionModal.mode != subscriptionAddProvider {
		target := m.subscriptionModalTarget(cardX, cardY)
		if msg.Action == tea.MouseActionMotion {
			m.subscriptionModal.hover = target
			return m, nil
		}
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			switch target.kind {
			case subscriptionHitConfirm:
				return m.updateSubscriptionModal(tea.KeyMsg{Type: tea.KeyEnter})
			case subscriptionHitCancel:
				return m.updateSubscriptionModal(tea.KeyMsg{Type: tea.KeyEsc})
			}
		}
		return m, nil
	}

	if m.subscriptionModal.mode == subscriptionAddProvider {
		target := m.subscriptionModalTarget(cardX, cardY)
		if msg.Action == tea.MouseActionMotion {
			m.subscriptionModal.hover = target
			return m, nil
		}
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			switch target.kind {
			case subscriptionHitProvider:
				m.subscriptionModal.providerCursor = target.index
				return m, m.beginSubscriptionAddName()
			case subscriptionHitCancel:
				return m.updateSubscriptionModal(tea.KeyMsg{Type: tea.KeyEsc})
			}
		}
		return m, nil
	}

	switch msg.Button {
	case tea.MouseButtonWheelDown:
		if m.subscriptionModal.pane == subscriptionProfilesPane {
			m.moveSubscriptionProfile(1)
		} else {
			m.moveSubscriptionDetail(1)
		}
		return m, nil
	case tea.MouseButtonWheelUp:
		if m.subscriptionModal.pane == subscriptionProfilesPane {
			m.moveSubscriptionProfile(-1)
		} else {
			m.moveSubscriptionDetail(-1)
		}
		return m, nil
	}

	target := m.subscriptionModalTarget(cardX, cardY)
	switch msg.Action {
	case tea.MouseActionMotion:
		m.subscriptionModal.hover = target
	case tea.MouseActionPress:
		if msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		m.subscriptionModal.hover = target
		switch target.kind {
		case subscriptionHitProfile:
			alreadyFocused := m.subscriptionModal.profileCursor == target.index
			m.selectSubscriptionProfile(target.index)
			if alreadyFocused && m.subscriptionModalCompact() {
				m.subscriptionModal.pane = subscriptionDetailsPane
			}
		case subscriptionHitAdd:
			m.startSubscriptionAdd()
		case subscriptionHitProvider:
			m.subscriptionModal.providerCursor = target.index
			return m, m.beginSubscriptionAddName()
		case subscriptionHitMapping:
			m.subscriptionModal.pane = subscriptionDetailsPane
			m.subscriptionModal.detailCursor = target.index
			m.cycleSubscriptionMapping("next")
		case subscriptionHitAuth:
			m.subscriptionModal.detailCursor = subscriptionDetailAuth
			return m, m.beginSubscriptionKeyEdit()
		case subscriptionHitUse:
			m.useSubscriptionProfile()
		case subscriptionHitSave:
			m.saveSubscriptionDraft()
		case subscriptionHitRename:
			m.startSubscriptionRename()
		case subscriptionHitDelete:
			m.startSubscriptionDelete()
		case subscriptionHitBack:
			m.subscriptionModal.pane = subscriptionProfilesPane
		}
	}
	return m, nil
}

func (m *MainMenuModel) renderSubscriptionModalCard() string {
	_, _, width, height := m.subscriptionModalLayout()
	innerWidth := width - 2
	if innerWidth < 1 {
		innerWidth = 1
	}
	bodyHeight := height - 4 // top + separator/footer + bottom
	if bodyHeight < 1 {
		bodyHeight = 1
	}

	border := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	title := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true)
	topInner := border.Render(strings.Repeat("─", innerWidth))
	if innerWidth >= 3 {
		titleText := modalTruncate("Subscriptions", innerWidth-3)
		fill := innerWidth - lipgloss.Width("─ ") - lipgloss.Width(titleText) - lipgloss.Width(" ")
		if fill < 0 {
			fill = 0
		}
		topInner = border.Render("─ ") +
			title.Render(titleText) +
			border.Render(" "+strings.Repeat("─", fill))
	}
	lines := []string{
		border.Render("╭") + topInner + border.Render("╮"),
	}

	compact := m.subscriptionModalCompact()
	if compact {
		var body []string
		if m.subscriptionModal.pane == subscriptionDetailsPane ||
			m.subscriptionModal.mode != subscriptionBrowse {
			body = m.subscriptionDetailLines(innerWidth, bodyHeight)
		} else {
			body = m.subscriptionProfileLines(innerWidth, bodyHeight)
		}
		for _, line := range body {
			lines = append(lines, border.Render("│")+line+border.Render("│"))
		}
	} else {
		leftWidth := subscriptionListWidth
		rightWidth := innerWidth - leftWidth - 1
		if rightWidth < 1 {
			rightWidth = 1
		}
		left := m.subscriptionProfileLines(leftWidth, bodyHeight)
		right := m.subscriptionDetailLines(rightWidth, bodyHeight)
		for i := 0; i < bodyHeight; i++ {
			lines = append(lines, border.Render("│")+left[i]+border.Render("│")+right[i]+border.Render("│"))
		}
	}

	help := "↑↓ profile · → details · Tab pane · Enter action · Esc close"
	if m.subscriptionModal.mode != subscriptionBrowse {
		help = "Enter confirm · Esc cancel"
	} else if compact && m.subscriptionModal.pane == subscriptionDetailsPane {
		help = "↑↓ setting · Enter action · ←/Esc profiles"
	} else if compact {
		help = "↑↓ navigate · → details · Esc close"
	} else if m.subscriptionModal.pane == subscriptionDetailsPane {
		help = "↑↓ setting · Tab pane · ← back/previous · → value/next · Enter action · Esc close"
	}
	help = modalTruncate(help, innerWidth)
	lines = append(lines,
		border.Render("├"+strings.Repeat("─", innerWidth)+"┤"),
		border.Render("│")+lipgloss.NewStyle().Foreground(lipgloss.Color("247")).Render(modalPad(help, innerWidth))+border.Render("│"),
		border.Render("╰"+strings.Repeat("─", innerWidth)+"╯"),
	)
	return strings.Join(lines, "\n")
}

func (m *MainMenuModel) overlaySubscriptionModal(placed string) string {
	left, top, width, _ := m.subscriptionModalLayout()
	return m.overlayCard(
		placed,
		strings.Split(m.renderSubscriptionModalCard(), "\n"),
		left,
		top,
		width,
	)
}
