package claudeconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readEnv(t *testing.T, dir, file string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	env, _ := settings["env"].(map[string]any)
	if env == nil {
		t.Fatalf("config %s has no env section", file)
	}
	return env
}

func customConfig(t *testing.T) (dir, list, file string) {
	t.Helper()
	dir = t.TempDir()
	list = filepath.Join(dir, "claude-configs.list")
	file, err := AddForProvider(list, dir, "Qwen", "custom")
	if err != nil {
		t.Fatalf("AddForProvider: %v", err)
	}
	return dir, list, file
}

// A user-configured provider supplies no endpoint or models of its own, so the
// modal must offer text fields instead of the mapping cycler, which is dead
// with an empty model list.
func TestCustomProvider_declares_itself_user_configured(t *testing.T) {
	provider, ok := ProviderByKey("custom")
	if !ok {
		t.Fatal("catalog has no custom provider")
	}
	if !provider.UserConfigured {
		t.Error("custom provider must set UserConfigured")
	}
	if provider.BaseURL != "" {
		t.Errorf("custom provider must ship no base URL, got %q", provider.BaseURL)
	}
	if len(provider.Models) != 0 {
		t.Errorf("custom provider must ship no models, got %d", len(provider.Models))
	}
	if provider.Auth != AuthAPIKey {
		t.Errorf("custom provider auth = %q, want %q", provider.Auth, AuthAPIKey)
	}
	if provider.MirrorOpenCode {
		t.Error("custom provider must not mirror into OpenCode: its models are unknown to that catalog")
	}
}

// Providers[0] is the fallback for every profile name matching no alias. A
// user-configured provider there would claim every stray config.
func TestUserConfiguredProviderIsNeverTheUnknownNameFallback(t *testing.T) {
	if Providers[0].UserConfigured {
		t.Fatalf("Providers[0] (%s) is the unknown-name fallback and must be a real gateway", Providers[0].Key)
	}
	if got := ProviderForName("some brand new gateway").Key; got == "custom" {
		t.Errorf("unknown name resolved to custom, want a real gateway (got %q)", got)
	}
}

// A placeholder empty model id reaches Claude Code as a real env var, so a
// provider with no defaults must write none at all.
func TestAddForProvider_custom_writes_no_placeholder_models_or_endpoint(t *testing.T) {
	dir, _, file := customConfig(t)
	env := readEnv(t, dir, file)

	if got, _ := env["WISP_DECK_SUBSCRIPTION_PROVIDER"].(string); got != "custom" {
		t.Errorf("provider marker = %q, want custom", got)
	}
	if _, ok := env["ANTHROPIC_BASE_URL"]; ok {
		t.Error("custom profile must not declare a base URL before the user sets one")
	}
	for _, key := range envKeys {
		if _, ok := env[key]; ok {
			t.Errorf("%s written with no model to name", key)
		}
	}
}

func TestWriteCustomEndpoint_sets_and_clears_the_base_url(t *testing.T) {
	dir, _, file := customConfig(t)

	if err := WriteCustomEndpoint(dir, file, "  https://abc-8000.proxy.runpod.net/  "); err != nil {
		t.Fatalf("WriteCustomEndpoint: %v", err)
	}
	if got := ReadBaseURL(dir, file); got != "https://abc-8000.proxy.runpod.net" {
		t.Errorf("endpoint = %q, want trimmed with no trailing slash", got)
	}

	if err := WriteCustomEndpoint(dir, file, ""); err != nil {
		t.Fatalf("clear endpoint: %v", err)
	}
	if _, ok := readEnv(t, dir, file)["ANTHROPIC_BASE_URL"]; ok {
		t.Error("clearing the endpoint must remove the key, not blank it")
	}
}

func TestWriteCustomEndpoint_rejects_a_non_http_endpoint(t *testing.T) {
	dir, _, file := customConfig(t)
	for _, endpoint := range []string{"abc-8000.proxy.runpod.net", "ftp://pod/v1", "https://pod one/v1"} {
		if err := WriteCustomEndpoint(dir, file, endpoint); err == nil {
			t.Errorf("endpoint %q accepted, want rejection", endpoint)
		}
	}
	if _, ok := readEnv(t, dir, file)["ANTHROPIC_BASE_URL"]; ok {
		t.Error("a rejected endpoint must leave the profile untouched")
	}
}

// One pod serves one model, and /model plus subagents move freely across all
// four aliases — so every alias has to name it or those tiers launch unmapped.
func TestWriteCustomModel_names_the_model_in_every_alias(t *testing.T) {
	dir, _, file := customConfig(t)

	if err := WriteCustomModel(dir, file, " qwen3-coder "); err != nil {
		t.Fatalf("WriteCustomModel: %v", err)
	}
	env := readEnv(t, dir, file)
	for _, key := range envKeys {
		if got, _ := env[key].(string); got != "qwen3-coder" {
			t.Errorf("%s = %q, want qwen3-coder", key, got)
		}
	}

	if err := WriteCustomModel(dir, file, ""); err != nil {
		t.Fatalf("clear model: %v", err)
	}
	env = readEnv(t, dir, file)
	for _, key := range envKeys {
		if _, ok := env[key]; ok {
			t.Errorf("clearing the model must remove %s", key)
		}
	}
}

func TestWriteCustomModel_rejects_whitespace_inside_an_id(t *testing.T) {
	dir, _, file := customConfig(t)
	if err := WriteCustomModel(dir, file, "qwen3 coder"); err == nil {
		t.Error("model id with an inner space accepted, want rejection")
	}
}

func TestWriteCustomContextWindow_requires_a_positive_integer(t *testing.T) {
	dir, _, file := customConfig(t)

	if err := WriteCustomContextWindow(dir, file, " 131072 "); err != nil {
		t.Fatalf("WriteCustomContextWindow: %v", err)
	}
	if got, _ := readEnv(t, dir, file)[ContextBudgetKey].(string); got != "131072" {
		t.Errorf("%s = %q, want 131072", ContextBudgetKey, got)
	}

	for _, bad := range []string{"0", "-1", "many", "131_072"} {
		if err := WriteCustomContextWindow(dir, file, bad); err == nil {
			t.Errorf("context window %q accepted, want rejection", bad)
		}
	}
	if got, _ := readEnv(t, dir, file)[ContextBudgetKey].(string); got != "131072" {
		t.Errorf("a rejected window overwrote the stored one: %q", got)
	}

	if err := WriteCustomContextWindow(dir, file, ""); err != nil {
		t.Fatalf("clear window: %v", err)
	}
	if _, ok := readEnv(t, dir, file)[ContextBudgetKey]; ok {
		t.Error("clearing the window must remove the key")
	}
}

// Overshooting the server's real window is unrecoverable, so a hand-entered
// window must survive every path that re-stamps the budget from the catalog.
func TestCustomContextWindowSurvivesTheCatalogBudgetPasses(t *testing.T) {
	dir, _, file := customConfig(t)
	if err := WriteCustomModel(dir, file, "qwen3-coder"); err != nil {
		t.Fatalf("WriteCustomModel: %v", err)
	}
	if err := WriteCustomContextWindow(dir, file, "131072"); err != nil {
		t.Fatalf("WriteCustomContextWindow: %v", err)
	}

	if err := WriteModelMappings(dir, file, [4]int{-1, -1, -1, -1}, nil); err != nil {
		t.Fatalf("WriteModelMappings: %v", err)
	}
	if got, _ := readEnv(t, dir, file)[ContextBudgetKey].(string); got != "131072" {
		t.Errorf("after WriteModelMappings %s = %q, want 131072", ContextBudgetKey, got)
	}

	if _, err := EnsureContextBudgetAll(dir); err != nil {
		t.Fatalf("EnsureContextBudgetAll: %v", err)
	}
	if got, _ := readEnv(t, dir, file)[ContextBudgetKey].(string); got != "131072" {
		t.Errorf("after the budget sweep %s = %q, want 131072", ContextBudgetKey, got)
	}
}

// The endpoint and model live in the same file as the credential.
func TestCustomWritesKeepTheProfilePrivate(t *testing.T) {
	dir, _, file := customConfig(t)
	if err := WriteCustomEndpoint(dir, file, "https://pod:8000"); err != nil {
		t.Fatalf("WriteCustomEndpoint: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, file))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("profile perms = %o, want 600", perm)
	}
}

func TestReadCustomModel_reports_the_configured_id(t *testing.T) {
	dir, _, file := customConfig(t)
	if got := ReadCustomModel(dir, file); got != "" {
		t.Errorf("unset model reads %q, want empty", got)
	}
	if err := WriteCustomModel(dir, file, "qwen3-coder"); err != nil {
		t.Fatalf("WriteCustomModel: %v", err)
	}
	if got := ReadCustomModel(dir, file); got != "qwen3-coder" {
		t.Errorf("ReadCustomModel = %q, want qwen3-coder", got)
	}
}

func TestReadContextWindow_reports_the_configured_window(t *testing.T) {
	dir, _, file := customConfig(t)
	if got := ReadContextWindow(dir, file); got != "" {
		t.Errorf("unset window reads %q, want empty", got)
	}
	if err := WriteCustomContextWindow(dir, file, "131072"); err != nil {
		t.Fatalf("WriteCustomContextWindow: %v", err)
	}
	if got := ReadContextWindow(dir, file); got != "131072" {
		t.Errorf("ReadContextWindow = %q, want 131072", got)
	}
}

// A public pod URL with no credential is the shape this must not silently allow.
func TestCustomProfileIsNotReadyWithoutAKey(t *testing.T) {
	dir, list, file := customConfig(t)
	configs := Load(list)
	if len(configs) != 1 {
		t.Fatalf("expected one config, got %d", len(configs))
	}
	if ConfigReady(dir, configs[0]) {
		t.Error("custom profile is ready with no API key stored")
	}
	if err := WriteAPIKey(dir, file, "pod-secret"); err != nil {
		t.Fatalf("WriteAPIKey: %v", err)
	}
	if !ConfigReady(dir, configs[0]) {
		t.Error("custom profile with a key is still not ready")
	}
}

func TestCustomProviderIsOfferedInTheProviderPicker(t *testing.T) {
	for _, p := range Providers {
		if p.Key == "custom" {
			if !strings.Contains(strings.ToLower(p.Name), "custom") {
				t.Errorf("custom provider name %q does not read as custom", p.Name)
			}
			return
		}
	}
	t.Fatal("custom provider missing from Providers")
}
