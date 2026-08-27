package claudeconfig

import (
	"os"
	"path/filepath"
	"testing"
)

// A profile written by an older wisp-deck, in the shape the modal produces.
func writeSelfHostedProfile(t *testing.T, dir, file string, extra string) {
	t.Helper()
	body := `{
  "$schema": "https://json.schemastore.org/claude-code-settings.json",
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "tok",
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:8000",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "Qwen-3.8",
    "WISP_DECK_SUBSCRIPTION_PROVIDER": "custom"` + extra + `
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
}

func TestAddForProvider_disarms_the_byte_watchdog_for_a_self_hosted_profile(t *testing.T) {
	dir, _, file := customConfig(t)
	if got := readEnv(t, dir, file)[ByteWatchdogKey]; got != "0" {
		t.Errorf("%s = %v, want %q", ByteWatchdogKey, got, "0")
	}
}

// A vendor gateway heartbeats its stream, so the watchdog's premise holds and
// disarming it there would trade a real dead-connection signal for nothing.
func TestAddForProvider_leaves_the_byte_watchdog_armed_for_a_gateway(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-configs.list")
	file, err := AddForProvider(list, dir, "Zhipu", "zhipu")
	if err != nil {
		t.Fatalf("AddForProvider: %v", err)
	}
	if _, ok := readEnv(t, dir, file)[ByteWatchdogKey]; ok {
		t.Errorf("gateway profile declares %s", ByteWatchdogKey)
	}
}

// The installer only copies a default profile when the file is absent, so a
// profile created before this fix is reachable only by the sweep.
func TestEnsureByteWatchdog_backfills_a_profile_written_before_the_fix(t *testing.T) {
	dir := t.TempDir()
	writeSelfHostedProfile(t, dir, "qwen.json", "")

	changed, err := EnsureByteWatchdog(dir, "qwen.json")
	if err != nil {
		t.Fatalf("EnsureByteWatchdog: %v", err)
	}
	if !changed {
		t.Error("sweep reported no change for a profile missing the key")
	}
	if got := readEnv(t, dir, "qwen.json")[ByteWatchdogKey]; got != "0" {
		t.Errorf("%s = %v, want %q", ByteWatchdogKey, got, "0")
	}
}

// Every launch path may call the sweep, so a second run must not rewrite.
func TestEnsureByteWatchdog_is_a_no_op_on_a_current_profile(t *testing.T) {
	dir := t.TempDir()
	writeSelfHostedProfile(t, dir, "qwen.json", "")
	if _, err := EnsureByteWatchdog(dir, "qwen.json"); err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	changed, err := EnsureByteWatchdog(dir, "qwen.json")
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if changed {
		t.Error("second sweep rewrote an already-current profile")
	}
}

// The user may have armed it deliberately on an endpoint that does heartbeat.
func TestEnsureByteWatchdog_keeps_a_hand_set_value(t *testing.T) {
	dir := t.TempDir()
	writeSelfHostedProfile(t, dir, "qwen.json", `,
    "`+ByteWatchdogKey+`": "1"`)

	changed, err := EnsureByteWatchdog(dir, "qwen.json")
	if err != nil {
		t.Fatalf("EnsureByteWatchdog: %v", err)
	}
	if changed {
		t.Error("sweep overwrote a hand-set value")
	}
	if got := readEnv(t, dir, "qwen.json")[ByteWatchdogKey]; got != "1" {
		t.Errorf("%s = %v, want %q", ByteWatchdogKey, got, "1")
	}
}

func TestEnsureByteWatchdog_leaves_a_gateway_profile_alone(t *testing.T) {
	dir := t.TempDir()
	body := `{"env":{"ANTHROPIC_BASE_URL":"https://api.z.ai/api/anthropic",` +
		`"WISP_DECK_SUBSCRIPTION_PROVIDER":"zhipu"}}`
	if err := os.WriteFile(filepath.Join(dir, "zhipu-glm.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	changed, err := EnsureByteWatchdog(dir, "zhipu-glm.json")
	if err != nil {
		t.Fatalf("EnsureByteWatchdog: %v", err)
	}
	if changed {
		t.Error("sweep touched a gateway profile")
	}
	if _, ok := readEnv(t, dir, "zhipu-glm.json")[ByteWatchdogKey]; ok {
		t.Errorf("gateway profile declares %s", ByteWatchdogKey)
	}
}

// A profile whose name matches no alias falls through to Providers[0], so the
// sweep must read the explicit marker, never the filename.
func TestEnsureByteWatchdog_reads_the_marker_not_the_filename(t *testing.T) {
	dir := t.TempDir()
	writeSelfHostedProfile(t, dir, "qwen.json", "")
	if _, err := EnsureByteWatchdog(dir, "qwen.json"); err != nil {
		t.Fatalf("EnsureByteWatchdog: %v", err)
	}
	if got := readEnv(t, dir, "qwen.json")[ByteWatchdogKey]; got != "0" {
		t.Errorf("a marker-identified profile was skipped: %s = %v", ByteWatchdogKey, got)
	}
}

// One hand-edited file must not leave every other profile unrepaired.
func TestEnsureByteWatchdogAll_repairs_every_self_hosted_profile(t *testing.T) {
	dir := t.TempDir()
	writeSelfHostedProfile(t, dir, "qwen.json", "")
	writeSelfHostedProfile(t, dir, "pod.json", "")
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	changed, err := EnsureByteWatchdogAll(dir)
	if err != nil {
		t.Fatalf("EnsureByteWatchdogAll: %v", err)
	}
	if changed != 2 {
		t.Errorf("changed = %d, want 2", changed)
	}
	for _, file := range []string{"qwen.json", "pod.json"} {
		if got := readEnv(t, dir, file)[ByteWatchdogKey]; got != "0" {
			t.Errorf("%s: %s = %v, want %q", file, ByteWatchdogKey, got, "0")
		}
	}
}

// Converting an existing profile to the self-hosted provider must disarm it
// there and then; waiting for the next install sweep leaves the session lying.
func TestWriteProviderMarker_disarms_the_byte_watchdog_for_self_hosted(t *testing.T) {
	dir := t.TempDir()
	body := `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:8000",` +
		`"WISP_DECK_SUBSCRIPTION_PROVIDER":"zhipu"}}`
	if err := os.WriteFile(filepath.Join(dir, "qwen.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	if err := WriteProviderMarker(dir, "qwen.json", "custom"); err != nil {
		t.Fatalf("WriteProviderMarker: %v", err)
	}
	if got := readEnv(t, dir, "qwen.json")[ByteWatchdogKey]; got != "0" {
		t.Errorf("%s = %v, want %q", ByteWatchdogKey, got, "0")
	}
}
