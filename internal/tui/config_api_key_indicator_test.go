package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// The indicator must count mappings against the NAMED provider's model list, not
// the union of all providers' models — otherwise a value that belongs to a
// different provider is mis-counted as a valid mapping for this config.
func TestConfigAPIKeyIndicator_scopes_to_named_provider(t *testing.T) {
	dir := t.TempDir()

	// A MiMo-named config whose env points at a GLM model (not a MiMo model).
	// Scoped to the MiMo model list, that value is not a valid mapping.
	os.WriteFile(filepath.Join(dir, "x.json"),
		[]byte(`{"env":{"ANTHROPIC_DEFAULT_OPUS_MODEL":"glm-4.6"}}`), 0644)
	if got := configAPIKeyIndicator(dir, "x.json", "Xiaomi MiMo"); got != "unmapped" {
		t.Errorf("mimo config mapped to a glm model: got %q, want %q", got, "unmapped")
	}

	// A GLM-named config mapping two of its own provider's models -> "2 mapped".
	os.WriteFile(filepath.Join(dir, "g.json"),
		[]byte(`{"env":{"ANTHROPIC_DEFAULT_OPUS_MODEL":"glm-4.6","ANTHROPIC_DEFAULT_SONNET_MODEL":"glm-5"}}`), 0644)
	if got := configAPIKeyIndicator(dir, "g.json", "Work GLM"); got != "2 mapped" {
		t.Errorf("glm config with two mapped models: got %q, want %q", got, "2 mapped")
	}

	// Empty config -> "unmapped".
	os.WriteFile(filepath.Join(dir, "e.json"), []byte(`{}`), 0644)
	if got := configAPIKeyIndicator(dir, "e.json", "Work GLM"); got != "unmapped" {
		t.Errorf("empty config: got %q, want %q", got, "unmapped")
	}
}
