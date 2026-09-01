package claudeconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// The picked Featherless model's whole context is 32768. Claude Code sizes
// max_tokens from its own catalog, which has never heard of the model, so it
// asks for 32000 — 97.6% of the window — and the endpoint rejects
// input+max_tokens over the window with a 400 that kills every turn.
//
// Measured against api.featherless.ai on 2026-09-02 with a request captured
// from a live pane: max_tokens 32000 and 30000 both 400, 28000 and below 200,
// and the boundary moved down when ~4000 tokens of input were added — so the
// rule is input + max_tokens <= context. The same profile carrying an 8192
// reserve completed the turn.
func TestStampContextBudget_reserves_output_room_inside_the_window(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "featherless-qwen.json")
	profile := `{
  "env": {
    "WISP_DECK_SUBSCRIPTION_PROVIDER": "featherless",
    "ANTHROPIC_BASE_URL": "https://api.featherless.ai",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "TurboVadim/Qwen3.8-27B-OBLITERATED",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "TurboVadim/Qwen3.8-27B-OBLITERATED",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "TurboVadim/Qwen3.8-27B-OBLITERATED",
    "ANTHROPIC_DEFAULT_FABLE_MODEL": "TurboVadim/Qwen3.8-27B-OBLITERATED",
    "CLAUDE_CODE_MAX_CONTEXT_TOKENS": "32768"
  }
}`
	if err := os.WriteFile(path, []byte(profile), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureContextBudget(dir, "featherless-qwen.json"); err != nil {
		t.Fatalf("EnsureContextBudget: %v", err)
	}
	env := readEnvMap(t, path)
	if got := env[OutputReserveKey]; got != "8192" {
		t.Errorf("output reserve = %q, want 8192", got)
	}
	// Both window keys keep naming the endpoint's real limit: Claude Code sizes
	// its own auto-compact buffer from the reserve, so taking the reply's room
	// out of the window a second time buys nothing.
	if got := env[ContextBudgetKey]; got != "32768" {
		t.Errorf("declared window = %q, want the endpoint's real 32768", got)
	}
	if got := env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; got != "32768" {
		t.Errorf("auto-compact window = %q, want the window itself", got)
	}
}

// The sweep is the only thing that can repair a profile already on disk, and
// the profiles that hit this bug already declare all three window keys
// correctly. A change-check that only compares those three reports "unchanged"
// and never writes the file — leaving exactly the broken profiles broken.
func TestEnsureContextBudget_backfills_the_reserve_on_a_window_current_profile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "featherless-qwen.json")
	current := `{
  "env": {
    "WISP_DECK_SUBSCRIPTION_PROVIDER": "featherless",
    "ANTHROPIC_BASE_URL": "https://api.featherless.ai",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "TurboVadim/Qwen3.8-27B-OBLITERATED",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "TurboVadim/Qwen3.8-27B-OBLITERATED",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "TurboVadim/Qwen3.8-27B-OBLITERATED",
    "ANTHROPIC_DEFAULT_FABLE_MODEL": "TurboVadim/Qwen3.8-27B-OBLITERATED",
    "CLAUDE_CODE_MAX_CONTEXT_TOKENS": "32768",
    "CLAUDE_CODE_AUTO_COMPACT_WINDOW": "32768",
    "CLAUDE_CODE_DISABLE_1M_CONTEXT": "1"
  }
}`
	if err := os.WriteFile(path, []byte(current), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureContextBudget(dir, "featherless-qwen.json")
	if err != nil {
		t.Fatalf("EnsureContextBudget: %v", err)
	}
	if !changed {
		t.Fatal("a profile whose window keys are current but which reserves no output room must be rewritten")
	}
	if got := readEnvMap(t, path)[OutputReserveKey]; got != "8192" {
		t.Errorf("backfilled reserve = %q, want 8192", got)
	}

	changed, err = EnsureContextBudget(dir, "featherless-qwen.json")
	if err != nil {
		t.Fatalf("EnsureContextBudget (second pass): %v", err)
	}
	if changed {
		t.Error("a repaired profile must be left alone on the next sweep")
	}
}

// Claude Code's own default is 32000, so the reserve never raises it — it only
// takes it down to what a small window can actually spare.
func TestOutputReserve_fits_a_small_window_without_raising_claude_codes_ceiling(t *testing.T) {
	for _, tc := range []struct {
		name   string
		window int
		want   int
	}{
		{"a 32K self-hosted model spares a quarter", 32768, 8192},
		{"a 128K window is already capped by Claude's default", 128000, 32000},
		{"a 262144 window keeps the whole default", 262144, 32000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := outputReserve(tc.window); got != tc.want {
				t.Errorf("outputReserve(%d) = %d, want %d", tc.window, got, tc.want)
			}
			env := contextWindowEnv(strconv.Itoa(tc.window), 0)
			if got := env[OutputReserveKey]; got != strconv.Itoa(tc.want) {
				t.Errorf("stamped reserve = %q, want %d", got, tc.want)
			}
		})
	}
}

// A window small enough that a quarter of it rounds to nothing is already
// unusable, but stamping max_tokens 0 makes it worse: every endpoint rejects
// that outright, so the pane fails for a reason that has nothing to do with the
// window the user typed.
func TestOutputReserve_never_asks_for_no_output_at_all(t *testing.T) {
	for _, window := range []int{1, 2, 3, 4} {
		if got := outputReserve(window); got < 1 {
			t.Errorf("outputReserve(%d) = %d, want at least 1", window, got)
		}
		if got := contextWindowEnv(strconv.Itoa(window), 0)[OutputReserveKey]; got == "0" {
			t.Errorf("a %d-token window stamped a zero reserve", window)
		}
	}
}

// The user may have sized the reply themselves on an endpoint whose real output
// cap they know, so their figure survives every sweep — the same rule the byte
// watchdog's key follows.
func TestEnsureContextBudget_keeps_a_declared_output_reserve(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.json")
	declared := `{
  "env": {
    "WISP_DECK_SUBSCRIPTION_PROVIDER": "custom",
    "ANTHROPIC_BASE_URL": "https://gpu.example.com",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "Qwen3-32B",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "Qwen3-32B",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "Qwen3-32B",
    "ANTHROPIC_DEFAULT_FABLE_MODEL": "Qwen3-32B",
    "CLAUDE_CODE_MAX_CONTEXT_TOKENS": "32768",
    "CLAUDE_CODE_MAX_OUTPUT_TOKENS": "4096"
  }
}`
	if err := os.WriteFile(path, []byte(declared), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureContextBudget(dir, "custom.json"); err != nil {
		t.Fatalf("EnsureContextBudget: %v", err)
	}
	if got := readEnvMap(t, path)[OutputReserveKey]; got != "4096" {
		t.Errorf("declared reserve = %q, want the user's own 4096", got)
	}
}

// A shipped default is copied verbatim on a fresh install and is never
// re-copied, so one that reserves nothing ships the bug to every new user of
// that provider with only the sweep to save them.
func TestShippedDefaultConfigs_declare_the_reserve_their_window_implies(t *testing.T) {
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
			window, err := strconv.Atoi(settings.Env[ContextBudgetKey])
			if err != nil || window >= oneMillionContextSize {
				t.Skipf("%s declares no sub-1M window", entry.Name())
			}
			seen++
			want := strconv.Itoa(outputReserve(window))
			if got := settings.Env[OutputReserveKey]; got != want {
				t.Errorf("%s reserves %q of its %d window, want %s", entry.Name(), got, window, want)
			}
		})
	}
	if seen == 0 {
		t.Error("no shipped default declared a sub-1M window — the guard checked nothing")
	}
}
