package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/claudeconfig"
	"github.com/jackuait/wisp-deck/internal/featherless"
)

var errFetchFailed = errors.New("featherless: catalog returned 502 Bad Gateway")

func pickerCorpus() []featherless.Model {
	return []featherless.Model{
		{ID: "moonshotai/Kimi-K3", Class: "kimi3-2780b", Context: 262144, InPerM: 3, OutPerM: 15, ImageInput: true, OnPlan: true},
		{ID: "zai-org/GLM-5.2", Class: "glm52-753b", Context: 262144, InPerM: 0.75, OutPerM: 2.4, OnPlan: true},
		{ID: "unsloth/Llama-3.3-70B-Instruct", Class: "llama33-70b", Context: 32768, InPerM: 2.6, OutPerM: 3, OnPlan: true},
		{ID: "gated/Off-Plan-Model", Class: "x-70b", Context: 32768, InPerM: 1, OutPerM: 1, OnPlan: false},
	}
}

// openPickerOn focuses a featherless profile and opens the picker on it.
func openPickerOn(t *testing.T, name string) *MainMenuModel {
	t.Helper()
	m := newSubscriptionModalMenu(t)
	addFeatherlessProfile(t, m, name)
	m.featherlessCachePath = filepath.Join(t.TempDir(), "featherless-models.json")
	m.openSubscriptionModelPicker()
	m.Update(featherlessCatalogMsg{models: pickerCorpus()})
	return m
}

// The catalog is a 7MB HTTP round trip: fetching it inside Update would freeze
// the menu, so the picker opens in a loading state and is filled by a message.
func TestModelPicker_opens_loading_and_fills_from_the_catalog_message(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	addFeatherlessProfile(t, m, "Featherless")
	m.featherlessCachePath = filepath.Join(t.TempDir(), "featherless-models.json")

	cmd := m.openSubscriptionModelPicker()
	if cmd == nil {
		t.Fatal("opening the picker must return a fetch command")
	}
	if m.subscriptionModal.mode != subscriptionPickModel {
		t.Fatal("the picker did not become the active mode")
	}
	if !m.subscriptionModal.picker.loading {
		t.Error("the picker must show a loading state until the catalog arrives")
	}

	m.Update(featherlessCatalogMsg{models: pickerCorpus()})
	if m.subscriptionModal.picker.loading {
		t.Error("the catalog arrived; loading must clear")
	}
	if len(m.subscriptionModal.picker.filtered) != 4 {
		t.Errorf("filtered = %d, want the whole corpus", len(m.subscriptionModal.picker.filtered))
	}
}

func TestModelPicker_typing_narrows_the_list(t *testing.T) {
	m := openPickerOn(t, "Featherless")

	for _, r := range "kimi" {
		m.updateSubscriptionModelPicker(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.subscriptionModal.picker.filtered; len(got) != 1 || got[0].ID != "moonshotai/Kimi-K3" {
		t.Fatalf("filtered = %v, want just Kimi-K3", got)
	}
	if m.subscriptionModal.picker.cursor != 0 {
		t.Error("narrowing must reset the cursor into the visible list")
	}
}

// The pick is what declares the context window; without it the session falls
// back to a flat 200000 that strands a 32768 model permanently.
func TestModelPicker_enter_stamps_model_window_and_images(t *testing.T) {
	m := openPickerOn(t, "Featherless")

	// Second row: GLM-5.2, which declares no image_input.
	m.updateSubscriptionModelPicker(tea.KeyMsg{Type: tea.KeyDown})
	m.updateSubscriptionModelPicker(tea.KeyMsg{Type: tea.KeyEnter})

	draft := m.subscriptionModal.draft
	if draft.model != "zai-org/GLM-5.2" {
		t.Errorf("model = %q, want the highlighted row", draft.model)
	}
	if draft.window != "262144" {
		t.Errorf("window = %q, want the model's context_length", draft.window)
	}
	if !draft.imagesBlocked {
		t.Error("a model with no image_input must default to blocking image reads")
	}
	if !draft.customEdited || !draft.dirty {
		t.Error("a pick must mark the draft dirty so it can be saved")
	}
	if m.subscriptionModal.mode != subscriptionBrowse {
		t.Error("the picker must close on Enter")
	}
}

func TestModelPicker_a_vision_model_leaves_images_enabled(t *testing.T) {
	m := openPickerOn(t, "Featherless")
	m.updateSubscriptionModelPicker(tea.KeyMsg{Type: tea.KeyEnter})

	if m.subscriptionModal.draft.imagesBlocked {
		t.Error("Kimi-K3 accepts images; blocking reads would remove a working capability")
	}
}

// A plan that will not run the model produces a turn that fails on every send,
// so it is visible but unpickable.
func TestModelPicker_refuses_a_model_that_is_off_the_plan(t *testing.T) {
	m := openPickerOn(t, "Featherless")

	for i := 0; i < 3; i++ {
		m.updateSubscriptionModelPicker(tea.KeyMsg{Type: tea.KeyDown})
	}
	m.updateSubscriptionModelPicker(tea.KeyMsg{Type: tea.KeyEnter})

	if m.subscriptionModal.draft.model == "gated/Off-Plan-Model" {
		t.Error("an off-plan model must not be pickable")
	}
	if m.subscriptionModal.picker.err == nil {
		t.Error("refusing a pick must say why")
	}
}

func TestModelPicker_esc_closes_without_picking(t *testing.T) {
	m := openPickerOn(t, "Featherless")
	m.updateSubscriptionModelPicker(tea.KeyMsg{Type: tea.KeyEsc})

	if m.subscriptionModal.mode != subscriptionBrowse {
		t.Error("Esc must return to browsing")
	}
	if m.subscriptionModal.draft.model != "" {
		t.Error("Esc must not pick anything")
	}
}

// A catalog that cannot be fetched and has no cache must say so and stay
// dismissable, not sit on a spinner forever.
func TestModelPicker_reports_a_fetch_failure(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	addFeatherlessProfile(t, m, "Featherless")
	m.featherlessCachePath = filepath.Join(t.TempDir(), "featherless-models.json")
	m.openSubscriptionModelPicker()
	m.Update(featherlessCatalogMsg{err: errFetchFailed})

	if m.subscriptionModal.picker.loading {
		t.Error("a failed fetch must clear the loading state")
	}
	if m.subscriptionModal.picker.err == nil {
		t.Error("a failed fetch must be reported")
	}
}

// Choosing Featherless with nothing else configured should land on the picker,
// not on a name prompt for a profile with no model.
func TestAddFeatherless_opens_the_picker_before_naming(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.featherlessCachePath = filepath.Join(t.TempDir(), "featherless-models.json")
	m.openSubscriptionModal()
	m.startSubscriptionAdd()
	for i, provider := range claudeconfig.Providers {
		if provider.Key == "featherless" {
			m.subscriptionModal.providerCursor = i
		}
	}
	m.beginSubscriptionAddName()

	if m.subscriptionModal.mode != subscriptionPickModel {
		t.Fatal("picking Featherless must open the model picker first")
	}
}

// The name is derived from the model so a user can add several Featherless
// profiles without inventing names for them.
func TestFeatherlessProfileName_is_derived_from_the_model(t *testing.T) {
	for model, want := range map[string]string{
		"moonshotai/Kimi-K3":             "Featherless Kimi-K3",
		"zai-org/GLM-5.2":                "Featherless GLM-5.2",
		"unsloth/Llama-3.3-70B-Instruct": "Featherless Llama-3.3-70B-Instruct",
	} {
		if got := featherlessProfileName(model); got != want {
			t.Errorf("featherlessProfileName(%q) = %q, want %q", model, got, want)
		}
	}
}

// Adding a second model should not mean typing the key again.
func TestAddFeatherless_reuses_a_sibling_profiles_key(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.featherlessCachePath = filepath.Join(t.TempDir(), "featherless-models.json")
	existing := addFeatherlessProfile(t, m, "Featherless Kimi-K3")
	if err := claudeconfig.WriteAPIKey(m.claudeConfigsDir, existing, "rc_shared"); err != nil {
		t.Fatal(err)
	}
	m.SetClaudeConfigs(LoadClaudeConfigsList(m.claudeConfigsList))

	m.subscriptionModal.providerKey = "featherless"
	m.subscriptionModal.draft = subscriptionDraft{}
	m.subscriptionModal.pendingModel = &featherless.Model{
		ID: "zai-org/GLM-5.2", Context: 262144, OnPlan: true,
	}
	m.addSubscriptionProfile("Featherless GLM-5.2")
	if m.subscriptionModal.err != nil {
		t.Fatalf("add: %v", m.subscriptionModal.err)
	}

	file := m.subscriptionModalProfile().File
	if got := claudeconfig.ReadAPIKey(m.claudeConfigsDir, file); got != "rc_shared" {
		t.Errorf("key = %q, want the sibling profile's key reused", got)
	}
	if got := claudeconfig.ReadCustomModel(m.claudeConfigsDir, file); got != "zai-org/GLM-5.2" {
		t.Errorf("model = %q, want the pending pick applied", got)
	}
	if got := claudeconfig.ReadContextWindow(m.claudeConfigsDir, file); got != "262144" {
		t.Errorf("window = %q, want the pick's context length", got)
	}
	if !m.subscriptionModalProfile().Ready {
		t.Error("model + window + reused key must make the profile ready immediately")
	}
}

// The query field renders its own "> " prompt, so the highlighted row must not
// also start with one — two arrows on screen read as two cursors. The rest of
// the modal marks its cursor with ▌.
func TestModelPickerLines_marks_the_cursor_the_way_the_rest_of_the_modal_does(t *testing.T) {
	m := openPickerOn(t, "Featherless")
	lines := m.subscriptionModelPickerLines(60, 12)

	var row string
	for _, line := range lines {
		if strings.Contains(line, "moonshotai/Kimi-K3") {
			row = line
		}
	}
	if row == "" {
		t.Fatalf("the cursor row is not in the render: %q", lines)
	}
	if !strings.Contains(row, "▌") {
		t.Errorf("cursor row = %q, want the modal's ▌ marker", row)
	}
	if strings.Contains(row, ">") {
		t.Errorf("cursor row = %q, want no > — the query field already renders one", row)
	}
}

// Every row carries what the choice turns on: the id, the window it declares,
// and what it costs.
func TestModelPickerLines_show_the_window_and_the_price(t *testing.T) {
	m := openPickerOn(t, "Featherless")
	joined := strings.Join(m.subscriptionModelPickerLines(60, 12), "\n")

	for _, want := range []string{"moonshotai/Kimi-K3", "256K", "$3/$15", "(not on plan)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("render is missing %q:\n%s", want, joined)
		}
	}
}

// Abandoning the name prompt abandons the pick. Left set, it is applied to
// whatever profile is created next — writing a Featherless model id, window and
// API key onto an unrelated gateway's profile and breaking it.
func TestAddFeatherless_a_cancelled_name_prompt_drops_the_pick(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.featherlessCachePath = filepath.Join(t.TempDir(), "featherless-models.json")
	m.openSubscriptionModal()
	m.startSubscriptionAdd()
	for i, provider := range claudeconfig.Providers {
		if provider.Key == "featherless" {
			m.subscriptionModal.providerCursor = i
		}
	}
	m.beginSubscriptionAddName()
	m.Update(featherlessCatalogMsg{models: pickerCorpus()})
	m.updateSubscriptionModelPicker(tea.KeyMsg{Type: tea.KeyEnter})
	if m.subscriptionModal.pendingModel == nil {
		t.Fatal("the pick was not recorded")
	}

	m.updateSubscriptionNameInput(tea.KeyMsg{Type: tea.KeyEsc})
	if m.subscriptionModal.pendingModel != nil {
		t.Error("a cancelled name prompt must drop the pick")
	}

	// Now add an unrelated gateway; it must be untouched by the abandoned pick.
	m.subscriptionModal.providerKey = "zhipu"
	m.subscriptionModal.draft = subscriptionDraft{}
	m.addSubscriptionProfile("Zhipu GLM Two")
	if m.subscriptionModal.err != nil {
		t.Fatalf("add: %v", m.subscriptionModal.err)
	}
	file := m.subscriptionModalProfile().File
	if got := claudeconfig.ReadCustomModel(m.claudeConfigsDir, file); got == "moonshotai/Kimi-K3" {
		t.Errorf("the abandoned Featherless pick was written onto a zhipu profile: %q", got)
	}
	if got := claudeconfig.ReadAPIKey(m.claudeConfigsDir, file); got != "" {
		t.Errorf("a zhipu profile was given a key it never asked for: %q", got)
	}
}

// Even if a pick somehow survives, it belongs only to a remote-catalog profile:
// the model id means nothing to another gateway.
func TestApplyPendingSubscriptionModel_refuses_a_provider_that_did_not_pick(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.subscriptionModal.pendingModel = &featherless.Model{ID: "moonshotai/Kimi-K3", Context: 262144}
	m.subscriptionModal.providerKey = "zhipu"
	m.subscriptionModal.draft = subscriptionDraft{}
	m.addSubscriptionProfile("Zhipu GLM Three")
	if m.subscriptionModal.err != nil {
		t.Fatalf("add: %v", m.subscriptionModal.err)
	}
	file := m.subscriptionModalProfile().File
	if got := claudeconfig.ReadCustomModel(m.claudeConfigsDir, file); got == "moonshotai/Kimi-K3" {
		t.Errorf("a featherless pick reached a zhipu profile: %q", got)
	}
}

// A 32k model runs Claude Code only with MCP servers off: their schemas cost
// ~18k tokens on top of a ~23k base prompt. The row says so rather than hiding
// the model, because a small window is a real choice for short tasks.
func TestModelPickerLines_mark_windows_that_cannot_hold_MCP(t *testing.T) {
	m := openPickerOn(t, "Featherless")
	lines := m.subscriptionModelPickerLines(80, 12)

	rowFor := func(id string) string {
		t.Helper()
		for _, line := range lines {
			if strings.Contains(line, id) {
				return stripAnsi(line)
			}
		}
		t.Fatalf("no row for %q:\n%s", id, strings.Join(lines, "\n"))
		return ""
	}

	small := rowFor("unsloth/Llama-3.3-70B-Instruct")
	if !strings.Contains(small, "needs MCP off") {
		t.Errorf("32K row = %q, want it to flag that MCP cannot fit", small)
	}
	large := rowFor("moonshotai/Kimi-K3")
	if strings.Contains(large, "needs MCP off") {
		t.Errorf("256K row = %q, want no MCP warning", large)
	}
}

// The modal's two-pane renderer indexes left[i] and right[i] for the full body
// height, so a pane that returns fewer lines than it was asked for panics the
// whole menu. The picker's loading, error and no-match states are the short
// ones — this crashed a real session at "Project menu failed (exit 1)".
func TestModelPickerLines_always_fills_the_height_it_was_given(t *testing.T) {
	m := openPickerOn(t, "Featherless")
	picker := &m.subscriptionModal.picker

	states := map[string]func(){
		"ready":    func() { picker.loading, picker.err = false, nil },
		"loading":  func() { picker.loading, picker.err = true, nil },
		"error":    func() { picker.loading, picker.err = false, errFetchFailed },
		"no match": func() { picker.loading, picker.err = false, nil; picker.filtered = nil },
	}
	for name, set := range states {
		for _, height := range []int{2, 4, 5, 12, 30} {
			set()
			if got := len(m.subscriptionModelPickerLines(60, height)); got != height {
				t.Errorf("%s at height %d returned %d lines, want exactly %d",
					name, height, got, height)
			}
		}
	}
}

// The same invariant through the modal's own dispatcher, which is what the
// two-pane renderer actually calls.
func TestSubscriptionDetailLines_fills_the_height_while_the_picker_is_open(t *testing.T) {
	m := openPickerOn(t, "Featherless")
	m.subscriptionModal.picker.loading = true
	if got := len(m.subscriptionDetailLines(60, 18)); got != 18 {
		t.Errorf("detail pane returned %d lines, want 18", got)
	}
}

// End to end: rendering the whole menu with the picker open must not panic.
func TestMainMenuView_survives_an_open_model_picker(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.featherlessCachePath = filepath.Join(t.TempDir(), "f.json")
	m.SetSize(120, 40)
	addFeatherlessProfile(t, m, "Featherless")
	m.openSubscriptionModelPicker()
	_ = m.View() // loading
	m.Update(featherlessCatalogMsg{err: errFetchFailed})
	_ = m.View() // error
	m.Update(featherlessCatalogMsg{models: pickerCorpus()})
	_ = m.View() // ready
}

// renderSubscriptionModalCard returns the WHOLE card. Returning the picker's
// help string from it replaced the card with that one line, which then painted
// over the settings menu underneath — the modal simply never appeared.
func TestModalCard_with_the_picker_open_is_a_card_not_a_help_line(t *testing.T) {
	m := openPickerOn(t, "Featherless")
	m.SetSize(120, 40)
	card := m.renderSubscriptionModalCard()

	if !strings.Contains(card, "Pick a Featherless model") {
		t.Errorf("the card does not render the picker:\n%s", card)
	}
	if !strings.Contains(card, "moonshotai/Kimi-K3") {
		t.Errorf("the card does not list the models:\n%s", card)
	}
	if lines := strings.Count(card, "\n"); lines < 10 {
		t.Errorf("card is %d lines, want a full modal card:\n%s", lines+1, card)
	}
	// The help line belongs in the card's footer, not instead of the card.
	if !strings.Contains(card, "Enter pick") {
		t.Errorf("the picker's help line is missing from the card:\n%s", card)
	}
}

// The picked model's whole context is what Claude Code has to fit both the
// conversation and its reply into. It sizes max_tokens from its own catalog,
// which has never heard of a Featherless model, and asks for 32000 — so a
// 32768-token pick left ~768 tokens for everything else and Featherless
// rejected every turn with "The request was rejected as invalid". The profile
// the pick writes has to reserve the reply's room out of the window.
func TestApplyPendingSubscriptionModel_reserves_output_room_for_a_small_window(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	file := addFeatherlessProfile(t, m, "Featherless Small")
	// The add flow stamps this when the provider is chosen; only that provider
	// may receive a Featherless pick.
	m.subscriptionModal.providerKey = "featherless"
	m.subscriptionModal.pendingModel = &featherless.Model{
		ID: "unsloth/Llama-3.3-70B-Instruct", Context: 32768, OnPlan: true,
	}
	if err := m.applyPendingSubscriptionModel(file); err != nil {
		t.Fatalf("applyPendingSubscriptionModel: %v", err)
	}
	env := readConfigEnv(t, m.claudeConfigsDir, file)
	if got, _ := env[claudeconfig.OutputReserveKey].(string); got != "8192" {
		t.Errorf("%s = %q, want a quarter of the 32768 window", claudeconfig.OutputReserveKey, got)
	}
	if got, _ := env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"].(string); got != "32768" {
		t.Errorf("declared window = %q, want the endpoint's real 32768", got)
	}
}
