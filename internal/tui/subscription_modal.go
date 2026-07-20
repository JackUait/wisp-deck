package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/jackuait/wisp-deck/internal/claudeconfig"
	"github.com/jackuait/wisp-deck/internal/gptbridge"
	"github.com/mattn/go-runewidth"
)

const (
	subscriptionModalMaxWidth = 92
	subscriptionModalMinWide  = 64
	subscriptionListWidth     = 28
	subscriptionModalHeight   = 22
)

const subscriptionDetailNone = -1

const (
	subscriptionAuthCheckTimeout = 15 * time.Second
	subscriptionAuthLoginTimeout = 10 * time.Minute
)

const (
	subscriptionDetailOpus = iota
	subscriptionDetailSonnet
	subscriptionDetailHaiku
	subscriptionDetailFable
	subscriptionDetailAuth
	subscriptionDetailRename
	subscriptionDetailDelete
	subscriptionDetailSave
	subscriptionDetailUse
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
	subscriptionLoginName
)

const (
	subscriptionLifecycleConfirm = iota
	subscriptionLifecycleCancel
)

type subscriptionHitKind int

const (
	subscriptionHitNone subscriptionHitKind = iota
	subscriptionHitProfile
	subscriptionHitAdd
	subscriptionHitMapping
	subscriptionHitAuth
	subscriptionHitSave
	subscriptionHitRename
	subscriptionHitDelete
	subscriptionHitBack
	subscriptionHitProvider
	subscriptionHitConfirm
	subscriptionHitCancel
	subscriptionHitLogin
	subscriptionHitAddLogin
	subscriptionHitUse
)

type subscriptionHitTarget struct {
	kind  subscriptionHitKind
	index int
}

type subscriptionAuthStatus int

const (
	subscriptionAuthChecking subscriptionAuthStatus = iota
	subscriptionAuthSignedOut
	subscriptionAuthSignedIn
	subscriptionAuthUnavailable
)

type subscriptionAuthState struct {
	status     subscriptionAuthStatus
	pending    bool
	url        string
	openErr    error
	err        error
	generation uint64
	cancel     context.CancelFunc
}

type chatGPTAuthCheckFunc func(
	context.Context,
	string,
) (gptbridge.AccountReadResult, error)

type chatGPTAuthLoginFunc func(
	context.Context,
	string,
	func(gptbridge.ChatGPTAuthEvent),
) (gptbridge.AccountReadResult, error)

type subscriptionAuthCheckedMsg struct {
	generation uint64
	account    gptbridge.AccountReadResult
	err        error
}

type subscriptionAuthLoginEvent struct {
	presented *gptbridge.ChatGPTAuthEvent
	account   gptbridge.AccountReadResult
	err       error
	done      bool
}

type subscriptionAuthLoginMsg struct {
	generation uint64
	event      subscriptionAuthLoginEvent
	events     <-chan subscriptionAuthLoginEvent
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
	open            bool
	pane            subscriptionPane
	mode            subscriptionModalMode
	profileCursor   int
	detailCursor    int
	profileOffset   int
	detailOffset    int
	draft           subscriptionDraft
	input           textinput.Model
	providerKey     string
	providerCursor  int
	lifecycleCursor int
	pendingProfile  int
	pendingMode     subscriptionModalMode
	pendingClose    bool
	hover           subscriptionHitTarget
	err             error
	auth            subscriptionAuthState
}

type subscriptionProfile struct {
	Name     string
	File     string
	Provider claudeconfig.Provider
	Standard bool
	Active   bool
	Ready    bool
	Disabled bool
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
	disabled := claudeconfig.LoadDisabled(claudeconfig.DisabledFile(m.claudeConfigsList))
	for _, config := range m.claudeConfigs {
		profiles = append(profiles, subscriptionProfile{
			Name:     config.Name,
			File:     config.File,
			Provider: m.claudeConfigProvider(config),
			Active:   config.File == active,
			Ready:    m.configReady(config),
			Disabled: disabled[config.File],
		})
	}
	return profiles
}

// subscriptionLoginRow is one Claude login in the profiles pane's LOGINS
// section: the implicit Default (Dir == "") or a managed login.
type subscriptionLoginRow struct {
	Label  string
	Dir    string
	Active bool
}

func (m *MainMenuModel) subscriptionLoginRows() []subscriptionLoginRow {
	rows := []subscriptionLoginRow{{
		Label:  m.DefaultAccountLabel(),
		Active: m.selectedAccount == 0,
	}}
	for i, acc := range m.claudeAccounts {
		rows = append(rows, subscriptionLoginRow{
			Label:  acc.Label,
			Dir:    acc.Dir,
			Active: m.selectedAccount == i+1,
		})
	}
	return rows
}

// The profiles-pane cursor space, in order: subscription profiles, the
// + Add profile row, the login rows, the + Add login row.
func (m *MainMenuModel) subscriptionLoginRowStart() int {
	return len(m.subscriptionProfiles()) + 1
}

func (m *MainMenuModel) subscriptionAddLoginRow() int {
	return m.subscriptionLoginRowStart() + len(m.subscriptionLoginRows())
}

func (m *MainMenuModel) subscriptionLastRow() int { return m.subscriptionAddLoginRow() }

func (m *MainMenuModel) subscriptionModalOnLoginRow() bool {
	cursor := m.subscriptionModal.profileCursor
	return cursor >= m.subscriptionLoginRowStart() && cursor < m.subscriptionAddLoginRow()
}

// subscriptionModalLoginIndex returns the login under the cursor: 0 = Default,
// 1..len = managed logins. Only meaningful when subscriptionModalOnLoginRow().
func (m *MainMenuModel) subscriptionModalLoginIndex() int {
	return m.subscriptionModal.profileCursor - m.subscriptionLoginRowStart()
}

func (m *MainMenuModel) subscriptionModalOnAddLoginRow() bool {
	return m.subscriptionModal.profileCursor == m.subscriptionAddLoginRow()
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
	return m.subscriptionModal.profileCursor == len(m.subscriptionProfiles())
}

func (m *MainMenuModel) openSubscriptionModal() tea.Cmd {
	if m.subscriptionModal.auth.cancel != nil {
		m.subscriptionModal.auth.cancel()
	}
	generation := m.subscriptionModal.auth.generation + 1
	active := m.CurrentClaudeConfigFile()
	m.subscriptionModal = subscriptionModalState{
		open:           true,
		pane:           subscriptionProfilesPane,
		mode:           subscriptionBrowse,
		profileCursor:  0,
		pendingProfile: -1,
		auth: subscriptionAuthState{
			status:     subscriptionAuthChecking,
			generation: generation,
		},
	}
	for i, profile := range m.subscriptionProfiles() {
		if profile.File == active {
			m.subscriptionModal.profileCursor = i
			break
		}
	}
	m.loadSubscriptionDraft(m.subscriptionModalProfile())
	m.ensureSubscriptionProfileVisible()
	if !m.hasChatGPTSubscriptionProfile() {
		m.subscriptionModal.auth.status = subscriptionAuthUnavailable
		return nil
	}
	return m.startSubscriptionChatGPTCheck()
}

func defaultChatGPTAuthCheck(
	ctx context.Context,
	codexPath string,
) (gptbridge.AccountReadResult, error) {
	return gptbridge.ReadChatGPTAccount(ctx, gptbridge.ChatGPTAuthOptions{
		CodexPath: codexPath,
	})
}

func defaultChatGPTAuthLogin(
	ctx context.Context,
	codexPath string,
	present func(gptbridge.ChatGPTAuthEvent),
) (gptbridge.AccountReadResult, error) {
	return gptbridge.AuthenticateChatGPT(ctx, gptbridge.ChatGPTAuthOptions{
		CodexPath: codexPath,
	}, present)
}

func (m *MainMenuModel) hasChatGPTSubscriptionProfile() bool {
	for _, profile := range m.subscriptionProfiles() {
		if profile.Provider.Auth == claudeconfig.AuthCodexChatGPT {
			return true
		}
	}
	return false
}

func (m *MainMenuModel) startSubscriptionChatGPTCheck() tea.Cmd {
	auth := &m.subscriptionModal.auth
	if m.codexPath == "" {
		auth.status = subscriptionAuthUnavailable
		auth.err = fmt.Errorf("Codex is required for ChatGPT sign-in")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), subscriptionAuthCheckTimeout)
	auth.cancel = cancel
	auth.status = subscriptionAuthChecking
	auth.err = nil
	generation := auth.generation
	check := m.chatGPTAuthCheck
	codexPath := m.codexPath
	return func() tea.Msg {
		account, err := check(ctx, codexPath)
		cancel()
		return subscriptionAuthCheckedMsg{
			generation: generation,
			account:    account,
			err:        err,
		}
	}
}

func (m *MainMenuModel) startSubscriptionChatGPTLogin() tea.Cmd {
	auth := &m.subscriptionModal.auth
	if auth.pending {
		return nil
	}
	if m.codexPath == "" {
		auth.status = subscriptionAuthUnavailable
		auth.err = fmt.Errorf("Codex is required for ChatGPT sign-in")
		return nil
	}
	if auth.cancel != nil {
		auth.cancel()
	}
	auth.generation++
	generation := auth.generation
	ctx, cancel := context.WithTimeout(context.Background(), subscriptionAuthLoginTimeout)
	auth.cancel = cancel
	auth.pending = true
	auth.url = ""
	auth.openErr = nil
	auth.err = nil
	login := m.chatGPTAuthLogin
	codexPath := m.codexPath
	events := make(chan subscriptionAuthLoginEvent, 4)
	return func() tea.Msg {
		go func() {
			account, err := login(ctx, codexPath, func(event gptbridge.ChatGPTAuthEvent) {
				copy := event
				events <- subscriptionAuthLoginEvent{presented: &copy}
			})
			events <- subscriptionAuthLoginEvent{
				account: account,
				err:     err,
				done:    true,
			}
			close(events)
		}()
		event := <-events
		return subscriptionAuthLoginMsg{
			generation: generation,
			event:      event,
			events:     events,
		}
	}
}

func waitSubscriptionAuthLogin(
	generation uint64,
	events <-chan subscriptionAuthLoginEvent,
) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			event = subscriptionAuthLoginEvent{
				err:  fmt.Errorf("ChatGPT sign-in ended unexpectedly"),
				done: true,
			}
		}
		return subscriptionAuthLoginMsg{
			generation: generation,
			event:      event,
			events:     events,
		}
	}
}

func (m *MainMenuModel) applySubscriptionAuthChecked(
	msg subscriptionAuthCheckedMsg,
) tea.Cmd {
	auth := &m.subscriptionModal.auth
	if msg.generation != auth.generation {
		return nil
	}
	auth.cancel = nil
	m.setSubscriptionAuthResult(msg.account, msg.err)
	return nil
}

func (m *MainMenuModel) applySubscriptionAuthLogin(
	msg subscriptionAuthLoginMsg,
) tea.Cmd {
	auth := &m.subscriptionModal.auth
	if msg.generation != auth.generation {
		return nil
	}
	if msg.event.presented != nil {
		auth.url = msg.event.presented.URL
		auth.openErr = msg.event.presented.OpenErr
		return waitSubscriptionAuthLogin(msg.generation, msg.events)
	}
	if !msg.event.done {
		return waitSubscriptionAuthLogin(msg.generation, msg.events)
	}
	if auth.cancel != nil {
		auth.cancel()
	}
	auth.cancel = nil
	auth.pending = false
	m.setSubscriptionAuthResult(msg.event.account, msg.event.err)
	return nil
}

func (m *MainMenuModel) setSubscriptionAuthResult(
	account gptbridge.AccountReadResult,
	err error,
) {
	auth := &m.subscriptionModal.auth
	auth.err = err
	if err != nil {
		auth.status = subscriptionAuthUnavailable
		return
	}
	if account.Account == nil {
		auth.status = subscriptionAuthSignedOut
		return
	}
	if err := gptbridge.ValidateChatGPTSubscription(account); err != nil {
		auth.status = subscriptionAuthUnavailable
		auth.err = err
		return
	}
	auth.status = subscriptionAuthSignedIn
}

func (m *MainMenuModel) cancelSubscriptionAuth() {
	auth := &m.subscriptionModal.auth
	if auth.cancel != nil {
		auth.cancel()
		auth.cancel = nil
	}
	auth.pending = false
	auth.generation++
}

func (m *MainMenuModel) enterSubscriptionLifecycle(mode subscriptionModalMode) {
	m.subscriptionModal.mode = mode
	m.subscriptionModal.lifecycleCursor = subscriptionLifecycleConfirm
}

func (m *MainMenuModel) moveSubscriptionLifecycleAction(delta int) {
	next := m.subscriptionModal.lifecycleCursor + delta
	if next < subscriptionLifecycleConfirm {
		next = subscriptionLifecycleCancel
	}
	if next > subscriptionLifecycleCancel {
		next = subscriptionLifecycleConfirm
	}
	m.subscriptionModal.lifecycleCursor = next
}

func (m *MainMenuModel) updateSubscriptionModal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.subscriptionModal.mode == subscriptionEditKey {
		return m.updateSubscriptionKeyInput(msg)
	}
	if m.subscriptionModal.mode == subscriptionAddProvider {
		return m.updateSubscriptionProviderPicker(msg)
	}
	if m.subscriptionModal.mode == subscriptionAddName ||
		m.subscriptionModal.mode == subscriptionRename ||
		m.subscriptionModal.mode == subscriptionLoginName {
		return m.updateSubscriptionNameInput(msg)
	}
	if m.subscriptionModal.mode == subscriptionDeleteConfirm {
		switch msg.Type {
		case tea.KeyLeft:
			m.moveSubscriptionLifecycleAction(-1)
		case tea.KeyRight:
			m.moveSubscriptionLifecycleAction(1)
		case tea.KeyEnter:
			if m.subscriptionModal.lifecycleCursor == subscriptionLifecycleCancel {
				m.subscriptionModal.mode = subscriptionBrowse
			} else if m.subscriptionModalOnLoginRow() {
				m.deleteSubscriptionLogin()
			} else {
				m.deleteSubscriptionProfile()
			}
		case tea.KeyEsc, tea.KeyCtrlC:
			m.subscriptionModal.mode = subscriptionBrowse
		}
		return m, nil
	}
	if m.subscriptionModal.mode == subscriptionDiscardConfirm {
		switch msg.Type {
		case tea.KeyLeft:
			m.moveSubscriptionLifecycleAction(-1)
		case tea.KeyRight:
			m.moveSubscriptionLifecycleAction(1)
		case tea.KeyEnter:
			if m.subscriptionModal.lifecycleCursor == subscriptionLifecycleCancel {
				m.subscriptionModal.mode = subscriptionBrowse
				m.subscriptionModal.pendingProfile = -1
				m.subscriptionModal.pendingMode = subscriptionBrowse
				m.subscriptionModal.pendingClose = false
				return m, nil
			}
			if m.subscriptionModal.pendingClose {
				m.cancelSubscriptionAuth()
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
			m.enterSubscriptionLifecycle(subscriptionDiscardConfirm)
			m.subscriptionModal.pendingClose = true
			return m, nil
		}
		m.cancelSubscriptionAuth()
		m.subscriptionModal.open = false
	case tea.KeyCtrlC:
		if m.subscriptionModal.draft.dirty {
			m.enterSubscriptionLifecycle(subscriptionDiscardConfirm)
			m.subscriptionModal.pendingClose = true
			return m, nil
		}
		m.cancelSubscriptionAuth()
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
		} else if m.subscriptionModalOnAddLoginRow() {
			return m, m.startSubscriptionLoginAdd()
		} else if m.subscriptionModal.pane == subscriptionProfilesPane &&
			m.subscriptionModalCompact() {
			m.subscriptionModal.pane = subscriptionDetailsPane
			m.ensureSubscriptionDetailVisible()
		} else if m.subscriptionModal.pane == subscriptionProfilesPane &&
			m.subscriptionModalOnLoginRow() {
			m.useSubscriptionLogin()
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
				if m.subscriptionModalOnLoginRow() {
					m.useSubscriptionLogin()
					return m, nil
				}
				return m, m.useSubscriptionProfile()
			case 's':
				m.saveSubscriptionDraft()
			case 'e':
				return m, m.beginSubscriptionKeyEdit()
			case 'a':
				if m.subscriptionModalOnLoginRow() || m.subscriptionModalOnAddLoginRow() {
					return m, m.startSubscriptionLoginAdd()
				}
				m.startSubscriptionAdd()
			case 'r':
				if m.subscriptionModalOnLoginRow() {
					m.startSubscriptionLoginRename()
				} else {
					m.startSubscriptionRename()
				}
			case 'd':
				if m.subscriptionModalOnLoginRow() {
					m.startSubscriptionLoginDelete()
				} else {
					m.startSubscriptionDelete()
				}
			case 'x':
				m.toggleSubscriptionProfileDisabled()
			}
		}
	}
	return m, nil
}

// toggleSubscriptionProfileDisabled flips the focused subscription's disabled
// state (the 'x' key, mirroring the AI tools panel). A disabled subscription
// stays manageable here but is hidden from the in-session switcher popup.
// Standard Claude, the add rows, and the login rows have no toggle.
func (m *MainMenuModel) toggleSubscriptionProfileDisabled() {
	cursor := m.subscriptionModal.profileCursor
	profiles := m.subscriptionProfiles()
	if cursor <= 0 || cursor >= len(profiles) || m.claudeConfigsList == "" {
		return
	}
	if _, err := claudeconfig.ToggleDisabled(
		claudeconfig.DisabledFile(m.claudeConfigsList), profiles[cursor].File,
	); err != nil {
		m.subscriptionModal.err = err
		return
	}
	m.subscriptionModal.err = nil
}

func (m *MainMenuModel) moveSubscriptionProfile(delta int) {
	if m.subscriptionModal.pane != subscriptionProfilesPane {
		return
	}
	last := m.subscriptionLastRow()
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
	last := m.subscriptionLastRow()
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
		m.enterSubscriptionLifecycle(subscriptionDiscardConfirm)
		m.subscriptionModal.pendingProfile = next
		m.subscriptionModal.pendingMode = subscriptionBrowse
		m.subscriptionModal.pendingClose = false
		return
	}
	m.subscriptionModal.profileCursor = next
	if next >= len(m.subscriptionProfiles()) {
		// The add rows and the login rows have no subscription draft.
		m.subscriptionModal.draft = subscriptionDraft{}
		m.subscriptionModal.err = nil
		if m.subscriptionModalOnLoginRow() {
			m.subscriptionModal.detailCursor = subscriptionDetailUse
			m.subscriptionModal.detailOffset = 0
		}
		m.ensureSubscriptionProfileVisible()
		return
	}
	m.loadSubscriptionDraft(m.subscriptionModalProfile())
	m.ensureSubscriptionProfileVisible()
}

func (m *MainMenuModel) subscriptionDetailRows() []int {
	if m.subscriptionModalOnAddRow() || m.subscriptionModalOnAddLoginRow() {
		return nil
	}
	if m.subscriptionModalOnLoginRow() {
		return m.subscriptionLoginActions()
	}
	profile := m.subscriptionModalProfile()
	if profile.Standard {
		return nil
	}
	rows := []int{
		subscriptionDetailOpus,
		subscriptionDetailSonnet,
		subscriptionDetailHaiku,
		subscriptionDetailFable,
	}
	if profile.Provider.Auth == claudeconfig.AuthAPIKey ||
		profile.Provider.Auth == claudeconfig.AuthCodexChatGPT {
		rows = append(rows, subscriptionDetailAuth)
	}
	return append(rows, subscriptionDetailRename)
}

func (m *MainMenuModel) moveSubscriptionDetail(delta int) {
	rows := m.subscriptionDetailRows()
	if len(rows) == 0 {
		return
	}
	if !m.subscriptionModalOnLoginRow() &&
		m.subscriptionModal.detailCursor >= subscriptionDetailRename &&
		m.subscriptionModal.detailCursor <= subscriptionDetailSave {
		if delta >= 0 {
			return
		}
		m.subscriptionModal.detailCursor = subscriptionDetailRename
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
	if m.subscriptionModalOnLoginRow() {
		actions := m.subscriptionLoginActions()
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
		return true
	}
	if m.subscriptionModalOnAddRow() || m.subscriptionModalOnAddLoginRow() ||
		m.subscriptionModalProfile().Standard {
		return false
	}
	actions := [...]int{
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
		m.subscriptionModal.detailCursor = subscriptionDetailNone
	} else {
		m.subscriptionModal.detailCursor = subscriptionDetailOpus
	}
	m.subscriptionModal.detailOffset = 0
	m.subscriptionModal.err = nil
}

func (m *MainMenuModel) useSubscriptionProfile() tea.Cmd {
	if m.subscriptionModal.profileCursor >= len(m.subscriptionProfiles()) {
		return nil
	}
	profile := m.subscriptionModalProfile()
	if !profile.Ready {
		m.subscriptionModal.err = fmt.Errorf("%s needs an API key before it can be used", profile.Name)
		return nil
	}
	if err := claudeconfig.SetActive(m.claudeConfigFile, profile.File); err != nil {
		m.subscriptionModal.err = err
		return nil
	}
	m.SetActiveClaudeConfig(profile.File)
	m.subscriptionModal.err = nil
	m.syncOpenCode()
	if profile.Provider.Auth == claudeconfig.AuthCodexChatGPT &&
		m.subscriptionModal.auth.status == subscriptionAuthSignedOut {
		return m.startSubscriptionChatGPTLogin()
	}
	return nil
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
	if m.subscriptionModal.profileCursor >= len(m.subscriptionProfiles()) {
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
	m.enterSubscriptionLifecycle(subscriptionEditKey)
	m.subscriptionModal.err = nil
	return textinput.Blink
}

func (m *MainMenuModel) updateSubscriptionKeyInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyLeft:
		m.moveSubscriptionLifecycleAction(-1)
		return m, nil
	case tea.KeyRight:
		m.moveSubscriptionLifecycleAction(1)
		return m, nil
	case tea.KeyEsc, tea.KeyCtrlC:
		m.subscriptionModal.mode = subscriptionBrowse
		m.subscriptionModal.input.Blur()
		return m, nil
	case tea.KeyEnter:
		if m.subscriptionModal.lifecycleCursor == subscriptionLifecycleCancel {
			m.subscriptionModal.mode = subscriptionBrowse
			m.subscriptionModal.input.Blur()
			return m, nil
		}
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
		if m.subscriptionModalProfile().Provider.Auth == claudeconfig.AuthCodexChatGPT {
			return m, m.startSubscriptionChatGPTLogin()
		}
		return m, m.beginSubscriptionKeyEdit()
	case subscriptionDetailUse:
		m.useSubscriptionLogin()
	case subscriptionDetailRename:
		if m.subscriptionModalOnLoginRow() {
			m.startSubscriptionLoginRename()
		} else {
			m.startSubscriptionRename()
		}
	case subscriptionDetailDelete:
		if m.subscriptionModalOnLoginRow() {
			m.startSubscriptionLoginDelete()
		} else {
			m.startSubscriptionDelete()
		}
	case subscriptionDetailSave:
		m.saveSubscriptionDraft()
	}
	return m, nil
}

func (m *MainMenuModel) startSubscriptionAdd() {
	if m.subscriptionModal.draft.dirty {
		m.enterSubscriptionLifecycle(subscriptionDiscardConfirm)
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
	m.enterSubscriptionLifecycle(subscriptionAddName)
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
		m.enterSubscriptionLifecycle(subscriptionDiscardConfirm)
		m.subscriptionModal.pendingProfile = -1
		m.subscriptionModal.pendingMode = subscriptionRename
		m.subscriptionModal.pendingClose = false
		return
	}
	m.subscriptionModal.input = m.newSubscriptionNameInput(profile.Name)
	m.enterSubscriptionLifecycle(subscriptionRename)
	m.subscriptionModal.pane = subscriptionDetailsPane
	m.subscriptionModal.err = nil
}

func (m *MainMenuModel) updateSubscriptionNameInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyLeft:
		m.moveSubscriptionLifecycleAction(-1)
		return m, nil
	case tea.KeyRight:
		m.moveSubscriptionLifecycleAction(1)
		return m, nil
	case tea.KeyEsc, tea.KeyCtrlC:
		m.subscriptionModal.mode = subscriptionBrowse
		m.subscriptionModal.input.Blur()
		return m, nil
	case tea.KeyEnter:
		if m.subscriptionModal.lifecycleCursor == subscriptionLifecycleCancel {
			m.subscriptionModal.mode = subscriptionBrowse
			m.subscriptionModal.input.Blur()
			return m, nil
		}
		name := strings.TrimSpace(m.subscriptionModal.input.Value())
		if name == "" {
			m.subscriptionModal.err = fmt.Errorf("profile name cannot be empty")
			return m, nil
		}
		switch {
		case m.subscriptionModal.mode == subscriptionAddName:
			m.addSubscriptionProfile(name)
		case m.subscriptionModal.mode == subscriptionLoginName:
			m.addSubscriptionLogin(name)
		case m.subscriptionModalOnLoginRow():
			m.renameSubscriptionLogin(name)
		default:
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
		m.enterSubscriptionLifecycle(subscriptionDiscardConfirm)
		m.subscriptionModal.pendingProfile = -1
		m.subscriptionModal.pendingMode = subscriptionDeleteConfirm
		m.subscriptionModal.pendingClose = false
		return
	}
	m.enterSubscriptionLifecycle(subscriptionDeleteConfirm)
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
		actionHovered := m.subscriptionModal.hover.kind == subscriptionHitConfirm ||
			m.subscriptionModal.hover.kind == subscriptionHitCancel
		selected := m.subscriptionModal.mode != subscriptionAddProvider &&
			((kind == subscriptionHitConfirm &&
				m.subscriptionModal.lifecycleCursor == subscriptionLifecycleConfirm) ||
				(kind == subscriptionHitCancel &&
					m.subscriptionModal.lifecycleCursor == subscriptionLifecycleCancel))
		if m.subscriptionModal.hover.kind == kind || (!actionHovered && selected) {
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
		lines = append(lines,
			accent.Render("CHOOSE PROVIDER"),
			dim.Render("Select how this profile authenticates."),
			"",
			subscriptionSectionLine("AVAILABLE PROVIDERS", width, dim.Bold(true), dim),
		)
		for i, provider := range claudeconfig.Providers {
			lines = append(lines, m.subscriptionProviderLine(
				provider,
				width,
				i == m.subscriptionModal.providerCursor,
				m.subscriptionModal.hover.kind == subscriptionHitProvider &&
					m.subscriptionModal.hover.index == i,
			))
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
		title, field := "RENAME PROFILE", "Profile name"
		if m.subscriptionModalOnLoginRow() {
			title, field = "RENAME LOGIN", "Login label"
		}
		lines = append(lines,
			dim.Bold(true).Render(title),
			"",
			field,
			m.subscriptionModal.input.View(),
			"",
			buttons("[ Rename ]", accent, "[ Cancel ]"),
		)
	case subscriptionLoginName:
		lines = append(lines,
			dim.Bold(true).Render("NEW CLAUDE LOGIN"),
			"",
			"Login label",
			m.subscriptionModal.input.View(),
			"",
			buttons("[ Create ]", accent, "[ Cancel ]"),
		)
	case subscriptionDeleteConfirm:
		name := m.subscriptionModalProfile().Name
		description := "This removes the profile settings file."
		if m.subscriptionModalOnLoginRow() {
			name = m.subscriptionModalLoginName()
			description = "This removes the login and its config dir."
		}
		lines = append(lines,
			danger.Render("DELETE "+strings.ToUpper(name)+"?"),
			"",
			description,
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
	const actions = "[ Rename ]  [ Delete ]  [ Save changes ]"
	return width >= lipgloss.Width(actions)
}

func subscriptionActionPairFitsOneLine(width int) bool {
	const actions = "[ Rename ]  [ Delete ]"
	return width >= lipgloss.Width(actions)
}

// subscriptionProfileLineFor maps a profiles-pane cursor position to its line
// in the pane's item list (headers, add rows and the section gap included).
func (m *MainMenuModel) subscriptionProfileLineFor(cursor int) int {
	if cursor <= len(m.subscriptionProfiles()) {
		return cursor + 1 // SUBSCRIPTIONS header at line 0
	}
	return cursor + 3 // blank + LOGINS header between the sections
}

// subscriptionProfileRowAt is the inverse mapping: the cursor position whose
// row renders on the given item line, or -1 for headers and gaps.
func (m *MainMenuModel) subscriptionProfileRowAt(line int) int {
	subs := len(m.subscriptionProfiles())
	switch {
	case line >= 1 && line <= subs+1:
		return line - 1
	case line >= subs+4 && line-3 <= m.subscriptionLastRow():
		return line - 3
	}
	return -1
}

func (m *MainMenuModel) ensureSubscriptionProfileVisible() {
	viewport := m.subscriptionModalBodyHeight() - 1 // top gutter
	if viewport < 1 {
		viewport = 1
	}
	itemCount := m.subscriptionProfileLineFor(m.subscriptionLastRow()) + 1
	cursor := m.subscriptionModal.profileCursor
	if cursor > m.subscriptionLastRow() {
		cursor = m.subscriptionLastRow()
	}
	line := m.subscriptionProfileLineFor(cursor)
	if line < m.subscriptionModal.profileOffset {
		m.subscriptionModal.profileOffset = line
	}
	if line >= m.subscriptionModal.profileOffset+viewport {
		m.subscriptionModal.profileOffset = line - viewport + 1
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
	if m.subscriptionModalOnAddRow() || m.subscriptionModalOnAddLoginRow() ||
		m.subscriptionModalOnLoginRow() {
		return 0
	}
	profile := m.subscriptionModalProfile()
	if profile.Standard {
		return 0
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
	line += 2 // blank line and heading before actions
	if profile.Provider.Auth == claudeconfig.AuthCodexChatGPT {
		if cursor == subscriptionDetailAuth {
			return line
		}
		line++ // dedicated sign-in row
		if m.subscriptionModal.auth.url != "" {
			line++
		}
		if m.subscriptionModal.auth.openErr != nil {
			line++
		}
		if m.subscriptionModal.auth.err != nil {
			line++
		}
	}
	if subscriptionActionsFitOneLine(m.subscriptionDetailPaneWidth()) {
		return line
	}
	if subscriptionActionPairFitsOneLine(m.subscriptionDetailPaneWidth()) {
		if cursor == subscriptionDetailSave {
			return line + 1
		}
		return line
	}
	if cursor == subscriptionDetailDelete {
		return line + 1
	}
	if cursor == subscriptionDetailSave {
		return line + 2
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

func subscriptionSectionLine(title string, width int, style, rule lipgloss.Style) string {
	label := style.Render(title)
	gap := width - lipgloss.Width(label) - 1
	if gap <= 0 {
		return modalPad(label, width)
	}
	return label + " " + rule.Render(strings.Repeat("─", gap))
}

func subscriptionAuthLabel(provider claudeconfig.Provider) string {
	if provider.Auth == claudeconfig.AuthCodexChatGPT {
		return "CODEX LOGIN"
	}
	return "API KEY"
}

func (m *MainMenuModel) subscriptionProviderLine(
	provider claudeconfig.Provider,
	width int,
	focused,
	hovered bool,
) string {
	accent := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	text := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	auth := subscriptionAuthLabel(provider)
	nameWidth := width - lipgloss.Width(auth) - 3
	if nameWidth < 1 {
		nameWidth = 1
	}
	name := modalTruncate(provider.Name, nameWidth)
	gap := width - lipgloss.Width("  ") - lipgloss.Width(name) - lipgloss.Width(auth)
	if gap < 1 {
		gap = 1
	}

	if focused || hovered {
		selectionColor := lipgloss.Color("236")
		wash := lipgloss.NewStyle().Background(selectionColor)
		markerStyle := dim
		nameStyle := text
		if focused {
			markerStyle = accent
			nameStyle = accent
		}
		return markerStyle.Background(selectionColor).Render("▌") +
			wash.Render(" ") +
			nameStyle.Background(selectionColor).Render(name) +
			wash.Render(strings.Repeat(" ", gap)) +
			dim.Background(selectionColor).Bold(true).Render(auth)
	}
	return "  " + text.Render(name) + strings.Repeat(" ", gap) + dim.Bold(true).Render(auth)
}

func (m *MainMenuModel) subscriptionProfileLines(width, height int) []string {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	accent := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	green := lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	amber := lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	selectionColor := lipgloss.Color("236")
	selectionWash := lipgloss.NewStyle().Background(selectionColor)
	rowWidth := width - 1
	if rowWidth < 1 {
		rowWidth = 1
	}

	var items []string
	items = append(items, subscriptionSectionLine("SUBSCRIPTIONS", rowWidth, dim.Bold(true), dim))
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
		if profile.Disabled {
			statusText = "Disabled"
			status = dim.Render(statusText)
		}
		nameWidth := rowWidth - lipgloss.Width(cursor) - lipgloss.Width(active) - lipgloss.Width(statusText) - 1
		if nameWidth < 1 {
			nameWidth = 1
		}
		name := modalTruncate(profile.Name, nameWidth)
		gap := rowWidth - lipgloss.Width(cursor) - lipgloss.Width(active) - lipgloss.Width(name) - lipgloss.Width(status)
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
			if profile.Disabled {
				statusStyle = dim
			}
			status = statusStyle.Background(selectionColor).Render(statusText)
			items = append(items, cursor+active+name+selectionWash.Render(strings.Repeat(" ", gap))+status)
			continue
		}
		items = append(items, cursor+active+name+strings.Repeat(" ", gap)+status)
	}

	// addRow renders one of the two trailing add rows, highlighted when the
	// cursor sits on rowIndex or the pointer hovers hit.
	addRow := func(label string, rowIndex int, hit subscriptionHitKind) string {
		switch {
		case m.subscriptionModal.profileCursor == rowIndex:
			padding := rowWidth - lipgloss.Width("▌ ") - lipgloss.Width(label)
			if padding < 0 {
				padding = 0
			}
			return accent.Background(selectionColor).Render("▌") +
				selectionWash.Render(" ") +
				accent.Background(selectionColor).Render(label) +
				selectionWash.Render(strings.Repeat(" ", padding))
		case m.subscriptionModal.hover.kind == hit:
			return dim.Render("▌") + " " + label
		}
		return "  " + label
	}
	items = append(items, addRow("+ Add profile", len(profiles), subscriptionHitAdd))

	// LOGINS section: the implicit Default plus every managed Claude login.
	items = append(items,
		strings.Repeat(" ", rowWidth),
		subscriptionSectionLine("LOGINS", rowWidth, dim.Bold(true), dim),
	)
	for i, login := range m.subscriptionLoginRows() {
		rowIndex := m.subscriptionLoginRowStart() + i
		focused := rowIndex == m.subscriptionModal.profileCursor
		cursor := "  "
		if focused {
			cursor = accent.Render("▌") + " "
		} else if m.subscriptionModal.hover.kind == subscriptionHitLogin &&
			m.subscriptionModal.hover.index == i {
			cursor = dim.Render("▌") + " "
		}
		active := "  "
		if login.Active {
			active = green.Render("●") + " "
		}
		dirText := login.Dir
		if dirText == "" {
			dirText = "default"
		}
		nameWidth := rowWidth - lipgloss.Width(cursor) - lipgloss.Width(active) - lipgloss.Width(dirText) - 1
		if nameWidth < 1 {
			nameWidth = 1
		}
		name := modalTruncate(login.Label, nameWidth)
		gap := rowWidth - lipgloss.Width(cursor) - lipgloss.Width(active) - lipgloss.Width(name) - lipgloss.Width(dirText)
		if gap < 1 {
			gap = 1
		}
		// Each login's label wears its persistent account color (shared with the
		// statusline), falling back to the pane's default text.
		nameStyle := lipgloss.NewStyle()
		if c, ok := m.accountColor(login.Dir); ok {
			nameStyle = nameStyle.Foreground(c)
		}
		if focused {
			cursor = accent.Background(selectionColor).Render("▌") + selectionWash.Render(" ")
			active = selectionWash.Render("  ")
			if login.Active {
				active = green.Background(selectionColor).Render("●") + selectionWash.Render(" ")
			}
			items = append(items, cursor+active+
				accent.Background(selectionColor).Render(name)+
				selectionWash.Render(strings.Repeat(" ", gap))+
				dim.Background(selectionColor).Render(dirText))
			continue
		}
		items = append(items, cursor+active+nameStyle.Render(name)+strings.Repeat(" ", gap)+dim.Render(dirText))
	}
	items = append(items, addRow("+ Add login", m.subscriptionAddLoginRow(), subscriptionHitAddLogin))

	if height <= 0 {
		return nil
	}
	lines := []string{strings.Repeat(" ", width)}
	return append(
		lines,
		modalWindow(items, m.subscriptionModal.profileOffset, height-1, width)...,
	)
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
		subscriptionRename, subscriptionDeleteConfirm, subscriptionDiscardConfirm,
		subscriptionLoginName:
		return m.subscriptionLifecycleLines(width, height)
	}

	if m.subscriptionModalOnAddLoginRow() {
		return m.subscriptionAddLoginDetailLines(width, height)
	}
	if m.subscriptionModalOnLoginRow() {
		return m.subscriptionLoginDetailLines(width, height)
	}

	if m.subscriptionModalOnAddRow() {
		lines := []string{
			accent.Render("ADD PROFILE"),
			dim.Render("Connect another provider to Claude-compatible routes."),
			"",
			subscriptionSectionLine("AVAILABLE PROVIDERS", width, dim.Bold(true), dim),
		}
		for _, provider := range claudeconfig.Providers {
			lines = append(lines, m.subscriptionProviderLine(provider, width, false, false))
		}
		lines = append(lines,
			"",
			accent.Render("[ Choose provider ]"),
			dim.Render("Name it, configure routes, then make it active."),
		)
		return modalWindow(lines, 0, height, width)
	}

	profile := m.subscriptionModalProfile()
	providerLabel := dim.Bold(true).Render("PROVIDER  ")
	providerWidth := width - lipgloss.Width(providerLabel)
	if providerWidth < 1 {
		providerWidth = 1
	}
	lines := []string{
		providerLabel + green.Render(modalTruncate(profile.Provider.Name, providerWidth)),
	}
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

	if profile.Standard {
		lines = append(lines,
			"",
			subscriptionSectionLine("CONNECTION", width, dim.Bold(true), dim),
			valueRow(-1, "Authentication", "Claude Code login", green),
			dim.Render("Uses Claude Code's native models and account."),
		)
		return modalWindow(lines, m.subscriptionModal.detailOffset, height, width)
	}

	auth := "API key"
	authStyle := amber
	endpoint := profile.Provider.BaseURL
	if configured := claudeconfig.ReadBaseURL(m.claudeConfigsDir, profile.File); configured != "" {
		endpoint = configured
	}
	if profile.Provider.Auth == claudeconfig.AuthCodexChatGPT {
		auth = m.subscriptionChatGPTAuthStatus()
		authStyle = m.subscriptionChatGPTAuthStatusStyle(green, amber, dim)
		endpoint = "Local Codex bridge"
	} else if profile.Ready {
		authStyle = green
	}
	lines = append(lines,
		"",
		subscriptionSectionLine("CONNECTION", width, dim.Bold(true), dim),
		valueRow(-1, "Authentication", auth, authStyle),
		valueRow(-1, "Endpoint", endpoint, dim),
	)
	lines = append(lines,
		"",
		subscriptionSectionLine("MODEL ROUTING", width, dim.Bold(true), dim),
		"",
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
	lines = append(lines,
		"",
		subscriptionSectionLine("ACTIONS", width, dim.Bold(true), dim),
	)
	if profile.Provider.Auth == claudeconfig.AuthCodexChatGPT {
		loginLabel := "[ Sign in / switch account ]"
		if m.subscriptionModal.auth.pending {
			loginLabel = "[ Waiting for browser… ]"
		}
		lines = append(lines, m.subscriptionActionLabel(
			subscriptionHitAuth,
			subscriptionDetailAuth,
			loginLabel,
			accent,
			label,
		))
		if m.subscriptionModal.auth.url != "" {
			const prefix = "Open manually: "
			lines = append(lines, dim.Render(prefix)+accent.Render(modalTruncate(
				m.subscriptionModal.auth.url,
				width-lipgloss.Width(prefix),
			)))
		}
		if m.subscriptionModal.auth.openErr != nil {
			lines = append(lines, amber.Render(modalTruncate(
				m.subscriptionModal.auth.openErr.Error(),
				width,
			)))
		}
		if m.subscriptionModal.auth.err != nil {
			lines = append(lines, lipgloss.NewStyle().
				Foreground(lipgloss.Color("196")).
				Render(modalTruncate(m.subscriptionModal.auth.err.Error(), width)))
		}
	}
	rename := m.subscriptionActionLabel(subscriptionHitRename, subscriptionDetailRename, "[ Rename ]", accent, label)
	deleteAction := m.subscriptionActionLabel(subscriptionHitDelete, subscriptionDetailDelete, "[ Delete ]", accent, label)
	save := m.subscriptionActionLabel(subscriptionHitSave, subscriptionDetailSave, "[ Save changes ]", accent, label)
	lines = append(lines, m.subscriptionActionLines(width, rename, deleteAction, save)...)
	return modalWindow(lines, m.subscriptionModal.detailOffset, height, width)
}

func (m *MainMenuModel) subscriptionChatGPTAuthStatus() string {
	switch m.subscriptionModal.auth.status {
	case subscriptionAuthSignedOut:
		return "Signed out"
	case subscriptionAuthSignedIn:
		return "Signed in"
	case subscriptionAuthUnavailable:
		return "Unavailable"
	default:
		return "Checking…"
	}
}

func (m *MainMenuModel) subscriptionChatGPTAuthStatusStyle(
	green,
	amber,
	dim lipgloss.Style,
) lipgloss.Style {
	switch m.subscriptionModal.auth.status {
	case subscriptionAuthSignedIn:
		return green
	case subscriptionAuthSignedOut, subscriptionAuthUnavailable:
		return amber
	default:
		return dim
	}
}

func (m *MainMenuModel) subscriptionActionLines(width int, rename, deleteAction, save string) []string {
	if subscriptionActionsFitOneLine(width) {
		return []string{rename + "  " + deleteAction + "  " + save}
	}
	if subscriptionActionPairFitsOneLine(width) {
		return []string{rename + "  " + deleteAction, save}
	}
	return []string{rename, deleteAction, save}
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
	case subscriptionLoginName:
		return "[ Create ]", "[ Cancel ]"
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
	if m.subscriptionModalOnAddRow() && hitText("[ Choose provider ]") {
		return subscriptionHitTarget{kind: subscriptionHitAdd}
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
		if hitText("+ Add profile") {
			return subscriptionHitTarget{kind: subscriptionHitAdd}
		}
		if hitText("+ Add login") {
			return subscriptionHitTarget{kind: subscriptionHitAddLogin}
		}
		row := m.subscriptionProfileRowAt(cardY - 2 + m.subscriptionModal.profileOffset)
		profiles := m.subscriptionProfiles()
		if row >= 0 && row < len(profiles) && hitText(profiles[row].Name) {
			return subscriptionHitTarget{kind: subscriptionHitProfile, index: row}
		}
		if row >= m.subscriptionLoginRowStart() {
			logins := m.subscriptionLoginRows()
			idx := row - m.subscriptionLoginRowStart()
			if idx >= 0 && idx < len(logins) && hitText(logins[idx].Label) {
				return subscriptionHitTarget{kind: subscriptionHitLogin, index: idx}
			}
		}
		return subscriptionHitTarget{}
	}

	for i, alias := range claudeconfig.AnthropicAliases {
		display := strings.ToUpper(alias[:1]) + alias[1:]
		if hitText(display) {
			return subscriptionHitTarget{kind: subscriptionHitMapping, index: i}
		}
	}
	if hitText("API key") ||
		hitText("[ Sign in / switch account ]") ||
		hitText("[ Waiting for browser… ]") {
		return subscriptionHitTarget{kind: subscriptionHitAuth}
	}
	for _, button := range []struct {
		text string
		kind subscriptionHitKind
	}{
		{"[ Save changes ]", subscriptionHitSave},
		{"[ Use ]", subscriptionHitUse},
		{"[ Rename ]", subscriptionHitRename},
		{"[ Delete ]", subscriptionHitDelete},
		{"[ Create login ]", subscriptionHitAddLogin},
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
				m.enterSubscriptionLifecycle(subscriptionDiscardConfirm)
				m.subscriptionModal.pendingClose = true
			} else {
				m.cancelSubscriptionAuth()
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
				m.subscriptionModal.lifecycleCursor = subscriptionLifecycleConfirm
				return m.updateSubscriptionModal(tea.KeyMsg{Type: tea.KeyEnter})
			case subscriptionHitCancel:
				m.subscriptionModal.lifecycleCursor = subscriptionLifecycleCancel
				return m.updateSubscriptionModal(tea.KeyMsg{Type: tea.KeyEnter})
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
		case subscriptionHitLogin:
			m.selectSubscriptionProfile(m.subscriptionLoginRowStart() + target.index)
			if m.subscriptionModalCompact() &&
				m.subscriptionModal.profileCursor == m.subscriptionLoginRowStart()+target.index {
				m.subscriptionModal.pane = subscriptionDetailsPane
			}
		case subscriptionHitAddLogin:
			return m, m.startSubscriptionLoginAdd()
		case subscriptionHitUse:
			m.subscriptionModal.detailCursor = subscriptionDetailUse
			m.useSubscriptionLogin()
		case subscriptionHitProvider:
			m.subscriptionModal.providerCursor = target.index
			return m, m.beginSubscriptionAddName()
		case subscriptionHitMapping:
			m.subscriptionModal.pane = subscriptionDetailsPane
			m.subscriptionModal.detailCursor = target.index
			m.cycleSubscriptionMapping("next")
		case subscriptionHitAuth:
			m.subscriptionModal.detailCursor = subscriptionDetailAuth
			if m.subscriptionModalProfile().Provider.Auth == claudeconfig.AuthCodexChatGPT {
				return m, m.startSubscriptionChatGPTLogin()
			}
			return m, m.beginSubscriptionKeyEdit()
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

	// x mirrors toggleSubscriptionProfileDisabled's bounds: cursor 0 is Standard
	// Claude and cursors past the profiles are add/login rows, where x is a noop.
	xLabel := "x disable"
	if profiles, cursor := m.subscriptionProfiles(), m.subscriptionModal.profileCursor; cursor > 0 &&
		cursor < len(profiles) && profiles[cursor].Disabled {
		xLabel = "x enable"
	}
	help := "↑↓ profile · → details · " + xLabel + " · Tab pane · Enter action · Esc close"
	if m.subscriptionModal.mode == subscriptionAddProvider {
		help = "↑↓ provider · Enter choose · Esc cancel"
	} else if m.subscriptionModal.mode != subscriptionBrowse {
		help = "←→ action · Enter choose · Esc cancel"
	} else if compact && m.subscriptionModal.pane == subscriptionDetailsPane {
		help = "↑↓ setting · Enter action · ←/Esc profiles"
	} else if compact {
		help = "↑↓ navigate · → details · Esc close"
	} else if m.subscriptionModal.pane == subscriptionDetailsPane {
		help = "↑↓ setting · Tab pane · ← back/previous · → value/next · Enter action · Esc close"
	}
	lines = append(lines,
		border.Render("├"+strings.Repeat("─", innerWidth)+"┤"),
		border.Render("│")+subscriptionHelpLine(help, innerWidth)+border.Render("│"),
		border.Render("╰"+strings.Repeat("─", innerWidth)+"╯"),
	)
	return strings.Join(lines, "\n")
}

func subscriptionHelpLine(help string, width int) string {
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("247"))
	separatorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	segments := strings.Split(help, " · ")
	rendered := make([]string, 0, len(segments))
	for _, segment := range segments {
		key, label, ok := strings.Cut(segment, " ")
		if !ok {
			rendered = append(rendered, keyStyle.Render(segment))
			continue
		}
		rendered = append(rendered, keyStyle.Render(key)+" "+labelStyle.Render(label))
	}
	return modalPad(strings.Join(rendered, separatorStyle.Render(" · ")), width)
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
