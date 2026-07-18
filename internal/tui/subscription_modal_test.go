package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/claudeconfig"
	"github.com/jackuait/wisp-deck/internal/models"
)

func newSubscriptionModalMenu(t *testing.T) *MainMenuModel {
	t.Helper()
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-configs.list")
	configsDir := filepath.Join(dir, "claude-configs")
	pointer := filepath.Join(dir, "claude-config")

	for _, profile := range []struct {
		name, provider string
		key            bool
	}{
		{name: "Zhipu GLM", provider: "zhipu"},
		{name: "Xiaomi MiMo", provider: "mimo", key: true},
		{name: "OpenAI GPT", provider: "openai-chatgpt"},
	} {
		file, err := claudeconfig.AddForProvider(list, configsDir, profile.name, profile.provider)
		if err != nil {
			t.Fatal(err)
		}
		if profile.key {
			if err := claudeconfig.WriteAPIKey(configsDir, file, "sk-test"); err != nil {
				t.Fatal(err)
			}
		}
	}

	m := NewMainMenu(
		[]models.Project{{Name: "p", Path: "/p"}},
		[]string{"claude", "opencode"},
		"claude",
		"none",
	)
	m.SetSize(100, 36)
	m.SetClaudeConfigFile(pointer)
	m.SetClaudeConfigPaths(list, configsDir)
	m.SetClaudeConfigs(LoadClaudeConfigsList(list))
	m.SetActiveClaudeConfig("")
	m.SetActiveTab(TabSettings)
	m.settingsSelected = rowSubscription
	return m
}

func subscriptionModalKey(t *testing.T, m *MainMenuModel, msg tea.KeyMsg) *MainMenuModel {
	t.Helper()
	updated, _ := m.Update(msg)
	got, ok := updated.(*MainMenuModel)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	return got
}

func TestSubscriptionModal_openFocusesActiveProfile(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.SetActiveClaudeConfig("openai-gpt.json")
	m.openSubscriptionModal()

	if !m.subscriptionModal.open {
		t.Fatal("modal did not open")
	}
	if got := m.subscriptionModalProfile().File; got != "openai-gpt.json" {
		t.Fatalf("focused file = %q, want openai-gpt.json", got)
	}
}

func TestSubscriptionModal_profilesIncludeStandardAndEveryConfig(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	profiles := m.subscriptionProfiles()
	if len(profiles) != 4 {
		t.Fatalf("profiles = %d, want Standard plus 3 configs", len(profiles))
	}
	if !profiles[0].Standard || profiles[0].Name != "Standard Claude" {
		t.Fatalf("first profile = %+v, want Standard Claude", profiles[0])
	}
	for i, want := range []string{"Zhipu GLM", "Xiaomi MiMo", "OpenAI GPT"} {
		if profiles[i+1].Name != want {
			t.Errorf("profile %d = %q, want %q", i+1, profiles[i+1].Name, want)
		}
	}
}

func TestSettingsEnter_onSubscriptionOpensModal(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	_, _ = m.settingsEnter()
	if !m.subscriptionModal.open {
		t.Fatal("Subscription Enter must open the modal")
	}
}

func TestSettingsArrows_doNotChangeSubscription(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	before := m.CurrentClaudeConfigFile()
	m.settingsValueRight()
	m.settingsValueLeft()
	if got := m.CurrentClaudeConfigFile(); got != before {
		t.Fatalf("settings arrows changed profile to %q", got)
	}
	if _, err := os.Stat(m.claudeConfigFile); !os.IsNotExist(err) {
		t.Fatalf("settings arrows wrote active pointer: %v", err)
	}
}

func TestPlanEnter_opensSubscriptionModal(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.focus = FocusSubscription
	_, _ = m.focusEnter()
	if !m.subscriptionModal.open {
		t.Fatal("PLAN Enter must open the modal")
	}
}

func TestSubscriptionModal_EscClosesOnlyModal(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.subscriptionModal.open {
		t.Fatal("Esc did not close modal")
	}
	if m.quitting {
		t.Fatal("Esc from modal quit Wisp Deck")
	}
	if m.activeTab != TabSettings {
		t.Fatalf("Esc changed active tab to %v", m.activeTab)
	}
}

func TestSubscriptionModal_CtrlCClosesOnlyModal(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.subscriptionModal.open {
		t.Fatal("Ctrl+C did not close modal")
	}
	if m.quitting {
		t.Fatal("Ctrl+C from modal quit Wisp Deck")
	}
}

func TestSubscriptionModal_previewDoesNotPersistActiveProfile(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(2)

	if got := m.subscriptionModalProfile().File; got != "xiaomi-mimo.json" {
		t.Fatalf("preview file = %q, want xiaomi-mimo.json", got)
	}
	if got := claudeconfig.GetActive(m.claudeConfigFile); got != "" {
		t.Fatalf("preview persisted %q", got)
	}
	if got := m.CurrentClaudeConfigFile(); got != "" {
		t.Fatalf("preview changed in-memory active profile to %q", got)
	}
}

func TestSubscriptionModal_useProfilePersists(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(2) // ready Xiaomi MiMo

	m.useSubscriptionProfile()

	if got := m.CurrentClaudeConfigFile(); got != "xiaomi-mimo.json" {
		t.Fatalf("active file = %q, want xiaomi-mimo.json", got)
	}
	if got := claudeconfig.GetActive(m.claudeConfigFile); got != "xiaomi-mimo.json" {
		t.Fatalf("pointer = %q, want xiaomi-mimo.json", got)
	}
	if m.subscriptionModal.err != nil {
		t.Fatalf("activation error: %v", m.subscriptionModal.err)
	}
}

func TestSubscriptionModal_refusesUnreadyProfile(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(1) // keyless Zhipu

	m.useSubscriptionProfile()

	if got := m.CurrentClaudeConfigFile(); got != "" {
		t.Fatalf("unready profile became active: %q", got)
	}
	if got := claudeconfig.GetActive(m.claudeConfigFile); got != "" {
		t.Fatalf("unready profile wrote pointer: %q", got)
	}
	if m.subscriptionModal.err == nil {
		t.Fatal("unready activation did not report an inline error")
	}
}

func TestSubscriptionModal_mappingDraftDoesNotWriteUntilSave(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(2) // Xiaomi MiMo
	m.subscriptionModal.pane = subscriptionDetailsPane
	m.subscriptionModal.detailCursor = subscriptionDetailOpus

	before := claudeconfig.ReadModelMappings(
		m.claudeConfigsDir,
		"xiaomi-mimo.json",
		claudeconfig.ProviderModels["mimo"],
	)
	m.cycleSubscriptionMapping("next")
	if !m.subscriptionModal.draft.dirty {
		t.Fatal("mapping edit did not dirty the draft")
	}
	onDisk := claudeconfig.ReadModelMappings(
		m.claudeConfigsDir,
		"xiaomi-mimo.json",
		claudeconfig.ProviderModels["mimo"],
	)
	if onDisk != before {
		t.Fatalf("mapping edit wrote before Save: before %v after %v", before, onDisk)
	}

	m.saveSubscriptionDraft()
	saved := claudeconfig.ReadModelMappings(
		m.claudeConfigsDir,
		"xiaomi-mimo.json",
		claudeconfig.ProviderModels["mimo"],
	)
	if saved != m.subscriptionModal.draft.mappings {
		t.Fatalf("saved mappings = %v, draft %v", saved, m.subscriptionModal.draft.mappings)
	}
	if m.subscriptionModal.draft.dirty {
		t.Fatal("successful Save left draft dirty")
	}
}

func TestSubscriptionModal_dirtyProfileSwitchRequiresDiscard(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(2)
	m.subscriptionModal.detailCursor = subscriptionDetailOpus
	m.cycleSubscriptionMapping("next")

	m.moveSubscriptionProfile(-1)
	if m.subscriptionModal.mode != subscriptionDiscardConfirm {
		t.Fatalf("dirty switch mode = %v, want discard confirmation", m.subscriptionModal.mode)
	}
	if got := m.subscriptionModalProfile().File; got != "xiaomi-mimo.json" {
		t.Fatalf("dirty switch moved cursor early to %q", got)
	}

	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.subscriptionModal.mode != subscriptionBrowse {
		t.Fatalf("confirmed discard mode = %v, want browse", m.subscriptionModal.mode)
	}
	if got := m.subscriptionModalProfile().File; got != "zhipu-glm.json" {
		t.Fatalf("confirmed switch focused %q, want zhipu-glm.json", got)
	}
	if m.subscriptionModal.draft.dirty {
		t.Fatal("discard confirmation kept dirty draft")
	}
}

func TestSubscriptionModal_saveAPIKeyDraft(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(1) // keyless Zhipu
	m.subscriptionModal.draft.apiKey = "sk-new"
	m.subscriptionModal.draft.keyEdited = true
	m.subscriptionModal.draft.dirty = true

	m.saveSubscriptionDraft()

	if got := claudeconfig.ReadAPIKey(m.claudeConfigsDir, "zhipu-glm.json"); got != "sk-new" {
		t.Fatalf("saved API key = %q, want sk-new", got)
	}
	if !m.subscriptionModalProfile().Ready {
		t.Fatal("saved API key did not refresh readiness")
	}
}

func TestSubscriptionModal_chatGPTDoesNotEnterKeyEditor(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(3) // OpenAI GPT

	cmd := m.beginSubscriptionKeyEdit()

	if cmd != nil {
		t.Fatal("ChatGPT key editor returned a command")
	}
	if m.subscriptionModal.mode == subscriptionEditKey {
		t.Fatal("ChatGPT exposed API-key editing")
	}
}

func TestSubscriptionModal_compactEscReturnsToProfileList(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.width = 60
	m.openSubscriptionModal()
	m.subscriptionModal.pane = subscriptionDetailsPane

	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	if !m.subscriptionModal.open {
		t.Fatal("Esc from compact details closed the modal")
	}
	if m.subscriptionModal.pane != subscriptionProfilesPane {
		t.Fatalf("Esc pane = %v, want profiles", m.subscriptionModal.pane)
	}
}

func TestSubscriptionModal_compactEnterOpensProfileDetails(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.width = 60
	m.openSubscriptionModal()

	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.subscriptionModal.pane != subscriptionDetailsPane {
		t.Fatalf("compact Enter pane = %v, want details", m.subscriptionModal.pane)
	}
}

func TestSubscriptionModal_wideRightEntersAndNavigatesProfileDetails(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(1) // Zhipu GLM; detail cursor starts on Opus.
	profileCursor := m.subscriptionModal.profileCursor

	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRight})
	if m.subscriptionModal.pane != subscriptionDetailsPane {
		t.Fatalf("wide Right pane = %v, want details", m.subscriptionModal.pane)
	}

	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.subscriptionModal.detailCursor != subscriptionDetailSonnet {
		t.Fatalf("Down after Right selected detail %d, want Sonnet", m.subscriptionModal.detailCursor)
	}
	if m.subscriptionModal.profileCursor != profileCursor {
		t.Fatalf("detail navigation moved profile cursor from %d to %d", profileCursor, m.subscriptionModal.profileCursor)
	}
}

func TestSubscriptionModal_wideLeftReturnsFromDetailsToProfiles(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(1)
	m.subscriptionModal.pane = subscriptionDetailsPane
	m.subscriptionModal.detailCursor = subscriptionDetailOpus
	before := m.subscriptionModal.draft.mappings

	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyLeft})

	if m.subscriptionModal.pane != subscriptionProfilesPane {
		t.Fatalf("wide Left pane = %v, want profiles", m.subscriptionModal.pane)
	}
	if got := m.subscriptionModal.draft.mappings; got != before {
		t.Fatalf("wide Left changed mapping from %v to %v instead of returning", before, got)
	}
}

func TestSubscriptionModal_leftFromRenameReturnsToProfiles(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(2)
	m.subscriptionModal.pane = subscriptionDetailsPane
	m.subscriptionModal.detailCursor = subscriptionDetailRename

	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.subscriptionModal.pane != subscriptionProfilesPane {
		t.Fatalf("Left from Rename pane = %v, want profiles", m.subscriptionModal.pane)
	}
}

func TestSubscriptionModal_actionRowUsesHorizontalKeyboardNavigation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		rights   int
		wantMode subscriptionModalMode
	}{
		{name: "Rename", rights: 0, wantMode: subscriptionRename},
		{name: "Delete", rights: 1, wantMode: subscriptionDeleteConfirm},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newSubscriptionModalMenu(t)
			m.openSubscriptionModal()
			m.moveSubscriptionProfile(2)
			m.subscriptionModal.pane = subscriptionDetailsPane
			m.subscriptionModal.detailCursor = subscriptionDetailRename

			for range tc.rights {
				m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRight})
			}
			m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})

			if m.subscriptionModal.mode != tc.wantMode {
				t.Fatalf("mode = %v, want %v after %d Right presses", m.subscriptionModal.mode, tc.wantMode, tc.rights)
			}
		})
	}
}

func TestSubscriptionModal_actionRowCanReachSaveWithKeyboard(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(2)
	m.subscriptionModal.pane = subscriptionDetailsPane
	m.subscriptionModal.detailCursor = subscriptionDetailRename
	m.subscriptionModal.draft.dirty = true

	for range 2 {
		m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRight})
	}
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.subscriptionModal.draft.dirty {
		t.Fatal("two Right presses from Rename did not focus and activate Save")
	}
}

func TestSubscriptionModal_addRowCannotActivateLastProfile(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.subscriptionModal.profileCursor = len(m.subscriptionProfiles())

	m = subscriptionRune(t, m, 'u')

	if got := m.CurrentClaudeConfigFile(); got != "" {
		t.Fatalf("Use on Add row activated %q", got)
	}
	card := stripAnsi(m.renderSubscriptionModalCard())
	if !strings.Contains(card, "Connect another provider") {
		t.Fatalf("Add row renders another profile's details:\n%s", card)
	}
}

func TestSubscriptionModal_addPromptEnterOpensProviderChooser(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.subscriptionModal.profileCursor = len(m.subscriptionProfiles())
	m.subscriptionModal.pane = subscriptionDetailsPane

	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.subscriptionModal.mode != subscriptionAddProvider {
		t.Fatalf("Add prompt Enter mode = %v, want provider chooser", m.subscriptionModal.mode)
	}
}

func TestSubscriptionModal_profileCursorStaysInScrolledViewport(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("Extra profile %02d", i)
		if _, err := claudeconfig.AddForProvider(
			m.claudeConfigsList,
			m.claudeConfigsDir,
			name,
			"mimo",
		); err != nil {
			t.Fatal(err)
		}
	}
	m.SetClaudeConfigs(LoadClaudeConfigsList(m.claudeConfigsList))
	m.height = 12
	m.openSubscriptionModal()
	for range len(m.subscriptionProfiles()) {
		m.moveSubscriptionProfile(1)
	}

	if m.subscriptionModal.profileOffset == 0 {
		t.Fatal("long profile inventory did not scroll")
	}
	card := stripAnsi(m.renderSubscriptionModalCard())
	if !strings.Contains(card, "+ Add profile") {
		t.Fatalf("selected Add row is outside viewport:\n%s", card)
	}
}

func TestSubscriptionModal_chatGPTNavigationIncludesLoginAction(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(3) // OpenAI GPT
	m.subscriptionModal.pane = subscriptionDetailsPane
	m.subscriptionModal.detailCursor = subscriptionDetailFable

	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyDown})

	if m.subscriptionModal.detailCursor != subscriptionDetailAuth {
		t.Fatalf(
			"Down from Fable selected %d, want login action (%d)",
			m.subscriptionModal.detailCursor,
			subscriptionDetailAuth,
		)
	}

	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.subscriptionModal.detailCursor != subscriptionDetailRename {
		t.Fatalf("Down from login selected %d, want Rename", m.subscriptionModal.detailCursor)
	}
}
