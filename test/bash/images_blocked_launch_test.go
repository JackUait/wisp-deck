package bash_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

// The Images toggle is nothing but permission rules in the profile, so the
// launch overlay carrying `permissions` verbatim is what makes it work at all.
// The overlay copies the whole settings object today; this fails the moment
// someone narrows it to an allowlist of keys, which would silently hand a
// text-only model its images back.
func TestClaudeLaunchSettingsCarriesTheImageDenyRules(t *testing.T) {
	dir := t.TempDir()
	generationDir := filepath.Join(dir, "runtime", "generation.Img123")
	if err := os.MkdirAll(generationDir, 0o700); err != nil {
		t.Fatal(err)
	}

	configsDir := filepath.Join(dir, "claude-configs")
	if err := os.MkdirAll(configsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := claudeconfig.AddForProvider(
		filepath.Join(dir, "claude-configs.list"), configsDir, "Qwen", "custom")
	if err != nil {
		t.Fatalf("AddForProvider: %v", err)
	}
	if err := claudeconfig.WriteImagesBlocked(configsDir, file, true); err != nil {
		t.Fatalf("WriteImagesBlocked: %v", err)
	}

	snippet := settingsJsonSnippet(t, fmt.Sprintf(
		`write_claude_launch_settings %q %q`, generationDir, filepath.Join(configsDir, file)))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(filepath.Join(generationDir, "claude-settings.json"))
	if err != nil {
		t.Fatalf("read overlay: %v", err)
	}
	var overlay struct {
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &overlay); err != nil {
		t.Fatalf("parse overlay: %v", err)
	}
	present := make(map[string]bool, len(overlay.Permissions.Deny))
	for _, rule := range overlay.Permissions.Deny {
		present[rule] = true
	}
	for _, want := range claudeconfig.ImageReadDenyRules() {
		if !present[want] {
			t.Errorf("launch overlay is missing %q; deny = %v", want, overlay.Permissions.Deny)
		}
	}
}
