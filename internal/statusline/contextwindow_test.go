package statusline

import (
	"encoding/json"
	"strings"
	"testing"
)

// RewriteContextWindow corrects the context_window object Claude Code hands the
// statusline: Claude Code hardcodes context_window_size to its own models'
// windows (200K/1M), so on a subscription pane running a third-party model the
// size — and the used/remaining percentages derived from it — misstate the real
// context. The rewrite swaps in the catalog's real window for the active model
// and recomputes the percentages against it.

func rewritten(t *testing.T, input string) map[string]any {
	t.Helper()
	out, changed := RewriteContextWindow([]byte(input))
	if !changed {
		t.Fatalf("expected a rewrite for input %s", input)
	}
	var data map[string]any
	if err := json.Unmarshal(out, &data); err != nil {
		t.Fatalf("rewritten output is not JSON: %v\n%s", err, out)
	}
	return data
}

func ctxWindow(t *testing.T, data map[string]any) map[string]any {
	t.Helper()
	cw, ok := data["context_window"].(map[string]any)
	if !ok {
		t.Fatalf("context_window missing from output: %v", data)
	}
	return cw
}

// glm-5.2's real window is 1M, not the 200K Claude Code assumes. With
// current_usage present the used percentage is recomputed from the real token
// counts (input + cache creation + cache read, output excluded — Claude's own
// formula) against the real window.
func TestRewrite_recomputes_pct_from_current_usage_tokens(t *testing.T) {
	input := `{"model":{"id":"glm-5.2","display_name":"GLM 5.2"},` +
		`"context_window":{"context_window_size":200000,` +
		`"total_input_tokens":100000,"total_output_tokens":5000,` +
		`"used_percentage":50,"remaining_percentage":50,` +
		`"current_usage":{"input_tokens":20000,"output_tokens":5000,` +
		`"cache_creation_input_tokens":30000,"cache_read_input_tokens":50000}}}`
	cw := ctxWindow(t, rewritten(t, input))
	if got := cw["context_window_size"]; got != float64(1000000) {
		t.Errorf("context_window_size = %v, want 1000000", got)
	}
	// used = (20000+30000+50000) / 1000000 = 10%
	if got := cw["used_percentage"]; got != float64(10) {
		t.Errorf("used_percentage = %v, want 10", got)
	}
	if got := cw["remaining_percentage"]; got != float64(90) {
		t.Errorf("remaining_percentage = %v, want 90", got)
	}
}

// Without current_usage the used tokens are recovered from the reported
// percentage times the reported window, then re-divided by the real one.
func TestRewrite_scales_pct_when_no_current_usage(t *testing.T) {
	input := `{"model":{"id":"gpt-5.6-sol"},` +
		`"context_window":{"context_window_size":200000,` +
		`"used_percentage":68,"remaining_percentage":32}}`
	cw := ctxWindow(t, rewritten(t, input))
	if got := cw["context_window_size"]; got != float64(272000) {
		t.Errorf("context_window_size = %v, want 272000", got)
	}
	// 68% of 200000 = 136000 tokens; 136000/272000 = 50%
	if got := cw["used_percentage"]; got != float64(50) {
		t.Errorf("used_percentage = %v, want 50", got)
	}
	if got := cw["remaining_percentage"]; got != float64(50) {
		t.Errorf("remaining_percentage = %v, want 50", got)
	}
}

// A model absent from the catalog (a native Claude id) must pass through
// untouched: Claude Code's own figures are already right for its own models.
func TestRewrite_unknown_model_passes_through(t *testing.T) {
	input := `{"model":{"id":"claude-fable-5"},` +
		`"context_window":{"context_window_size":200000,"used_percentage":50}}`
	out, changed := RewriteContextWindow([]byte(input))
	if changed {
		t.Fatalf("native model must not be rewritten")
	}
	if string(out) != input {
		t.Errorf("passthrough must return the input verbatim, got %s", out)
	}
}

// rate_limits carries its own used_percentage fields (the 5h/7d quota bars);
// the rewrite must never touch them.
func TestRewrite_leaves_rate_limits_untouched(t *testing.T) {
	input := `{"model":{"id":"glm-5.2"},` +
		`"rate_limits":{"seven_day":{"used_percentage":81},"five_hour":{"used_percentage":55}},` +
		`"context_window":{"context_window_size":200000,"used_percentage":50}}`
	data := rewritten(t, input)
	rl := data["rate_limits"].(map[string]any)
	if got := rl["seven_day"].(map[string]any)["used_percentage"]; got != float64(81) {
		t.Errorf("seven_day used_percentage = %v, want 81 (untouched)", got)
	}
	if got := rl["five_hour"].(map[string]any)["used_percentage"]; got != float64(55) {
		t.Errorf("five_hour used_percentage = %v, want 55 (untouched)", got)
	}
}

// No context_window object → nothing to fix, verbatim passthrough.
func TestRewrite_no_context_window_passes_through(t *testing.T) {
	input := `{"model":{"id":"glm-5.2"},"workspace":{"current_dir":"/tmp"}}`
	out, changed := RewriteContextWindow([]byte(input))
	if changed {
		t.Fatalf("input without context_window must not be rewritten")
	}
	if string(out) != input {
		t.Errorf("passthrough must return the input verbatim, got %s", out)
	}
}

// Null percentages (session start, right after /compact): only the window size
// is corrected; the null percentages stay null rather than becoming numbers
// invented from nothing.
func TestRewrite_null_percentages_stay_null(t *testing.T) {
	input := `{"model":{"id":"mimo-v2.5-pro"},` +
		`"context_window":{"context_window_size":200000,` +
		`"used_percentage":null,"remaining_percentage":null,"current_usage":null}}`
	cw := ctxWindow(t, rewritten(t, input))
	if got := cw["context_window_size"]; got != float64(1048576) {
		t.Errorf("context_window_size = %v, want 1048576", got)
	}
	if got := cw["used_percentage"]; got != nil {
		t.Errorf("used_percentage = %v, want null", got)
	}
	if got := cw["remaining_percentage"]; got != nil {
		t.Errorf("remaining_percentage = %v, want null", got)
	}
}

// Unrelated fields survive the round trip (the wrapper feeds the rewritten
// JSON to ccstatusline, which also reads model/effort/cost fields).
func TestRewrite_preserves_other_fields(t *testing.T) {
	input := `{"model":{"id":"glm-5.2","display_name":"GLM 5.2"},` +
		`"effort":{"level":"high"},"workspace":{"current_dir":"/tmp/x"},` +
		`"context_window":{"context_window_size":200000,"used_percentage":50}}`
	data := rewritten(t, input)
	model := data["model"].(map[string]any)
	if model["display_name"] != "GLM 5.2" {
		t.Errorf("display_name lost: %v", model)
	}
	if data["effort"].(map[string]any)["level"] != "high" {
		t.Errorf("effort lost: %v", data["effort"])
	}
	if data["workspace"].(map[string]any)["current_dir"] != "/tmp/x" {
		t.Errorf("workspace lost: %v", data["workspace"])
	}
}

// A shrunken real window must clamp at 100 rather than report >100% used.
func TestRewrite_clamps_used_percentage_at_100(t *testing.T) {
	// gpt-5.3-codex-spark's real window (128000) is smaller than the assumed
	// 200000: 90% of 200000 = 180000 used tokens > the whole real window.
	input := `{"model":{"id":"gpt-5.3-codex-spark"},` +
		`"context_window":{"context_window_size":200000,"used_percentage":90}}`
	cw := ctxWindow(t, rewritten(t, input))
	if got := cw["used_percentage"]; got != float64(100) {
		t.Errorf("used_percentage = %v, want clamped 100", got)
	}
	if got := cw["remaining_percentage"]; got != float64(0) {
		t.Errorf("remaining_percentage = %v, want 0", got)
	}
}

// Malformed input yields no output and no rewrite claim — the wrapper falls
// back to the original bytes.
func TestRewrite_malformed_input_not_rewritten(t *testing.T) {
	out, changed := RewriteContextWindow([]byte(`{"model":`))
	if changed {
		t.Fatalf("malformed input must not claim a rewrite")
	}
	if !strings.HasPrefix(string(out), `{"model":`) {
		t.Errorf("malformed input should pass through verbatim, got %q", out)
	}
}
