package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

// addFeatherlessProfile appends a Featherless profile and focuses it.
func addFeatherlessProfile(t *testing.T, m *MainMenuModel, name string) string {
	t.Helper()
	file, err := claudeconfig.AddForProvider(m.claudeConfigsList, m.claudeConfigsDir, name, "featherless")
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

func readConfigEnv(t *testing.T, dir, file string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	env, _ := settings["env"].(map[string]any)
	if env == nil {
		t.Fatalf("config %s has no env section", file)
	}
	return env
}

// The endpoint ships with the provider, so offering a text field for it invites
// someone to break a working profile. The other three rows are the pick's.
func TestFeatherlessProfile_offers_model_context_and_images_but_no_endpoint(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	addFeatherlessProfile(t, m, "Featherless Kimi-K3")

	rows := m.subscriptionDetailRows()
	want := map[int]bool{
		subscriptionDetailModel:   false,
		subscriptionDetailContext: false,
		subscriptionDetailImages:  false,
	}
	for _, row := range rows {
		if row == subscriptionDetailEndpoint {
			t.Error("featherless must not offer an endpoint field: the provider supplies it")
		}
		if _, ok := want[row]; ok {
			want[row] = true
		}
	}
	for row, seen := range want {
		if !seen {
			t.Errorf("detail row %d missing for a featherless profile", row)
		}
	}
}

// WriteModelMappings writes the four aliases from the draft's model list, which
// is empty for a remote-catalog provider — so running it deletes the picked
// model on every save. This is the same defect the custom provider was fixed for.
func TestFeatherlessSave_keeps_the_picked_model_on_all_four_aliases(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	file := addFeatherlessProfile(t, m, "Featherless Kimi-K3")

	draft := &m.subscriptionModal.draft
	draft.model = "moonshotai/Kimi-K3"
	draft.window = "262144"
	draft.customEdited = true
	draft.dirty = true
	m.saveSubscriptionDraft()
	if m.subscriptionModal.err != nil {
		t.Fatalf("save: %v", m.subscriptionModal.err)
	}

	env := readConfigEnv(t, m.claudeConfigsDir, file)
	for _, key := range []string{
		"ANTHROPIC_DEFAULT_OPUS_MODEL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"ANTHROPIC_DEFAULT_FABLE_MODEL",
	} {
		if got, _ := env[key].(string); got != "moonshotai/Kimi-K3" {
			t.Errorf("%s = %q, want the picked model", key, got)
		}
	}
	if got, _ := env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"].(string); got != "262144" {
		t.Errorf("declared window = %q, want 262144", got)
	}
	if got, _ := env["ANTHROPIC_BASE_URL"].(string); got != "https://api.featherless.ai" {
		t.Errorf("base URL = %q, want the provider's own", got)
	}
}

// Most Featherless models are text-only, and an image sent to one fails the
// turn, so the toggle must be reachable on this provider too.
func TestFeatherlessProfile_can_block_image_reads(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	file := addFeatherlessProfile(t, m, "Featherless GLM")

	m.subscriptionModal.draft.model = "zai-org/GLM-5.2"
	m.subscriptionModal.draft.window = "262144"
	m.subscriptionModal.detailCursor = subscriptionDetailImages
	m.toggleSubscriptionImages()
	if !m.subscriptionModal.draft.imagesBlocked {
		t.Fatal("toggling images on a featherless profile did nothing")
	}
	m.saveSubscriptionDraft()
	if m.subscriptionModal.err != nil {
		t.Fatalf("save: %v", m.subscriptionModal.err)
	}
	if !claudeconfig.ReadImagesBlocked(m.claudeConfigsDir, file) {
		t.Error("the deny rules were not written")
	}
}

// The unready message names what to do next, and for featherless that is never
// an endpoint.
func TestFeatherlessUnreadyMessage_does_not_ask_for_an_endpoint(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	addFeatherlessProfile(t, m, "Featherless Kimi-K3")
	m.useSubscriptionProfile()
	if m.subscriptionModal.err == nil {
		t.Fatal("an unconfigured featherless profile must refuse to be used")
	}
	if got := m.subscriptionModal.err.Error(); strings.Contains(got, "endpoint") {
		t.Errorf("message asks for an endpoint the provider supplies: %q", got)
	}
}
