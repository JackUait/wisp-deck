package claudeconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func readEnvMap(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var s struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return s.Env
}

func TestContextBudget_is_the_smallest_window_any_mapped_model_can_hold(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want int
		ok   bool
	}{
		{
			name: "kimi for coding maps every alias to a 262144 model",
			env: map[string]string{
				"ANTHROPIC_DEFAULT_OPUS_MODEL":   "k3",
				"ANTHROPIC_DEFAULT_SONNET_MODEL": "kimi-for-coding",
				"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "kimi-for-coding",
				"ANTHROPIC_DEFAULT_FABLE_MODEL":  "k3",
			},
			want: 262144,
			ok:   true,
		},
		{
			name: "a 131072 haiku model caps the whole session",
			env: map[string]string{
				"ANTHROPIC_DEFAULT_OPUS_MODEL":   "glm-4.7",
				"ANTHROPIC_DEFAULT_SONNET_MODEL": "glm-4.7",
				"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "glm-4.5-air",
				"ANTHROPIC_DEFAULT_FABLE_MODEL":  "glm-4.5-air",
			},
			want: 131072,
			ok:   true,
		},
		{
			name: "a model outside the catalog cannot lower the budget it does not describe",
			env: map[string]string{
				"ANTHROPIC_DEFAULT_OPUS_MODEL":   "k3",
				"ANTHROPIC_DEFAULT_SONNET_MODEL": "something-unlisted",
			},
			want: 262144,
			ok:   true,
		},
		{
			name: "no mapped model in the catalog yields no budget",
			env:  map[string]string{"ANTHROPIC_DEFAULT_OPUS_MODEL": "something-unlisted"},
			want: 0,
			ok:   false,
		},
		{
			name: "no mappings at all yields no budget",
			env:  map[string]string{},
			want: 0,
			ok:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ContextBudget(tt.env)
			if ok != tt.ok || got != tt.want {
				t.Errorf("ContextBudget() = (%d, %v), want (%d, %v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

// Claude Code sizes its auto-compact budget from its own model catalog, which
// knows nothing about the server ANTHROPIC_BASE_URL points at. Every provider
// must therefore ship its real window, or a session grows past a cap only the
// provider enforces and every later request 400s — including /compact, which
// carries the same oversized transcript and so cannot dig the session out.
func TestAddForProvider_stamps_the_providers_real_context_window(t *testing.T) {
	for _, provider := range Providers {
		t.Run(provider.Key, func(t *testing.T) {
			dir := t.TempDir()
			listFile := filepath.Join(dir, "list")
			file, err := AddForProvider(listFile, dir, "probe", provider.Key)
			if err != nil {
				t.Fatalf("AddForProvider: %v", err)
			}
			env := readEnvMap(t, filepath.Join(dir, file))
			want, ok := ContextBudget(env)
			if provider.SuppliesOwnModel() {
				// Nobody but the user knows this endpoint's window, and capping
				// a session at an invented limit is its own bug — so the profile
				// declares none until they enter one.
				if ok {
					t.Fatalf("own-model provider %q sized itself from the catalog", provider.Key)
				}
				for _, key := range []string{
					"CLAUDE_CODE_MAX_CONTEXT_TOKENS",
					"CLAUDE_CODE_AUTO_COMPACT_WINDOW",
					"CLAUDE_CODE_DISABLE_1M_CONTEXT",
				} {
					if got, declared := env[key]; declared {
						t.Errorf("own-model provider %q shipped %s=%q", provider.Key, key, got)
					}
				}
				return
			}
			if !ok {
				t.Fatalf("provider %q maps no catalog model, so no window can be declared", provider.Key)
			}
			window := strconv.Itoa(want)
			if got := env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"]; got != window {
				t.Errorf("CLAUDE_CODE_MAX_CONTEXT_TOKENS = %q, want %q", got, window)
			}
			autoCompact, capped := env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]
			wantCap := want < 1000000
			if capped != wantCap {
				t.Errorf("auto-compaction capped = %v, want %v", capped, wantCap)
			} else if capped && autoCompact != window {
				t.Errorf("auto-compact window = %q, want %q", autoCompact, window)
			}
			_, disabled := env["CLAUDE_CODE_DISABLE_1M_CONTEXT"]
			if disabled != wantCap {
				t.Errorf("1M marker disabled = %v, want %v", disabled, wantCap)
			}
			// The window has to hold the reply as well as the conversation, and
			// Claude Code sizes max_tokens from a catalog that cannot see this
			// endpoint.
			reserve, reserved := env[OutputReserveKey]
			if reserved != wantCap {
				t.Errorf("output reserved = %v, want %v", reserved, wantCap)
			} else if reserved && reserve != strconv.Itoa(outputReserve(want)) {
				t.Errorf("output reserve = %q, want %q", reserve, strconv.Itoa(outputReserve(want)))
			}
			for _, id := range provider.DefaultModels {
				if window, _, known := ModelLimit(id); known && want > window {
					t.Errorf("declared budget %d exceeds %s's real window %d", want, id, window)
				}
			}
		})
	}
}

func TestWriteCustomContextWindow_caps_an_inherited_1m_session(t *testing.T) {
	tests := []struct {
		name    string
		window  string
		wantCap bool
	}{
		{name: "smaller window", window: "262144", wantCap: true},
		{name: "one million window", window: "1000000", wantCap: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, _, file := customConfig(t)
			if err := WriteCustomContextWindow(dir, file, tt.window); err != nil {
				t.Fatalf("WriteCustomContextWindow: %v", err)
			}
			env := readEnvMap(t, filepath.Join(dir, file))
			if got := env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"]; got != tt.window {
				t.Errorf("model window = %q, want %q", got, tt.window)
			}
			autoCompact, capped := env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]
			if capped != tt.wantCap {
				t.Errorf("auto-compaction capped = %v, want %v", capped, tt.wantCap)
			} else if capped && autoCompact != tt.window {
				t.Errorf("auto-compact window = %q, want %q", autoCompact, tt.window)
			}
			_, disabled := env["CLAUDE_CODE_DISABLE_1M_CONTEXT"]
			if disabled != tt.wantCap {
				t.Errorf("1M marker disabled = %v, want %v", disabled, tt.wantCap)
			}
		})
	}
}

func TestWriteModelMappings_restamps_the_budget_when_a_smaller_model_is_mapped(t *testing.T) {
	dir := t.TempDir()
	listFile := filepath.Join(dir, "list")
	file, err := AddForProvider(listFile, dir, "zhipu probe", "zhipu")
	if err != nil {
		t.Fatalf("AddForProvider: %v", err)
	}
	models := ProviderModels["zhipu"]
	air := -1
	for i, m := range models {
		if m == "glm-4.5-air" {
			air = i
		}
	}
	if air < 0 {
		t.Fatal("glm-4.5-air missing from the zhipu model list")
	}
	if err := WriteModelMappings(dir, file, [4]int{air, air, air, air}, models); err != nil {
		t.Fatalf("WriteModelMappings: %v", err)
	}
	env := readEnvMap(t, filepath.Join(dir, file))
	if got := env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"]; got != "131072" {
		t.Errorf("after remapping to glm-4.5-air, budget = %q, want %q", got, "131072")
	}
}

// Profiles created before the window was declared are already on disk; leaving
// them alone would keep exactly the sessions that hit this bug unprotected.
func TestEnsureContextBudget_backfills_a_config_written_before_the_window_was_declared(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.json")
	legacy := `{
  "env": {
    "WISP_DECK_SUBSCRIPTION_PROVIDER": "moonshot-coding",
    "ANTHROPIC_BASE_URL": "https://api.kimi.com/coding",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "k3",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "kimi-for-coding",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "kimi-for-coding",
    "ANTHROPIC_DEFAULT_FABLE_MODEL": "k3"
  }
}`
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureContextBudget(dir, "legacy.json")
	if err != nil {
		t.Fatalf("EnsureContextBudget: %v", err)
	}
	if !changed {
		t.Error("a config with no declared window should have been rewritten")
	}
	if got := readEnvMap(t, path)["CLAUDE_CODE_MAX_CONTEXT_TOKENS"]; got != "262144" {
		t.Errorf("backfilled budget = %q, want %q", got, "262144")
	}

	changed, err = EnsureContextBudget(dir, "legacy.json")
	if err != nil {
		t.Fatalf("EnsureContextBudget (second pass): %v", err)
	}
	if changed {
		t.Error("a config already declaring the right window must not be rewritten")
	}
}

func TestEnsureContextBudget_keeps_a_custom_profiles_declared_window(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.json")
	custom := `{
  "env": {
    "WISP_DECK_SUBSCRIPTION_PROVIDER": "custom",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "glm-5.2",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "glm-5.2",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "glm-5.2",
    "ANTHROPIC_DEFAULT_FABLE_MODEL": "glm-5.2",
    "CLAUDE_CODE_MAX_CONTEXT_TOKENS": "262144"
  }
}`
	if err := os.WriteFile(path, []byte(custom), 0600); err != nil {
		t.Fatal(err)
	}

	changed, err := EnsureContextBudget(dir, "custom.json")
	if err != nil {
		t.Fatalf("EnsureContextBudget: %v", err)
	}
	if !changed {
		t.Fatal("custom profile was not given its compaction safeguards")
	}
	env := readEnvMap(t, path)
	if got := env[ContextBudgetKey]; got != "262144" {
		t.Errorf("declared custom window = %q, want 262144", got)
	}
	if got := env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; got != "262144" {
		t.Errorf("auto-compact window = %q, want 262144", got)
	}
	if got := env["CLAUDE_CODE_DISABLE_1M_CONTEXT"]; got != "1" {
		t.Errorf("disable 1M marker = %q, want 1", got)
	}
}

func TestEnsureContextBudget_keeps_a_markerless_self_hosted_profiles_window(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qwen.json")
	custom := `{
  "env": {
    "ANTHROPIC_BASE_URL": "https://self-hosted.example/v1",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "glm-5.2",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "glm-5.2",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "glm-5.2",
    "ANTHROPIC_DEFAULT_FABLE_MODEL": "glm-5.2",
    "CLAUDE_CODE_MAX_CONTEXT_TOKENS": "262144"
  }
}`
	if err := os.WriteFile(path, []byte(custom), 0600); err != nil {
		t.Fatal(err)
	}

	changed, err := EnsureContextBudget(dir, "qwen.json")
	if err != nil {
		t.Fatalf("EnsureContextBudget: %v", err)
	}
	if !changed {
		t.Fatal("legacy custom profile was not given its compaction safeguards")
	}
	env := readEnvMap(t, path)
	if got := env[ContextBudgetKey]; got != "262144" {
		t.Errorf("declared custom window = %q, want 262144", got)
	}
	if got := env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; got != "262144" {
		t.Errorf("auto-compact window = %q, want 262144", got)
	}
}

func TestEnsureContextBudget_normalizes_a_padded_custom_window(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.json")
	custom := `{
  "env": {
    "WISP_DECK_SUBSCRIPTION_PROVIDER": "custom",
    "CLAUDE_CODE_MAX_CONTEXT_TOKENS": " 262144 "
  }
}`
	if err := os.WriteFile(path, []byte(custom), 0600); err != nil {
		t.Fatal(err)
	}

	changed, err := EnsureContextBudget(dir, "custom.json")
	if err != nil {
		t.Fatalf("EnsureContextBudget: %v", err)
	}
	if !changed {
		t.Fatal("padded custom window was not normalized and safeguarded")
	}
	env := readEnvMap(t, path)
	for _, key := range []string{
		ContextBudgetKey,
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW",
	} {
		if got := env[key]; got != "262144" {
			t.Errorf("%s = %q, want 262144", key, got)
		}
	}
	if got := env["CLAUDE_CODE_DISABLE_1M_CONTEXT"]; got != "1" {
		t.Errorf("disable 1M marker = %q, want 1", got)
	}
}

func TestEnsureContextBudget_does_not_infer_a_custom_profiles_window(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.json")
	custom := `{
  "env": {
    "WISP_DECK_SUBSCRIPTION_PROVIDER": "custom",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "glm-5.2",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "glm-5.2",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "glm-5.2",
    "ANTHROPIC_DEFAULT_FABLE_MODEL": "glm-5.2"
  }
}`
	if err := os.WriteFile(path, []byte(custom), 0600); err != nil {
		t.Fatal(err)
	}

	changed, err := EnsureContextBudget(dir, "custom.json")
	if err != nil {
		t.Fatalf("EnsureContextBudget: %v", err)
	}
	if changed {
		t.Error("custom profile without a declared window was rewritten")
	}
	if _, declared := readEnvMap(t, path)[ContextBudgetKey]; declared {
		t.Error("catalog window was inferred for a user-configured endpoint")
	}
}

func TestEnsureContextBudget_backfills_compaction_for_an_existing_budget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.json")
	existing := `{
  "env": {
    "WISP_DECK_SUBSCRIPTION_PROVIDER": "custom",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "Qwen-3.8-Uncensored",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "Qwen-3.8-Uncensored",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "Qwen-3.8-Uncensored",
    "ANTHROPIC_DEFAULT_FABLE_MODEL": "Qwen-3.8-Uncensored",
    "CLAUDE_CODE_MAX_CONTEXT_TOKENS": "262144"
  }
}`
	if err := os.WriteFile(path, []byte(existing), 0600); err != nil {
		t.Fatal(err)
	}

	changed, err := EnsureContextBudget(dir, "existing.json")
	if err != nil {
		t.Fatalf("EnsureContextBudget: %v", err)
	}
	if !changed {
		t.Fatal("an existing budget without a compaction cap was not rewritten")
	}
	env := readEnvMap(t, path)
	if got := env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; got != "262144" {
		t.Errorf("auto-compact window = %q, want 262144", got)
	}
	if got := env["CLAUDE_CODE_DISABLE_1M_CONTEXT"]; got != "1" {
		t.Errorf("disable 1M marker = %q, want 1", got)
	}

	changed, err = EnsureContextBudget(dir, "existing.json")
	if err != nil {
		t.Fatalf("EnsureContextBudget (second pass): %v", err)
	}
	if changed {
		t.Error("a fully capped config was rewritten again")
	}
}

func TestEnsureContextBudget_leaves_a_config_it_cannot_size_alone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "native.json")
	if err := os.WriteFile(path, []byte(`{"env":{"FOO":"bar"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureContextBudget(dir, "native.json")
	if err != nil {
		t.Fatalf("EnsureContextBudget: %v", err)
	}
	if changed {
		t.Error("a config mapping no catalog model must be left verbatim")
	}
	if _, present := readEnvMap(t, path)["CLAUDE_CODE_MAX_CONTEXT_TOKENS"]; present {
		t.Error("no window may be invented for a config the catalog cannot size")
	}
}

// The installer copies a default profile only when the file is absent, so a
// default that ships without its window keeps handing new users the launch
// this bug needs.
func TestShippedDefaultConfigs_declare_their_providers_window(t *testing.T) {
	dir := filepath.Join("..", "..", "defaults", "claude-configs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read defaults: %v", err)
	}
	seen := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			env := readEnvMap(t, filepath.Join(dir, entry.Name()))
			want, ok := ContextBudget(env)
			if !ok {
				t.Skip("maps no catalog model")
			}
			seen++
			window := strconv.Itoa(want)
			if got := env[ContextBudgetKey]; got != window {
				t.Errorf("%s = %q, want %q", ContextBudgetKey, got, window)
			}
			autoCompact, capped := env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]
			wantCap := want < 1000000
			if capped != wantCap {
				t.Errorf("auto-compaction capped = %v, want %v", capped, wantCap)
			} else if capped && autoCompact != window {
				t.Errorf("auto-compact window = %q, want %q", autoCompact, window)
			}
			_, disabled := env["CLAUDE_CODE_DISABLE_1M_CONTEXT"]
			if disabled != wantCap {
				t.Errorf("1M marker disabled = %v, want %v", disabled, wantCap)
			}
			// Same window, same reply: a shipped default has to reserve it too.
			reserve, reserved := env[OutputReserveKey]
			if reserved != wantCap {
				t.Errorf("output reserved = %v, want %v", reserved, wantCap)
			} else if reserved && reserve != strconv.Itoa(outputReserve(want)) {
				t.Errorf("output reserve = %q, want %q", reserve, strconv.Itoa(outputReserve(want)))
			}
		})
	}
	if seen == 0 {
		t.Error("no shipped default mapped a catalog model — the guard checked nothing")
	}
}

func TestEnsureContextBudgetAll_sweeps_every_profile_and_skips_what_it_cannot_parse(t *testing.T) {
	dir := t.TempDir()
	if _, err := AddForProvider(filepath.Join(dir, "list"), dir, "kimi", "moonshot-coding"); err != nil {
		t.Fatal(err)
	}
	legacy := `{"env":{"ANTHROPIC_DEFAULT_OPUS_MODEL":"glm-4.5-air"}}`
	if err := os.WriteFile(filepath.Join(dir, "legacy.json"), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureContextBudgetAll(dir)
	if err != nil {
		t.Fatalf("EnsureContextBudgetAll: %v", err)
	}
	if changed != 1 {
		t.Errorf("changed = %d, want 1 (only the legacy profile lacked a window)", changed)
	}
	if got := readEnvMap(t, filepath.Join(dir, "legacy.json"))[ContextBudgetKey]; got != "131072" {
		t.Errorf("legacy budget = %q, want %q", got, "131072")
	}
}
