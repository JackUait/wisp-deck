package tui

import (
	"strings"
	"testing"

	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

func toggleSubscriptionImagesRow(t *testing.T, m *MainMenuModel) {
	t.Helper()
	m.subscriptionModal.detailCursor = subscriptionDetailImages
	if _, cmd := m.activateSubscriptionDetail(); cmd != nil {
		t.Fatal("the images row must toggle in place, not open an editor")
	}
}

// A self-hosted endpoint may serve a text-only model, and Claude Code has no
// switch of its own for that, so the profile is where the user declares it.
func TestSubscriptionModal_customProfileOffersAnImagesToggle(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	addCustomProfile(t, m, "Qwen")

	found := false
	for _, row := range m.subscriptionDetailRows() {
		if row == subscriptionDetailImages {
			found = true
		}
	}
	if !found {
		t.Fatalf("rows = %v, want an images row", m.subscriptionDetailRows())
	}
}

// A vendor gateway serves the catalog's own models, every one of which sees
// images, so the row would only offer a way to break a working profile.
func TestSubscriptionModal_gatewayProfileHasNoImagesToggle(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(1) // Zhipu GLM

	for _, row := range m.subscriptionDetailRows() {
		if row == subscriptionDetailImages {
			t.Fatalf("rows = %v, must not offer the images row", m.subscriptionDetailRows())
		}
	}
}

func TestSubscriptionModal_savingABlockedProfileDeniesImageReads(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	file := addCustomProfile(t, m, "Qwen")

	toggleSubscriptionImagesRow(t, m)
	if !m.subscriptionModal.draft.imagesBlocked {
		t.Fatal("toggling left the draft unblocked")
	}
	m.saveSubscriptionDraft()
	if m.subscriptionModal.err != nil {
		t.Fatalf("save: %v", m.subscriptionModal.err)
	}

	if !claudeconfig.ReadImagesBlocked(m.claudeConfigsDir, file) {
		t.Error("saved profile does not deny image reads")
	}
}

// The toggle must survive the round trip, or reopening the modal shows a
// profile that is blocking images as if it were not.
func TestSubscriptionModal_loadsTheImagesToggleFromTheProfile(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	file := addCustomProfile(t, m, "Qwen")
	if err := claudeconfig.WriteImagesBlocked(m.claudeConfigsDir, file, true); err != nil {
		t.Fatalf("WriteImagesBlocked: %v", err)
	}

	m.loadSubscriptionDraft(m.subscriptionModalProfile())

	if !m.subscriptionModal.draft.imagesBlocked {
		t.Error("draft reports images allowed for a profile that denies them")
	}
}

func TestSubscriptionModal_togglingImagesBackAllowsThem(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	file := addCustomProfile(t, m, "Qwen")

	toggleSubscriptionImagesRow(t, m)
	m.saveSubscriptionDraft()
	toggleSubscriptionImagesRow(t, m)
	m.saveSubscriptionDraft()
	if m.subscriptionModal.err != nil {
		t.Fatalf("save: %v", m.subscriptionModal.err)
	}

	if claudeconfig.ReadImagesBlocked(m.claudeConfigsDir, file) {
		t.Error("profile still denies image reads after the toggle was turned off")
	}
}

// WriteModelMappings deletes a user-configured profile's model, so the save
// path must keep skipping it. Blocking images travels the same path.
func TestSubscriptionModal_blockingImagesKeepsTheModelAndEndpoint(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	file := addCustomProfile(t, m, "Qwen")

	editSubscriptionField(t, m, subscriptionDetailEndpoint, "http://127.0.0.1:8000")
	editSubscriptionField(t, m, subscriptionDetailModel, "Qwen-3.8")
	toggleSubscriptionImagesRow(t, m)
	m.saveSubscriptionDraft()
	if m.subscriptionModal.err != nil {
		t.Fatalf("save: %v", m.subscriptionModal.err)
	}

	if got := claudeconfig.ReadCustomModel(m.claudeConfigsDir, file); got != "Qwen-3.8" {
		t.Errorf("model = %q, want it kept", got)
	}
	if got := claudeconfig.ReadBaseURL(m.claudeConfigsDir, file); got != "http://127.0.0.1:8000" {
		t.Errorf("endpoint = %q, want it kept", got)
	}
	if !claudeconfig.ReadImagesBlocked(m.claudeConfigsDir, file) {
		t.Error("saved profile does not deny image reads")
	}
}

func TestSubscriptionModal_rendersTheImagesToggleState(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	addCustomProfile(t, m, "Qwen")

	view := m.View()
	if !strings.Contains(view, "Images") {
		t.Fatalf("modal does not render an Images row:\n%s", view)
	}
	if !strings.Contains(view, "Sent to the model") {
		t.Errorf("modal does not render the allowed state:\n%s", view)
	}

	toggleSubscriptionImagesRow(t, m)
	view = m.View()
	if !strings.Contains(view, "Never sent") {
		t.Errorf("modal does not render the blocked state:\n%s", view)
	}
}
