package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

// addCustomProfile appends a user-configured profile and focuses it.
func addCustomProfile(t *testing.T, m *MainMenuModel, name string) string {
	t.Helper()
	file, err := claudeconfig.AddForProvider(m.claudeConfigsList, m.claudeConfigsDir, name, "custom")
	if err != nil {
		t.Fatal(err)
	}
	m.SetClaudeConfigs(LoadClaudeConfigsList(m.claudeConfigsList))
	m.openSubscriptionModal()
	for steps := 0; m.subscriptionModalProfile().File != file; steps++ {
		if steps > len(m.subscriptionProfiles()) {
			t.Fatalf("profile %q not reachable in the modal", file)
		}
		m.moveSubscriptionProfile(1)
	}
	return file
}

func editSubscriptionField(t *testing.T, m *MainMenuModel, row int, value string) {
	t.Helper()
	m.subscriptionModal.detailCursor = row
	if _, cmd := m.activateSubscriptionDetail(); cmd == nil {
		t.Fatalf("row %d did not open an editor", row)
	}
	if m.subscriptionModal.mode != subscriptionEditField {
		t.Fatalf("row %d left mode %v, want the field editor", row, m.subscriptionModal.mode)
	}
	m.subscriptionModal.input.SetValue(value)
	m.updateSubscriptionFieldInput(tea.KeyMsg{Type: tea.KeyEnter})
}

// The mapping cycler is inert with an empty model list, so a user-configured
// profile must offer text fields where a gateway offers the four aliases.
func TestSubscriptionModal_customProfileOffersTextFieldsInsteadOfMappings(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	addCustomProfile(t, m, "Qwen")

	rows := m.subscriptionDetailRows()
	want := []int{
		subscriptionDetailEndpoint,
		subscriptionDetailModel,
		subscriptionDetailContext,
		subscriptionDetailImages,
		subscriptionDetailAuth,
		subscriptionDetailRename,
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %v, want %v", rows, want)
	}
	for i, row := range want {
		if rows[i] != row {
			t.Fatalf("rows = %v, want %v", rows, want)
		}
	}
}

// The counterweight: a catalog gateway must keep cycling its own model list.
func TestSubscriptionModal_gatewayProfileKeepsItsMappingRows(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(1) // Zhipu GLM

	rows := m.subscriptionDetailRows()
	for _, want := range []int{
		subscriptionDetailOpus, subscriptionDetailSonnet,
		subscriptionDetailHaiku, subscriptionDetailFable,
	} {
		found := false
		for _, row := range rows {
			if row == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("gateway rows = %v, missing alias row %d", rows, want)
		}
	}
	for _, unwanted := range []int{
		subscriptionDetailEndpoint, subscriptionDetailModel, subscriptionDetailContext,
	} {
		for _, row := range rows {
			if row == unwanted {
				t.Errorf("gateway rows = %v, must not offer user-configured row %d", rows, unwanted)
			}
		}
	}
}

// Opening a custom profile used to park the cursor on Opus, a row it does not have.
func TestSubscriptionModal_customProfileFocusesAnExistingRow(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	addCustomProfile(t, m, "Qwen")

	cursor := m.subscriptionModal.detailCursor
	for _, row := range m.subscriptionDetailRows() {
		if row == cursor {
			return
		}
	}
	t.Errorf("detail cursor %d is not among rows %v", cursor, m.subscriptionDetailRows())
}

func TestSubscriptionModal_savingCustomFieldsWritesTheProfile(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	file := addCustomProfile(t, m, "Qwen")

	editSubscriptionField(t, m, subscriptionDetailEndpoint, "https://abc-8000.proxy.runpod.net")
	editSubscriptionField(t, m, subscriptionDetailModel, "qwen3-coder")
	editSubscriptionField(t, m, subscriptionDetailContext, "131072")
	if !m.subscriptionModal.draft.dirty {
		t.Fatal("editing the custom fields left the draft clean")
	}
	m.saveSubscriptionDraft()

	if m.subscriptionModal.err != nil {
		t.Fatalf("save reported %v", m.subscriptionModal.err)
	}
	if got := claudeconfig.ReadBaseURL(m.claudeConfigsDir, file); got != "https://abc-8000.proxy.runpod.net" {
		t.Errorf("endpoint = %q", got)
	}
	if got := claudeconfig.ReadCustomModel(m.claudeConfigsDir, file); got != "qwen3-coder" {
		t.Errorf("model = %q", got)
	}
	if got := claudeconfig.ReadContextWindow(m.claudeConfigsDir, file); got != "131072" {
		t.Errorf("context window = %q", got)
	}
	if m.subscriptionModal.draft.dirty {
		t.Error("save left the draft dirty")
	}
}

// The save path writes model mappings from the draft's (empty) model list,
// which would delete every alias the user just set.
func TestSubscriptionModal_savingACustomProfileKeepsItsModelMapping(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	file := addCustomProfile(t, m, "Qwen")

	editSubscriptionField(t, m, subscriptionDetailModel, "qwen3-coder")
	m.saveSubscriptionDraft()

	m.subscriptionModal.draft.apiKey = "pod-secret"
	m.subscriptionModal.draft.keyEdited = true
	m.subscriptionModal.draft.dirty = true
	m.saveSubscriptionDraft()

	if got := claudeconfig.ReadCustomModel(m.claudeConfigsDir, file); got != "qwen3-coder" {
		t.Errorf("model after a later save = %q, want qwen3-coder", got)
	}
}

func TestSubscriptionModal_customFieldEditorPrefillsTheStoredValue(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	addCustomProfile(t, m, "Qwen")
	editSubscriptionField(t, m, subscriptionDetailModel, "qwen3-coder")
	m.saveSubscriptionDraft()

	m.subscriptionModal.detailCursor = subscriptionDetailModel
	m.activateSubscriptionDetail()
	if got := m.subscriptionModal.input.Value(); got != "qwen3-coder" {
		t.Errorf("editor prefilled with %q, want qwen3-coder", got)
	}
}

// Overshooting the endpoint's real window is unrecoverable, so a value that is
// not a positive integer has to be refused where the user can see it.
func TestSubscriptionModal_rejectedCustomFieldSurfacesTheError(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	file := addCustomProfile(t, m, "Qwen")

	editSubscriptionField(t, m, subscriptionDetailContext, "lots")
	m.saveSubscriptionDraft()

	if m.subscriptionModal.err == nil {
		t.Fatal("save accepted a non-numeric context window")
	}
	if got := claudeconfig.ReadContextWindow(m.claudeConfigsDir, file); got != "" {
		t.Errorf("rejected window was written as %q", got)
	}
	if !m.subscriptionModal.draft.dirty {
		t.Error("a refused save must keep the draft so the value can be corrected")
	}
}

func TestSubscriptionModal_customProfileRendersItsEndpointModelAndWindow(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	addCustomProfile(t, m, "Qwen")
	editSubscriptionField(t, m, subscriptionDetailEndpoint, "https://abc-8000.proxy.runpod.net")
	editSubscriptionField(t, m, subscriptionDetailModel, "qwen3-coder")
	editSubscriptionField(t, m, subscriptionDetailContext, "131072")
	m.saveSubscriptionDraft()
	m.subscriptionModal.mode = subscriptionBrowse

	view := stripAnsi(m.View())
	for _, want := range []string{
		"Custom / self-hosted",
		"Endpoint",
		"https://abc-8000.proxy.runpod.net",
		"Model",
		"qwen3-coder",
		"Context",
		"131072",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"Opus", "Sonnet", "Haiku"} {
		if strings.Contains(view, unwanted) {
			t.Errorf("view shows dead alias row %q:\n%s", unwanted, view)
		}
	}
}

func TestSubscriptionModalHit_customFieldsOpenTheirEditors(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	addCustomProfile(t, m, "Qwen")
	m.subscriptionModal.mode = subscriptionBrowse

	for _, tt := range []struct {
		text string
		row  int
	}{
		{"Endpoint", subscriptionDetailEndpoint},
		{"Model", subscriptionDetailModel},
		{"Context", subscriptionDetailContext},
	} {
		x, y := subscriptionCardCell(t, m, tt.text)
		target := m.subscriptionModalTarget(x, y)
		if target.kind != subscriptionHitField || target.index != tt.row {
			t.Errorf("hit %q = %+v, want field row %d", tt.text, target, tt.row)
			continue
		}
		m.handleSubscriptionModalMouse(
			subscriptionScreenMouse(m, x, y, tea.MouseActionPress, tea.MouseButtonLeft))
		if m.subscriptionModal.mode != subscriptionEditField {
			t.Errorf("clicking %q did not open the editor", tt.text)
		}
		m.subscriptionModal.mode = subscriptionBrowse
	}
}

// A field editor with no render path leaves the user typing into a card that
// still shows the browse view.
func TestSubscriptionModal_fieldEditorRendersItsInput(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	addCustomProfile(t, m, "Qwen")

	for _, tt := range []struct {
		row   int
		title string
	}{
		{subscriptionDetailEndpoint, "EDIT ENDPOINT"},
		{subscriptionDetailModel, "EDIT MODEL"},
		{subscriptionDetailContext, "EDIT CONTEXT"},
	} {
		m.subscriptionModal.detailCursor = tt.row
		m.activateSubscriptionDetail()
		m.subscriptionModal.input.SetValue("typed-value")

		view := stripAnsi(m.View())
		for _, want := range []string{tt.title, "typed-value", "[ Keep changes ]", "[ Cancel ]"} {
			if !strings.Contains(view, want) {
				t.Errorf("row %d editor missing %q:\n%s", tt.row, want, view)
			}
		}
		m.subscriptionModal.mode = subscriptionBrowse
	}
}

// Every other test here calls AddForProvider directly, so none of them touch
// the picker the user actually goes through to create the profile.
func TestSubscriptionModal_addFlowCreatesACustomProfile(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.startSubscriptionAdd()
	if m.subscriptionModal.mode != subscriptionAddProvider {
		t.Fatalf("add did not open the provider picker (mode %v)", m.subscriptionModal.mode)
	}

	found := false
	for range claudeconfig.Providers {
		if claudeconfig.Providers[m.subscriptionModal.providerCursor].Key == "custom" {
			found = true
			break
		}
		m.updateSubscriptionModal(tea.KeyMsg{Type: tea.KeyDown})
	}
	if !found {
		t.Fatal("the picker never reaches the custom provider")
	}
	if view := stripAnsi(m.View()); !strings.Contains(view, "Custom / self-hosted") {
		t.Errorf("picker does not offer the custom provider:\n%s", view)
	}

	m.updateSubscriptionModal(tea.KeyMsg{Type: tea.KeyEnter})
	if m.subscriptionModal.mode != subscriptionAddName {
		t.Fatalf("choosing the provider did not ask for a name (mode %v)", m.subscriptionModal.mode)
	}
	m.subscriptionModal.input.SetValue("Qwen")
	m.updateSubscriptionModal(tea.KeyMsg{Type: tea.KeyEnter})

	profile := m.subscriptionModalProfile()
	if profile.Name != "Qwen" {
		t.Fatalf("focused profile = %q, want the one just created", profile.Name)
	}
	if profile.Provider.Key != "custom" {
		t.Errorf("created profile provider = %q, want custom", profile.Provider.Key)
	}
	if profile.Ready {
		t.Error("a profile with no endpoint, model, or key is selectable")
	}
	// The details pane must already be the field form, not a dead alias cycler.
	rows := m.subscriptionDetailRows()
	if len(rows) == 0 || rows[0] != subscriptionDetailEndpoint {
		t.Errorf("new custom profile opens on rows %v, want the endpoint field first", rows)
	}
}

// A profile created before the byte watchdog was disarmed must not wait for the
// next installer run to stop reporting a working endpoint as a dead network.
// Saving its fields is the moment the user is looking straight at it.
func TestSubscriptionModal_savingACustomProfileDisarmsTheByteWatchdog(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	file := addCustomProfile(t, m, "Qwen")
	path := filepath.Join(m.claudeConfigsDir, file)
	env := readSettingsEnv(t, path)
	delete(env, claudeconfig.ByteWatchdogKey)
	writeSettingsEnv(t, path, env)

	editSubscriptionField(t, m, subscriptionDetailModel, "qwen3-coder")
	m.saveSubscriptionDraft()

	if got := readSettingsEnv(t, path)[claudeconfig.ByteWatchdogKey]; got != "0" {
		t.Errorf("%s after a save = %v, want %q",
			claudeconfig.ByteWatchdogKey, got, "0")
	}
}

func readSettingsEnv(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	env, _ := settings["env"].(map[string]any)
	if env == nil {
		t.Fatalf("%s has no env section", path)
	}
	return env
}

func writeSettingsEnv(t *testing.T, path string, env map[string]any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	settings["env"] = env
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
