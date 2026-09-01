package tui

import (
	"errors"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
