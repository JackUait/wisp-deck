package claudeconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Every endpoint wisp-deck points Claude Code at is reached over a stream whose
// quiet stretches it cannot bound, so every profile it writes disarms the
// event-tier watchdog — a gateway included, unlike the byte watchdog.
func TestAddForProvider_disarms_the_stream_watchdog_for_a_gateway(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-configs.list")
	file, err := AddForProvider(list, dir, "Zhipu", "zhipu")
	if err != nil {
		t.Fatalf("AddForProvider: %v", err)
	}
	if got := readEnv(t, dir, file)[StreamWatchdogKey]; got != "0" {
		t.Errorf("%s = %v, want %q", StreamWatchdogKey, got, "0")
	}
}

func TestAddForProvider_disarms_the_stream_watchdog_for_a_self_hosted_profile(t *testing.T) {
	dir, _, file := customConfig(t)
	if got := readEnv(t, dir, file)[StreamWatchdogKey]; got != "0" {
		t.Errorf("%s = %v, want %q", StreamWatchdogKey, got, "0")
	}
}

// The installer copies a default profile only when the file is absent, so a
// profile written before this key was declared is reachable only by the sweep.
func TestEnsureStreamWatchdog_backfills_a_profile_written_before_the_fix(t *testing.T) {
	dir := t.TempDir()
	writeSelfHostedProfile(t, dir, "qwen.json", "")

	changed, err := EnsureStreamWatchdog(dir, "qwen.json")
	if err != nil {
		t.Fatalf("EnsureStreamWatchdog: %v", err)
	}
	if !changed {
		t.Fatal("sweep reported no change for a profile missing the key")
	}
	if got := readEnvMap(t, filepath.Join(dir, "qwen.json"))[StreamWatchdogKey]; got != "0" {
		t.Errorf("%s = %q, want %q", StreamWatchdogKey, got, "0")
	}
}

// Every launch path may run the sweep, so a second pass must change nothing.
func TestEnsureStreamWatchdog_is_idempotent(t *testing.T) {
	dir := t.TempDir()
	writeSelfHostedProfile(t, dir, "qwen.json", "")
	if _, err := EnsureStreamWatchdog(dir, "qwen.json"); err != nil {
		t.Fatalf("EnsureStreamWatchdog: %v", err)
	}
	changed, err := EnsureStreamWatchdog(dir, "qwen.json")
	if err != nil {
		t.Fatalf("EnsureStreamWatchdog: %v", err)
	}
	if changed {
		t.Error("second sweep rewrote a profile that already declares the key")
	}
}

// The user may have armed it deliberately on an endpoint that does emit a real
// stream event regularly, so a declared value is theirs to keep.
func TestEnsureStreamWatchdog_keeps_a_declared_value(t *testing.T) {
	dir := t.TempDir()
	writeSelfHostedProfile(t, dir, "qwen.json", `,
    "CLAUDE_ENABLE_STREAM_WATCHDOG": "1"`)

	changed, err := EnsureStreamWatchdog(dir, "qwen.json")
	if err != nil {
		t.Fatalf("EnsureStreamWatchdog: %v", err)
	}
	if changed {
		t.Error("sweep overwrote the user's own value")
	}
	if got := readEnvMap(t, filepath.Join(dir, "qwen.json"))[StreamWatchdogKey]; got != "1" {
		t.Errorf("%s = %q, want the user's own %q", StreamWatchdogKey, got, "1")
	}
}

// One hand-edited file must not leave every other profile unrepaired.
func TestEnsureStreamWatchdogAll_sweeps_past_an_unparseable_profile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeSelfHostedProfile(t, dir, "qwen.json", "")

	changed, err := EnsureStreamWatchdogAll(dir)
	if err != nil {
		t.Fatalf("EnsureStreamWatchdogAll: %v", err)
	}
	if changed != 1 {
		t.Errorf("swept %d profiles, want 1", changed)
	}
}

// A shipped default is copied verbatim on a fresh install and is never
// re-copied, so one that leaves the watchdog armed ships the abort to every new
// user of that provider with only the sweep to save them.
func TestShippedDefaultConfigs_disarm_the_stream_watchdog(t *testing.T) {
	dir := filepath.Join("..", "..", "defaults", "claude-configs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			var settings struct {
				Env map[string]string `json:"env"`
			}
			if err := json.Unmarshal(data, &settings); err != nil {
				t.Fatal(err)
			}
			seen++
			if got := settings.Env[StreamWatchdogKey]; got != "0" {
				t.Errorf("%s declares %s=%q, want %q", entry.Name(), StreamWatchdogKey, got, "0")
			}
		})
	}
	if seen == 0 {
		t.Error("no shipped default was read — the guard checked nothing")
	}
}
