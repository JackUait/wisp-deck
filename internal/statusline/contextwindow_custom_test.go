package statusline

import (
	"encoding/json"
	"testing"
)

func windowSize(t *testing.T, raw []byte) float64 {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("parse: %v", err)
	}
	window, _ := data["context_window"].(map[string]any)
	size, _ := window["context_window_size"].(float64)
	return size
}

const customStatuslineJSON = `{
  "model": {"id": "qwen3-coder"},
  "context_window": {"context_window_size": 200000, "current_usage": {"input_tokens": 50000}}
}`

// A self-hosted model is in no catalog, so the only thing that knows its window
// is the profile the pane launched with — which exports the figure Claude Code
// itself budgets from. Without it the gauge measures against a 200000 nobody
// enforces.
func TestRewriteContextWindow_sizes_an_uncatalogued_model_from_the_profile(t *testing.T) {
	t.Setenv("CLAUDE_CODE_MAX_CONTEXT_TOKENS", "131072")

	out, changed := RewriteContextWindow([]byte(customStatuslineJSON))
	if !changed {
		t.Fatal("declared window ignored for an uncatalogued model")
	}
	if got := windowSize(t, out); got != 131072 {
		t.Errorf("context_window_size = %v, want 131072", got)
	}
}

func TestRewriteContextWindow_ignores_an_unusable_declared_window(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "lots"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("CLAUDE_CODE_MAX_CONTEXT_TOKENS", value)
			out, changed := RewriteContextWindow([]byte(customStatuslineJSON))
			if changed {
				t.Errorf("window %q was used, want the input echoed through", value)
			}
			if got := windowSize(t, out); got != 200000 {
				t.Errorf("context_window_size = %v, want the input untouched", got)
			}
		})
	}
}

// The catalog stays the authority for models it knows: the env var governs a
// whole session and is the MINIMUM across its four aliases, so letting it win
// would size a gauge for the tightest model even while a roomier one runs.
func TestRewriteContextWindow_prefers_the_catalog_over_the_declared_window(t *testing.T) {
	t.Setenv("CLAUDE_CODE_MAX_CONTEXT_TOKENS", "131072")

	raw := []byte(`{"model":{"id":"kimi-k3"},"context_window":{"context_window_size":200000}}`)
	out, changed := RewriteContextWindow(raw)
	if !changed {
		t.Fatal("catalog model was not resized")
	}
	if got := windowSize(t, out); got != 1048576 {
		t.Errorf("context_window_size = %v, want kimi-k3's catalog window", got)
	}
}
