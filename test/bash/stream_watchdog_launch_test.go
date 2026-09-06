package bash_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

// Disarming the event-tier watchdog is one env key in the profile, so the
// launch overlay carrying `env` verbatim is what makes it reach the pane. The
// overlay copies the whole settings object today; this fails the moment someone
// narrows it, which would silently restore the 600s abort-and-replay on every
// subscription pane.
func TestClaudeLaunchSettingsCarriesTheStreamWatchdogDisarm(t *testing.T) {
	for _, provider := range []string{"custom", "zhipu"} {
		t.Run(provider, func(t *testing.T) {
			dir := t.TempDir()
			generationDir := filepath.Join(dir, "runtime", "generation.Wd123")
			if err := os.MkdirAll(generationDir, 0o700); err != nil {
				t.Fatal(err)
			}
			configsDir := filepath.Join(dir, "claude-configs")
			if err := os.MkdirAll(configsDir, 0o700); err != nil {
				t.Fatal(err)
			}
			file, err := claudeconfig.AddForProvider(
				filepath.Join(dir, "claude-configs.list"), configsDir, "Profile", provider)
			if err != nil {
				t.Fatalf("AddForProvider: %v", err)
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
				Env map[string]string `json:"env"`
			}
			if err := json.Unmarshal(data, &overlay); err != nil {
				t.Fatalf("parse overlay: %v", err)
			}
			if got := overlay.Env[claudeconfig.StreamWatchdogKey]; got != "0" {
				t.Errorf("launch overlay declares %s=%q, want %q; env = %v",
					claudeconfig.StreamWatchdogKey, got, "0", overlay.Env)
			}
		})
	}
}
