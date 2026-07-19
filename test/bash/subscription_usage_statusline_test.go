package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- gt_sub_usage_fresh ---
// The freshness gate for the subscription usage cache: only a snapshot whose
// fetched_at is within max_age of now may drive the statusline bars — stale
// data hides rather than lies.

func TestSubUsage_fresh_accepts_recent_fetched_at(t *testing.T) {
	json := `{"rate_limits":{"seven_day":{"used_percentage":70}},"fetched_at":1000}`
	_, code := runBashFunc(t, "lib/statusline.sh", "gt_sub_usage_fresh",
		[]string{json, "1500"}, nil)
	assertExitCode(t, code, 0)
}

func TestSubUsage_fresh_rejects_stale_fetched_at(t *testing.T) {
	json := `{"rate_limits":{},"fetched_at":1000}`
	_, code := runBashFunc(t, "lib/statusline.sh", "gt_sub_usage_fresh",
		[]string{json, "20000"}, nil)
	if code == 0 {
		t.Fatal("a snapshot older than the default max age must not be fresh")
	}
}

func TestSubUsage_fresh_rejects_missing_fetched_at(t *testing.T) {
	_, code := runBashFunc(t, "lib/statusline.sh", "gt_sub_usage_fresh",
		[]string{`{"rate_limits":{}}`, "100"}, nil)
	if code == 0 {
		t.Fatal("a snapshot without fetched_at must not be fresh")
	}
}

func TestSubUsage_fresh_honors_custom_max_age(t *testing.T) {
	json := `{"fetched_at":1000}`
	_, code := runBashFunc(t, "lib/statusline.sh", "gt_sub_usage_fresh",
		[]string{json, "1200", "100"}, nil)
	if code == 0 {
		t.Fatal("snapshot outside the custom max age must not be fresh")
	}
}

// The Go cache serializes claude's rate_limits shape precisely so the existing
// extractors parse it — guard that contract from the bash side too.
func TestSubUsage_cache_shape_parses_with_existing_extractors(t *testing.T) {
	cache := `{"rate_limits":{"five_hour":{"used_percentage":55},"seven_day":{"used_percentage":81}},"provider":"zhipu","fetched_at":123}`
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_five_hour_used_pct", []string{cache}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "55" {
		t.Fatalf("five_hour pct = %q, want 55", strings.TrimSpace(out))
	}
	out, code = runBashFunc(t, "lib/statusline.sh", "gt_weekly_used_pct", []string{cache}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "81" {
		t.Fatalf("weekly pct = %q, want 81", strings.TrimSpace(out))
	}
}

// --- statusline-wrapper: subscription panes show the SUBSCRIPTION's usage ---

// seedSubUsageCache writes a subscription usage snapshot where the wrapper
// looks for it (XDG root/wisp-deck/subscription-usage/<config>).
func seedSubUsageCache(t *testing.T, fakeHome, config, json string) {
	t.Helper()
	cfg := filepath.Join(fakeHome, ".config", "wisp-deck")
	writeTempFile(t, filepath.Join(cfg, "subscription-usage"), config, json)
}

func runWrapperWithInput(t *testing.T, env []string, stdinData string) (string, int) {
	t.Helper()
	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)
	return runBashSnippet(t, script, env)
}

// A subscription pane renders the bars from the subscription's own cached
// usage — even when the pane has a single (Default) login, where the account
// segment is ineligible.
func TestSubUsage_wrapper_bars_come_from_subscription_cache(t *testing.T) {
	env := setupWrapperTest(t)
	env = append(env, "CLAUDE_CONFIG_DIR=", "WISP_DECK_CLAUDE_CONFIG=glm.json")
	fakeHome := wrapperHome(env)
	cfg := filepath.Join(fakeHome, ".config", "wisp-deck")
	writeTempFile(t, cfg, "settings", "usage_bars=both\n")
	writeTempFile(t, cfg, "claude-config-colors", "glm.json:205\n")
	now := time.Now().Unix()
	seedSubUsageCache(t, fakeHome, "glm.json", fmt.Sprintf(
		`{"rate_limits":{"five_hour":{"used_percentage":50},"seven_day":{"used_percentage":90}},"provider":"zhipu","fetched_at":%d}`, now))

	out, code := runWrapperWithInput(t, env,
		`{"model":{"id":"claude-fable-5","display_name":"Fable 5"},"workspace":{"current_dir":"/tmp"}}`)
	assertExitCode(t, code, 0)
	assertContains(t, out, bar(9)) // 7d: 90%
	assertContains(t, out, bar(5)) // 5h: 50%
	// Painted in the subscription's color.
	assertContains(t, out, "\x1b[38;5;205m"+bar(9))
}

// Native rate_limits belong to the LOGIN, not the subscription — on a
// subscription pane they must never drive the bars.
func TestSubUsage_wrapper_ignores_native_rate_limits_on_subscription_pane(t *testing.T) {
	env := setupWrapperTest(t)
	env = append(env, "CLAUDE_CONFIG_DIR=", "WISP_DECK_CLAUDE_CONFIG=glm.json")
	fakeHome := wrapperHome(env)
	cfg := filepath.Join(fakeHome, ".config", "wisp-deck")
	writeTempFile(t, cfg, "settings", "usage_bars=7d\n")
	now := time.Now().Unix()
	seedSubUsageCache(t, fakeHome, "glm.json", fmt.Sprintf(
		`{"rate_limits":{"seven_day":{"used_percentage":20}},"provider":"zhipu","fetched_at":%d}`, now))

	out, code := runWrapperWithInput(t, env,
		`{"model":{"id":"claude-fable-5","display_name":"Fable 5"},"rate_limits":{"seven_day":{"used_percentage":90,"resets_at":2}},"workspace":{"current_dir":"/tmp"}}`)
	assertExitCode(t, code, 0)
	assertContains(t, out, bar(2))    // the subscription's 20%
	assertNotContains(t, out, bar(9)) // never the login's 90%
}

// A stale cache hides the figures: the bar shows the "…" placeholder instead
// of yesterday's percentages (and never the login's native ones).
func TestSubUsage_wrapper_stale_cache_shows_placeholder(t *testing.T) {
	env := setupWrapperTest(t)
	env = append(env, "CLAUDE_CONFIG_DIR=", "WISP_DECK_CLAUDE_CONFIG=glm.json")
	fakeHome := wrapperHome(env)
	cfg := filepath.Join(fakeHome, ".config", "wisp-deck")
	writeTempFile(t, cfg, "settings", "usage_bars=7d\n")
	stale := time.Now().Unix() - 100000
	seedSubUsageCache(t, fakeHome, "glm.json", fmt.Sprintf(
		`{"rate_limits":{"seven_day":{"used_percentage":90}},"provider":"zhipu","fetched_at":%d}`, stale))

	out, code := runWrapperWithInput(t, env,
		`{"model":{"id":"claude-fable-5","display_name":"Fable 5"},"workspace":{"current_dir":"/tmp"}}`)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, bar(9))
	assertContains(t, out, "…")
}

// The wrapper keeps the cache alive: every render on a subscription pane
// spawns the (self-throttling) refresher with the pane's config.
func TestSubUsage_wrapper_spawns_refresher_for_subscription_pane(t *testing.T) {
	env := setupWrapperTest(t)
	env = append(env, "CLAUDE_CONFIG_DIR=", "WISP_DECK_CLAUDE_CONFIG=glm.json")
	fakeHome := wrapperHome(env)
	logFile := filepath.Join(fakeHome, "tui-calls.log")

	// The mock stands in for wisp-deck-tui and records its argv.
	dir := filepath.Dir(filepath.Dir(fakeHome))
	_ = dir
	mockDir := filepath.Join(fakeHome, "..")
	binDir := mockCommand(t, mockDir, "wisp-deck-tui", fmt.Sprintf(`printf '%%s\n' "$*" >> %q`, logFile))
	env = prependPath(env, binDir)

	_, code := runWrapperWithInput(t, env,
		`{"model":{"id":"claude-fable-5","display_name":"Fable 5"},"workspace":{"current_dir":"/tmp"}}`)
	assertExitCode(t, code, 0)

	var recorded string
	for i := 0; i < 40; i++ { // the refresher is backgrounded; give it a beat
		if data, err := os.ReadFile(logFile); err == nil && len(data) > 0 {
			recorded = string(data)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(recorded, "subscription-usage") {
		t.Fatalf("refresher not spawned; recorded calls: %q", recorded)
	}
	if !strings.Contains(recorded, "--config glm.json") {
		t.Fatalf("refresher missing the pane's config: %q", recorded)
	}
}

// A standard-Claude pane must not spawn the refresher at all.
func TestSubUsage_wrapper_no_refresher_without_subscription(t *testing.T) {
	env := setupWrapperTest(t)
	env = append(env, "CLAUDE_CONFIG_DIR=", "WISP_DECK_CLAUDE_CONFIG=")
	fakeHome := wrapperHome(env)
	logFile := filepath.Join(fakeHome, "tui-calls.log")
	mockDir := filepath.Join(fakeHome, "..")
	binDir := mockCommand(t, mockDir, "wisp-deck-tui", fmt.Sprintf(`printf '%%s\n' "$*" >> %q`, logFile))
	env = prependPath(env, binDir)

	_, code := runWrapperWithInput(t, env,
		`{"model":{"id":"claude-fable-5","display_name":"Fable 5"},"workspace":{"current_dir":"/tmp"}}`)
	assertExitCode(t, code, 0)
	time.Sleep(200 * time.Millisecond)
	if data, err := os.ReadFile(logFile); err == nil && strings.Contains(string(data), "subscription-usage") {
		t.Fatalf("refresher spawned on a standard pane: %q", string(data))
	}
}

// prependPath returns env with dir prepended to its PATH entry.
func prependPath(env []string, dir string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			kv = "PATH=" + dir + ":" + strings.TrimPrefix(kv, "PATH=")
		}
		out = append(out, kv)
	}
	return out
}
