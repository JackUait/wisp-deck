package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/claudeconfig"
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
	open          bool
	pane          subscriptionPane
	mode          subscriptionModalMode
	profileCursor int
	detailCursor  int
	profileOffset int
	detailOffset  int
	draft         subscriptionDraft
	providerKey   string
	err           error
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
}

func (m *MainMenuModel) updateSubscriptionModal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.subscriptionModal.open = false
	case tea.KeyTab:
		if m.subscriptionModal.pane == subscriptionProfilesPane {
			m.subscriptionModal.pane = subscriptionDetailsPane
		} else {
			m.subscriptionModal.pane = subscriptionProfilesPane
		}
	case tea.KeyUp:
		m.moveSubscriptionProfile(-1)
	case tea.KeyDown:
		m.moveSubscriptionProfile(1)
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			switch TranslateRune(msg.Runes[0]) {
			case 'k':
				m.moveSubscriptionProfile(-1)
			case 'j':
				m.moveSubscriptionProfile(1)
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
	m.subscriptionModal.profileCursor = next
}
