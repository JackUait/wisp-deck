package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

func subscriptionRune(t *testing.T, m *MainMenuModel, r rune) *MainMenuModel {
	t.Helper()
	return subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
}

func findSubscriptionConfig(configs []ClaudeConfig, name string) ClaudeConfig {
	for _, config := range configs {
		if config.Name == name {
			return config
		}
	}
	return ClaudeConfig{}
}

func TestSubscriptionModal_addShowsEveryProvider(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()

	m = subscriptionRune(t, m, 'a')

	if m.subscriptionModal.mode != subscriptionAddProvider {
		t.Fatalf("add mode = %v, want provider chooser", m.subscriptionModal.mode)
	}
	card := stripAnsi(m.renderSubscriptionModalCard())
	for _, provider := range claudeconfig.Providers {
		if !strings.Contains(card, provider.Name) {
			t.Errorf("provider chooser missing %q:\n%s", provider.Name, card)
		}
	}
}

func TestSubscriptionModal_addProviderProfile(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m = subscriptionRune(t, m, 'a')
	m.subscriptionModal.providerCursor = 2 // OpenAI / ChatGPT
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.subscriptionModal.mode != subscriptionAddName {
		t.Fatalf("provider Enter mode = %v, want name input", m.subscriptionModal.mode)
	}
	m.subscriptionModal.input.SetValue("Research GPT")
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	cfg := findSubscriptionConfig(m.claudeConfigs, "Research GPT")
	if cfg.File == "" {
		t.Fatal("new profile missing from inventory")
	}
	provider := claudeconfig.ProviderForConfig(
		m.claudeConfigsDir,
		claudeconfig.Config{Name: cfg.Name, File: cfg.File},
	)
	if provider.Key != "openai-chatgpt" {
		t.Fatalf("provider = %q, want openai-chatgpt", provider.Key)
	}
	if m.CurrentClaudeConfigFile() == cfg.File {
		t.Fatal("new profile became active without Use")
	}
	if got := m.subscriptionModalProfile().File; got != cfg.File {
		t.Fatalf("focused file = %q, want new file %q", got, cfg.File)
	}
	if m.subscriptionModal.mode != subscriptionBrowse {
		t.Fatalf("successful add mode = %v, want browse", m.subscriptionModal.mode)
	}
}

func TestSubscriptionModal_emptyAddNameStaysInInput(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m = subscriptionRune(t, m, 'a')
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m.subscriptionModal.input.SetValue("   ")

	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.subscriptionModal.mode != subscriptionAddName {
		t.Fatalf("empty name mode = %v, want add-name input", m.subscriptionModal.mode)
	}
	if m.subscriptionModal.err == nil {
		t.Fatal("empty name did not report an error")
	}
}

func TestSubscriptionModal_renameProfilePreservesProvider(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(2) // Xiaomi MiMo

	m = subscriptionRune(t, m, 'r')
	if m.subscriptionModal.mode != subscriptionRename {
		t.Fatalf("rename mode = %v", m.subscriptionModal.mode)
	}
	m.subscriptionModal.input.SetValue("Research MiMo")
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	cfg := findSubscriptionConfig(m.claudeConfigs, "Research MiMo")
	if cfg.File != "xiaomi-mimo.json" {
		t.Fatalf("renamed config = %+v", cfg)
	}
	provider := claudeconfig.ProviderForConfig(
		m.claudeConfigsDir,
		claudeconfig.Config{Name: cfg.Name, File: cfg.File},
	)
	if provider.Key != "mimo" {
		t.Fatalf("rename changed provider to %q", provider.Key)
	}
}

func TestSubscriptionModal_standardCannotRenameOrDelete(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()

	m = subscriptionRune(t, m, 'r')
	if m.subscriptionModal.mode != subscriptionBrowse {
		t.Fatalf("Standard rename entered mode %v", m.subscriptionModal.mode)
	}
	m = subscriptionRune(t, m, 'd')
	if m.subscriptionModal.mode != subscriptionBrowse {
		t.Fatalf("Standard delete entered mode %v", m.subscriptionModal.mode)
	}
}

func TestSubscriptionModal_deleteActiveProfileResetsStandard(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.SetActiveClaudeConfig("xiaomi-mimo.json")
	if err := claudeconfig.SetActive(m.claudeConfigFile, "xiaomi-mimo.json"); err != nil {
		t.Fatal(err)
	}
	m.openSubscriptionModal()

	m = subscriptionRune(t, m, 'd')
	if m.subscriptionModal.mode != subscriptionDeleteConfirm {
		t.Fatalf("delete mode = %v, want confirmation", m.subscriptionModal.mode)
	}
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if cfg := findSubscriptionConfig(m.claudeConfigs, "Xiaomi MiMo"); cfg.File != "" {
		t.Fatalf("deleted config remains: %+v", cfg)
	}
	if got := m.CurrentClaudeConfigFile(); got != "" {
		t.Fatalf("active profile after delete = %q, want Standard", got)
	}
	if got := claudeconfig.GetActive(m.claudeConfigFile); got != "" {
		t.Fatalf("pointer after active delete = %q", got)
	}
	if !m.subscriptionModalProfile().Standard {
		t.Fatalf("focused profile after active delete = %+v, want Standard", m.subscriptionModalProfile())
	}
}

func TestSubscriptionModal_EscCancelsLifecycleMode(t *testing.T) {
	for _, key := range []rune{'a', 'r', 'd'} {
		t.Run(string(key), func(t *testing.T) {
			m := newSubscriptionModalMenu(t)
			m.openSubscriptionModal()
			if key != 'a' {
				m.moveSubscriptionProfile(2)
			}
			m = subscriptionRune(t, m, key)
			if m.subscriptionModal.mode == subscriptionBrowse {
				t.Fatalf("%q did not enter lifecycle mode", key)
			}
			m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
			if m.subscriptionModal.mode != subscriptionBrowse || !m.subscriptionModal.open {
				t.Fatalf("Esc mode/open = %v/%v, want browse/open", m.subscriptionModal.mode, m.subscriptionModal.open)
			}
		})
	}
}

func TestSubscriptionModal_dirtyAddContinuesAfterDiscard(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(2)
	m.subscriptionModal.detailCursor = subscriptionDetailOpus
	m.cycleSubscriptionMapping("next")

	m = subscriptionRune(t, m, 'a')
	if m.subscriptionModal.mode != subscriptionDiscardConfirm {
		t.Fatalf("dirty Add mode = %v, want discard confirmation", m.subscriptionModal.mode)
	}
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.subscriptionModal.mode != subscriptionAddProvider {
		t.Fatalf("confirmed Add mode = %v, want provider chooser", m.subscriptionModal.mode)
	}
	if m.subscriptionModal.draft.dirty {
		t.Fatal("confirmed Add kept the old draft dirty")
	}
}

func TestSubscriptionModal_dirtyRenameAndDeleteRequireDiscard(t *testing.T) {
	for _, tc := range []struct {
		key      rune
		wantMode subscriptionModalMode
	}{
		{key: 'r', wantMode: subscriptionRename},
		{key: 'd', wantMode: subscriptionDeleteConfirm},
	} {
		t.Run(string(tc.key), func(t *testing.T) {
			m := newSubscriptionModalMenu(t)
			m.openSubscriptionModal()
			m.moveSubscriptionProfile(2)
			m.subscriptionModal.detailCursor = subscriptionDetailOpus
			m.cycleSubscriptionMapping("next")

			m = subscriptionRune(t, m, tc.key)
			if m.subscriptionModal.mode != subscriptionDiscardConfirm {
				t.Fatalf("dirty %q mode = %v, want discard confirmation", tc.key, m.subscriptionModal.mode)
			}
			m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})

			if m.subscriptionModal.mode != tc.wantMode {
				t.Fatalf("confirmed %q mode = %v, want %v", tc.key, m.subscriptionModal.mode, tc.wantMode)
			}
			if m.subscriptionModal.draft.dirty {
				t.Fatalf("confirmed %q kept the old draft dirty", tc.key)
			}
		})
	}
}
