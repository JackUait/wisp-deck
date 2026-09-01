package claudeconfig

import (
	"path/filepath"
	"testing"
)

// Featherless ships an endpoint and an auth kind but no model list: its catalog
// is ~15,571 models fetched at runtime, so the modal must offer a picker rather
// than the alias cycler, which is inert on an empty model list.
func TestFeatherlessProvider_declares_a_remote_catalog(t *testing.T) {
	provider, ok := ProviderByKey("featherless")
	if !ok {
		t.Fatal("catalog has no featherless provider")
	}
	if !provider.RemoteCatalog {
		t.Error("featherless must set RemoteCatalog")
	}
	if provider.UserConfigured {
		t.Error("featherless ships its own endpoint, so it is not UserConfigured")
	}
	if provider.BaseURL != "https://api.featherless.ai" {
		t.Errorf("base URL = %q, want https://api.featherless.ai", provider.BaseURL)
	}
	if len(provider.Models) != 0 {
		t.Errorf("featherless must ship no static models, got %d", len(provider.Models))
	}
	if provider.Auth != AuthAPIKey {
		t.Errorf("auth = %q, want %q", provider.Auth, AuthAPIKey)
	}
	if provider.MirrorOpenCode {
		t.Error("OpenCode's catalog cannot size a Featherless-only model")
	}
	if !provider.SuppliesOwnModel() {
		t.Error("a remote-catalog provider supplies its own model")
	}
}

// Profiles are auto-named after the picked model, so "Featherless Kimi-K3" is a
// name this feature produces routinely. It contains "kimi", which is moonshot's
// alias, and alias matching is substring in slice order — so a featherless entry
// placed after moonshot resolves those profiles to the wrong gateway.
func TestFeatherlessAliasBeatsAModelNameFromAnotherProvider(t *testing.T) {
	for _, name := range []string{
		"Featherless Kimi-K3",
		"Featherless GLM-5.2",
		"featherless moonshotai/Kimi-K2.7-Code",
	} {
		if got := ProviderForName(name).Key; got != "featherless" {
			t.Errorf("ProviderForName(%q) = %q, want featherless", name, got)
		}
	}
}

// Providers[0] is the fallback for every name matching no alias. A provider with
// no models there would claim every stray config on the machine. The custom
// provider must stay last for the same reason.
func TestFeatherlessIsNeitherTheFallbackNorLast(t *testing.T) {
	if Providers[0].Key == "featherless" {
		t.Fatal("featherless must not be the unknown-name fallback")
	}
	if Providers[len(Providers)-1].Key != "custom" {
		t.Fatalf("custom must stay last, got %q", Providers[len(Providers)-1].Key)
	}
}

// The self-hosted provider must keep answering the same question the same way:
// the trait is an addition, not a redefinition.
func TestCustomProviderStillSuppliesItsOwnModel(t *testing.T) {
	provider, ok := ProviderByKey("custom")
	if !ok {
		t.Fatal("catalog has no custom provider")
	}
	if !provider.SuppliesOwnModel() {
		t.Error("custom must still report SuppliesOwnModel")
	}
	if zhipu, _ := ProviderByKey("zhipu"); zhipu.SuppliesOwnModel() {
		t.Error("a static-catalog gateway must not report SuppliesOwnModel")
	}
}

func featherlessConfig(t *testing.T) (dir, list, file string) {
	t.Helper()
	dir = t.TempDir()
	list = filepath.Join(dir, "claude-configs.list")
	file, err := AddForProvider(list, dir, "Featherless Kimi-K3", "featherless")
	if err != nil {
		t.Fatalf("AddForProvider: %v", err)
	}
	return dir, list, file
}

// A profile with no picked model would launch a pane with no model at all, so it
// must not be selectable until the pick and the key are both stored.
func TestFeatherlessConfigReady_requires_a_picked_model_and_a_key(t *testing.T) {
	dir, _, file := featherlessConfig(t)
	config := Config{Name: "Featherless Kimi-K3", File: file}

	if ConfigReady(dir, config) {
		t.Fatal("a fresh featherless profile has no model or key and must not be ready")
	}
	if err := WriteAPIKey(dir, file, "rc_test"); err != nil {
		t.Fatal(err)
	}
	if ConfigReady(dir, config) {
		t.Error("a key alone is not enough: the model is still unpicked")
	}
	if err := WriteCustomModel(dir, file, "moonshotai/Kimi-K3"); err != nil {
		t.Fatal(err)
	}
	if ConfigReady(dir, config) {
		t.Error("a model with no declared window strands the session on the flat 200000 default")
	}
	if err := WriteCustomContextWindow(dir, file, "262144"); err != nil {
		t.Fatal(err)
	}
	if !ConfigReady(dir, config) {
		t.Error("key + model + window must be ready")
	}
}

// Featherless keeps the socket warm with ": keep-alive" SSE comments through a
// cold model load (measured: comments every ~1.2s across a 12s load, worst byte
// silence 4.8s against the watchdog's 20s trigger). Disarming the watchdog here
// would trade a real dead-connection signal for nothing.
func TestFeatherlessKeepsTheByteWatchdogArmed(t *testing.T) {
	dir, _, file := featherlessConfig(t)
	if _, ok := readEnv(t, dir, file)[ByteWatchdogKey]; ok {
		t.Fatal("featherless must not disarm the byte watchdog at creation")
	}
	changed, err := EnsureByteWatchdog(dir, file)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("the watchdog sweep must leave a featherless profile untouched")
	}
	if _, ok := readEnv(t, dir, file)[ByteWatchdogKey]; ok {
		t.Error("featherless must not disarm the byte watchdog on sweep")
	}
}

// The static catalog cannot size a Featherless-only model, so the window written
// at pick time is the only figure available and every sweep must preserve it.
func TestFeatherlessDeclaredWindowSurvivesTheBudgetSweep(t *testing.T) {
	dir, _, file := featherlessConfig(t)
	if err := WriteCustomModel(dir, file, "moonshotai/Kimi-K3"); err != nil {
		t.Fatal(err)
	}
	if err := WriteCustomContextWindow(dir, file, "262144"); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureContextBudget(dir, file); err != nil {
		t.Fatal(err)
	}
	if got, _ := readEnv(t, dir, file)[ContextBudgetKey].(string); got != "262144" {
		t.Errorf("declared window = %q, want 262144 preserved", got)
	}
}
