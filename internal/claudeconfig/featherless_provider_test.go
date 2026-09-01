package claudeconfig

import "testing"

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
