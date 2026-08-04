package claudeconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Moonshot sells two different Kimi products behind two different gateways, and
// a credential for one is rejected outright by the other. The open platform
// (api.moonshot.ai/anthropic, kimi-k* ids) meters per token; the Kimi For
// Coding subscription (api.kimi.com/coding, k3 / kimi-for-coding ids) is
// flat-rate and issues sk-kimi-… keys. Shipping only the open platform meant a
// subscription key was posted to a service that had never heard of it, so every
// request came back "401 Invalid Authentication" and Claude Code retried it ten
// times. Both gateways were verified live against a real subscription key.
func TestKimiCodingProvider_catalogEntry(t *testing.T) {
	provider, ok := ProviderByKey("moonshot-coding")
	if !ok {
		t.Fatal("catalog is missing the moonshot-coding provider")
	}
	if provider.Name != "Kimi For Coding" {
		t.Errorf("name = %q, want %q", provider.Name, "Kimi For Coding")
	}
	if provider.BaseURL != "https://api.kimi.com/coding" {
		t.Errorf("base URL = %q, want the coding-subscription gateway", provider.BaseURL)
	}
	if provider.Auth != AuthAPIKey {
		t.Errorf("auth = %q, want %q", provider.Auth, AuthAPIKey)
	}
	if !provider.MirrorOpenCode {
		t.Error("the coding gateway is Anthropic-compatible and must mirror into OpenCode")
	}

	// Ids and context windows are the gateway's own /v1/models response. Prices
	// are zero because the plan is a flat-rate subscription, not metered per
	// token — pricing.go's catalog fold skips zero-priced models rather than
	// publishing a false $0 rate.
	type modelSpec struct{ context, output int }
	wantModels := map[string]modelSpec{
		"k3":                        {262144, 131072},
		"k3-256k":                   {262144, 131072},
		"kimi-for-coding":           {262144, 32768},
		"kimi-for-coding-highspeed": {262144, 32768},
	}
	got := map[string]bool{}
	for _, model := range provider.Models {
		got[model.ID] = true
		want, known := wantModels[model.ID]
		if !known {
			t.Errorf("unexpected coding model %q — only models the gateway serves may ship", model.ID)
			continue
		}
		if model.InPerM != 0 || model.OutPerM != 0 {
			t.Errorf("%s cost = %v/%v, want 0/0 (subscription, not metered)",
				model.ID, model.InPerM, model.OutPerM)
		}
		if model.Context != want.context || model.Output != want.output {
			t.Errorf("%s limits = %d/%d, want %d/%d",
				model.ID, model.Context, model.Output, want.context, want.output)
		}
	}
	for id := range wantModels {
		if !got[id] {
			t.Errorf("coding catalog is missing model %q", id)
		}
	}

	wantDefaults := [4]string{"k3", "kimi-for-coding", "kimi-for-coding", "k3"}
	if provider.DefaultModels != wantDefaults {
		t.Errorf("DefaultModels = %v, want %v", provider.DefaultModels, wantDefaults)
	}
}

// A profile with no explicit marker resolves by name, and "kimi" alone belongs
// to the open platform — so the coding entry has to be consulted first or every
// coding-named profile would be pointed at the gateway that 401s it.
func TestProviderForName_separatesKimiProducts(t *testing.T) {
	cases := map[string]string{
		"Kimi For Coding":     "moonshot-coding",
		"my kimi coding plan": "moonshot-coding",
		"Moonshot Kimi":       "moonshot",
		"Work kimi":           "moonshot",
		"my Moonshot plan":    "moonshot",
	}
	for name, want := range cases {
		if got := ProviderForName(name).Key; got != want {
			t.Errorf("ProviderForName(%q) = %q, want %q", name, got, want)
		}
	}
}

// The subscription key the user actually holds must never sit in a profile
// pointed at the open platform: that combination cannot authenticate, and the
// only symptom is a 401 retry loop inside the agent pane with no hint that the
// endpoint is the problem. Saving the key repairs the profile in place.
func TestRepairGatewayForKey_movesCodingKeyToCodingGateway(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-configs.list")
	configsDir := filepath.Join(dir, "claude-configs")

	file, err := AddForProvider(list, configsDir, "Moonshot Kimi", "moonshot")
	if err != nil {
		t.Fatal(err)
	}
	const key = "sk-kimi-a-fake-coding-subscription-key"
	if err := WriteAPIKey(configsDir, file, key); err != nil {
		t.Fatal(err)
	}

	changed, err := RepairGatewayForKey(configsDir, file)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("a coding key on the open-platform gateway was left unrepaired")
	}

	if got := ReadProviderMarker(configsDir, file); got != "moonshot-coding" {
		t.Errorf("marker = %q, want moonshot-coding", got)
	}
	if got := ReadBaseURL(configsDir, file); got != "https://api.kimi.com/coding" {
		t.Errorf("base URL = %q, want the coding gateway", got)
	}
	if got := ReadAPIKey(configsDir, file); got != key {
		t.Errorf("key = %q, want it preserved", got)
	}

	// Stale kimi-k* aliases would be rejected by the coding gateway just as
	// surely as the wrong endpoint was, so the model routing moves too.
	provider, _ := ProviderByKey("moonshot-coding")
	data, err := os.ReadFile(filepath.Join(configsDir, file))
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	aliasEnv := []string{
		"ANTHROPIC_DEFAULT_OPUS_MODEL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"ANTHROPIC_DEFAULT_FABLE_MODEL",
	}
	for i, envKey := range aliasEnv {
		if got := settings.Env[envKey]; got != provider.DefaultModels[i] {
			t.Errorf("%s = %q, want %q", envKey, got, provider.DefaultModels[i])
		}
	}

	// Repair is idempotent: a second pass must not report a change or churn the
	// file, or every save would look dirty forever.
	changed, err = RepairGatewayForKey(configsDir, file)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("repair is not idempotent")
	}
}

// Only the sk-kimi- prefix is evidence of a coding key. Everything else stays
// exactly where the user put it — silently moving an open-platform key would
// break a working profile to fix one that was never broken.
func TestRepairGatewayForKey_leavesEveryOtherKeyAlone(t *testing.T) {
	cases := []struct {
		provider string
		key      string
	}{
		{"moonshot", "sk-an-open-platform-key-without-the-coding-prefix"},
		{"moonshot", ""},
		{"zhipu", "sk-kimi-looks-like-coding-but-is-a-glm-profile"},
		{"mimo", "sk-kimi-looks-like-coding-but-is-a-mimo-profile"},
	}
	for _, tc := range cases {
		t.Run(tc.provider+"/"+tc.key, func(t *testing.T) {
			dir := t.TempDir()
			list := filepath.Join(dir, "claude-configs.list")
			configsDir := filepath.Join(dir, "claude-configs")
			file, err := AddForProvider(list, configsDir, "Profile", tc.provider)
			if err != nil {
				t.Fatal(err)
			}
			if tc.key != "" {
				if err := WriteAPIKey(configsDir, file, tc.key); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(filepath.Join(configsDir, file))
			if err != nil {
				t.Fatal(err)
			}

			changed, err := RepairGatewayForKey(configsDir, file)
			if err != nil {
				t.Fatal(err)
			}
			if changed {
				t.Error("unrelated profile was rewritten")
			}
			after, _ := os.ReadFile(filepath.Join(configsDir, file))
			if string(before) != string(after) {
				t.Errorf("file changed:\n%s\n%s", before, after)
			}
		})
	}
}

// A missing or unreadable profile is reported, never silently treated as
// repaired — the caller writes the key first and must not be told the profile
// is consistent when it could not be read at all.
func TestRepairGatewayForKey_missingFileErrors(t *testing.T) {
	if _, err := RepairGatewayForKey(t.TempDir(), "absent.json"); err == nil {
		t.Fatal("missing profile accepted")
	}
}
