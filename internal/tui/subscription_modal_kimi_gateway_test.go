package tui

import (
	"path/filepath"
	"testing"

	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

// Saving a Kimi For Coding subscription key into a Moonshot open-platform
// profile used to leave the profile pointed at api.moonshot.ai, which rejects
// that credential outright — the pane then sat in a "401 Invalid Authentication
// · Retrying" loop with no indication the endpoint was wrong. Saving the key
// now moves the whole profile to the gateway that accepts it, and the open
// detail pane must show the repaired endpoint and models rather than the stale
// ones it was rendering when the key was typed.
func TestSubscriptionModal_savingCodingKeyRepointsProfileGateway(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	file, err := claudeconfig.AddForProvider(
		m.claudeConfigsList, m.claudeConfigsDir, "Moonshot Kimi", "moonshot")
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

	const key = "sk-kimi-0i7PaMyeHsU3JKQNp7b58xo1DclZSOseiff18MbcAf1JXS8RWwXdeAPwqWbCCHuQ"
	m.subscriptionModal.draft.apiKey = key
	m.subscriptionModal.draft.keyEdited = true
	m.subscriptionModal.draft.dirty = true

	m.saveSubscriptionDraft()

	if m.subscriptionModal.err != nil {
		t.Fatalf("save reported %v", m.subscriptionModal.err)
	}
	if got := claudeconfig.ReadBaseURL(m.claudeConfigsDir, file); got != "https://api.kimi.com/coding" {
		t.Errorf("base URL = %q, want the coding gateway", got)
	}
	if got := claudeconfig.ReadAPIKey(m.claudeConfigsDir, file); got != key {
		t.Errorf("stored key = %q, want it preserved", got)
	}
	if got := m.subscriptionModalProfile().Provider.Key; got != "moonshot-coding" {
		t.Errorf("profile provider = %q, want moonshot-coding", got)
	}
	if !m.subscriptionModalProfile().Ready {
		t.Error("repaired profile is not selectable")
	}

	// The draft drives the detail pane; leaving it on the open platform's
	// kimi-k* list would show model routing this profile no longer has, and the
	// next mapping edit would write those dead ids back over the repair.
	want := claudeconfig.ProviderModels["moonshot-coding"]
	if len(m.subscriptionModal.draft.models) != len(want) {
		t.Fatalf("draft models = %v, want %v", m.subscriptionModal.draft.models, want)
	}
	for i, id := range want {
		if m.subscriptionModal.draft.models[i] != id {
			t.Fatalf("draft models = %v, want %v", m.subscriptionModal.draft.models, want)
		}
	}
	if m.subscriptionModal.draft.dirty {
		t.Error("repaired save left the draft dirty")
	}
	if got := filepath.Base(m.subscriptionModal.draft.file); got != file {
		t.Errorf("draft file = %q, want %q", got, file)
	}
}
