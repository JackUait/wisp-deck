package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	pendingProfile int
	pendingClose   bool
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

func (m *MainMenuModel) openSubscriptionModal() {
	active := m.CurrentClaudeConfigFile()
	m.subscriptionModal = subscriptionModalState{
		open:          true,
		pane:          subscriptionProfilesPane,
		mode:          subscriptionBrowse,
		profileCursor: 0,
	}
	for i, profile := range m.subscriptionProfiles() {
		if profile.File == active {
			m.subscriptionModal.profileCursor = i
			break
		}
	}
	m.loadSubscriptionDraft(m.subscriptionModalProfile())
}

func (m *MainMenuModel) updateSubscriptionModal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.subscriptionModal.mode == subscriptionEditKey {
		return m.updateSubscriptionKeyInput(msg)
	}
	if m.subscriptionModal.mode == subscriptionDiscardConfirm {
		switch msg.Type {
		case tea.KeyEnter:
			if m.subscriptionModal.pendingClose {
				m.subscriptionModal.open = false
				return m, nil
			}
			m.subscriptionModal.profileCursor = m.subscriptionModal.pendingProfile
			m.subscriptionModal.mode = subscriptionBrowse
			m.subscriptionModal.pendingProfile = -1
			m.loadSubscriptionDraft(m.subscriptionModalProfile())
		case tea.KeyEsc, tea.KeyCtrlC:
			m.subscriptionModal.mode = subscriptionBrowse
			m.subscriptionModal.pendingProfile = -1
			m.subscriptionModal.pendingClose = false
		}
		return m, nil
	}

	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		if m.subscriptionModal.draft.dirty {
			m.subscriptionModal.mode = subscriptionDiscardConfirm
			m.subscriptionModal.pendingClose = true
			return m, nil
		}
		m.subscriptionModal.open = false
	case tea.KeyTab:
		if m.subscriptionModal.pane == subscriptionProfilesPane {
			m.subscriptionModal.pane = subscriptionDetailsPane
		} else {
			m.subscriptionModal.pane = subscriptionProfilesPane
		}
	case tea.KeyUp:
		if m.subscriptionModal.pane == subscriptionDetailsPane {
			if m.subscriptionModal.detailCursor > subscriptionDetailOpus {
				m.subscriptionModal.detailCursor--
			}
		} else {
			m.moveSubscriptionProfile(-1)
		}
	case tea.KeyDown:
		if m.subscriptionModal.pane == subscriptionDetailsPane {
			if m.subscriptionModal.detailCursor < m.subscriptionDetailLastRow() {
				m.subscriptionModal.detailCursor++
			}
		} else {
			m.moveSubscriptionProfile(1)
		}
	case tea.KeyRight:
		if m.subscriptionModal.pane == subscriptionDetailsPane {
			m.cycleSubscriptionMapping("next")
		} else if m.subscriptionModalCompact() {
			m.subscriptionModal.pane = subscriptionDetailsPane
		}
	case tea.KeyLeft:
		if m.subscriptionModal.pane == subscriptionDetailsPane &&
			m.subscriptionModal.detailCursor <= subscriptionDetailFable {
			m.cycleSubscriptionMapping("prev")
		} else if m.subscriptionModalCompact() {
			m.subscriptionModal.pane = subscriptionProfilesPane
		}
	case tea.KeyEnter:
		if m.subscriptionModal.pane == subscriptionDetailsPane {
			return m.activateSubscriptionDetail()
		}
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			switch TranslateRune(msg.Runes[0]) {
			case 'k':
				if m.subscriptionModal.pane == subscriptionDetailsPane {
					if m.subscriptionModal.detailCursor > subscriptionDetailOpus {
						m.subscriptionModal.detailCursor--
					}
				} else {
					m.moveSubscriptionProfile(-1)
				}
			case 'j':
				if m.subscriptionModal.pane == subscriptionDetailsPane {
					if m.subscriptionModal.detailCursor < m.subscriptionDetailLastRow() {
						m.subscriptionModal.detailCursor++
					}
				} else {
					m.moveSubscriptionProfile(1)
				}
			case 'u':
				m.useSubscriptionProfile()
			case 's':
				m.saveSubscriptionDraft()
			case 'e':
				return m, m.beginSubscriptionKeyEdit()
			}
		}
	}
	return m, nil
}

func (m *MainMenuModel) moveSubscriptionProfile(delta int) {
	if m.subscriptionModal.pane != subscriptionProfilesPane {
		return
	}
	last := len(m.subscriptionProfiles()) - 1
	next := m.subscriptionModal.profileCursor + delta
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
		m.subscriptionModal.pendingClose = false
		return
	}
	m.subscriptionModal.profileCursor = next
	m.loadSubscriptionDraft(m.subscriptionModalProfile())
}

func (m *MainMenuModel) subscriptionDetailLastRow() int {
	if m.subscriptionModalProfile().Standard {
		return subscriptionDetailUse
	}
	return subscriptionDetailSave
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
	m.subscriptionModal.detailCursor = subscriptionDetailOpus
	m.subscriptionModal.err = nil
}

func (m *MainMenuModel) useSubscriptionProfile() {
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
	case subscriptionDetailSave:
		m.saveSubscriptionDraft()
	}
	return m, nil
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

func modalTruncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return runewidth.Truncate(s, width, "…")
}

func modalPad(s string, width int) string {
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

	lines := []string{dim.Bold(true).Render("PROFILES")}
	profiles := m.subscriptionProfiles()
	for i, profile := range profiles {
		cursor := "  "
		if i == m.subscriptionModal.profileCursor {
			cursor = accent.Render("▌") + " "
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
		lines = append(lines, cursor+active+name+strings.Repeat(" ", gap)+status)
	}

	add := "  + Add profile"
	if m.subscriptionModal.profileCursor == len(profiles) {
		add = accent.Render("▌") + " + Add profile"
	}
	lines = append(lines, add)
	return modalWindow(lines, m.subscriptionModal.profileOffset, height, width)
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

	profile := m.subscriptionModalProfile()
	lines := []string{dim.Bold(true).Render("PROFILE DETAILS")}
	valueRow := func(name, value string, style lipgloss.Style) string {
		const labelWidth = 15
		valueWidth := width - labelWidth
		if valueWidth < 1 {
			valueWidth = 1
		}
		return label.Render(fmt.Sprintf("%-14s ", modalTruncate(name, 14))) +
			style.Render(modalTruncate(value, valueWidth))
	}

	lines = append(lines, valueRow("Profile", profile.Name, accent))
	if profile.Standard {
		lines = append(lines,
			valueRow("Provider", "Anthropic / Claude", green),
			valueRow("Authentication", "Claude Code login", green),
			"",
			dim.Render("Uses Claude Code's native models and account."),
			"",
			accent.Render("[ Use profile ]"),
		)
		return modalWindow(lines, m.subscriptionModal.detailOffset, height, width)
	}

	auth := "API key"
	authStyle := amber
	authState := "Needs key"
	endpoint := profile.Provider.BaseURL
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
		valueRow("Provider", profile.Provider.Name, green),
		valueRow("Authentication", auth, authStyle),
		valueRow("Status", authState, authStyle),
		valueRow("Endpoint", endpoint, dim),
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
		lines = append(lines, valueRow(strings.ToUpper(alias[:1])+alias[1:], "→ "+value, style))
	}
	if profile.Provider.Auth == claudeconfig.AuthAPIKey {
		keyStatus := "(not set)"
		keyStyle := amber
		if m.subscriptionModal.draft.apiKey != "" {
			keyStatus = "••••••••"
			keyStyle = green
		}
		lines = append(lines, valueRow("API key", keyStatus, keyStyle))
	}
	if m.subscriptionModal.draft.dirty {
		lines = append(lines, amber.Render("• Unsaved changes"))
	}
	if m.subscriptionModal.err != nil {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("196")).
			Render(modalTruncate(m.subscriptionModal.err.Error(), width)))
	}
	lines = append(lines,
		"",
		accent.Render("[ Use profile ]")+"  "+label.Render("[ Rename ]")+"  "+label.Render("[ Delete ]"),
		label.Render("[ Save changes ]"),
	)
	return modalWindow(lines, m.subscriptionModal.detailOffset, height, width)
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
	topPrefix := "╭─ "
	topSuffix := " "
	fill := innerWidth - lipgloss.Width("─ ") - lipgloss.Width("Subscriptions") - lipgloss.Width(topSuffix)
	if fill < 0 {
		fill = 0
	}
	lines := []string{
		border.Render(topPrefix) + title.Render("Subscriptions") + border.Render(topSuffix+strings.Repeat("─", fill)+"╮"),
	}

	compact := m.subscriptionModalCompact()
	if compact {
		var body []string
		if m.subscriptionModal.pane == subscriptionDetailsPane {
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

	help := "↑↓ profile · Tab pane · ←→ value · Enter action · Esc close"
	if compact {
		help = "↑↓ navigate · → details · Esc close"
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
