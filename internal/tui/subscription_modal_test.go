package tui

import (
	"os"
	"path/filepath"
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
