package rolefix

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeSettings(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUpstreamFromSettings_reads_the_profiles_endpoint(t *testing.T) {
	path := writeSettings(t, `{"env":{"ANTHROPIC_BASE_URL":"https://api.featherless.ai","ANTHROPIC_AUTH_TOKEN":"rc_x"}}`)
	got, err := UpstreamFromSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://api.featherless.ai" {
		t.Errorf("upstream = %q", got)
	}
}

// A launch must never be broken by this: with nothing to proxy, the caller runs
// the child untouched rather than failing.
func TestUpstreamFromSettings_reports_nothing_to_proxy(t *testing.T) {
	for name, body := range map[string]string{
		"no endpoint": `{"env":{"ANTHROPIC_AUTH_TOKEN":"rc_x"}}`,
		"no env":      `{}`,
		"loopback":    `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:9/v1"}}`,
		"not http":    `{"env":{"ANTHROPIC_BASE_URL":"ftp://host"}}`,
		"unparseable": `{not json`,
	} {
		if got, err := UpstreamFromSettings(writeSettings(t, body)); err == nil || got != "" {
			t.Errorf("%s: got (%q, %v), want an empty upstream and an error", name, got, err)
		}
	}
}

// The overlay is the session's own copy, and everything else in it — the API
// key, the model aliases, the declared window, the image deny rules — must
// survive being pointed at the proxy.
func TestPointSettingsAt_replaces_only_the_endpoint(t *testing.T) {
	path := writeSettings(t, `{
		"$schema": "https://json.schemastore.org/claude-code-settings.json",
		"permissions": {"deny": ["Read(//**/*.png)"]},
		"env": {
			"ANTHROPIC_BASE_URL": "https://api.featherless.ai",
			"ANTHROPIC_AUTH_TOKEN": "rc_secret",
			"ANTHROPIC_DEFAULT_OPUS_MODEL": "zai-org/GLM-5.3-Flash",
			"CLAUDE_CODE_MAX_CONTEXT_TOKENS": "262144"
		}
	}`)
	if err := PointSettingsAt(path, "http://127.0.0.1:54321"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	env, _ := settings["env"].(map[string]any)
	if got, _ := env["ANTHROPIC_BASE_URL"].(string); got != "http://127.0.0.1:54321" {
		t.Errorf("endpoint = %q, want the proxy", got)
	}
	if got, _ := env["ANTHROPIC_AUTH_TOKEN"].(string); got != "rc_secret" {
		t.Error("the credential must survive: the proxy forwards it, it does not hold one")
	}
	if got, _ := env["ANTHROPIC_DEFAULT_OPUS_MODEL"].(string); got != "zai-org/GLM-5.3-Flash" {
		t.Error("the picked model must survive")
	}
	if got, _ := env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"].(string); got != "262144" {
		t.Error("the declared window must survive, or the session strands on the flat default")
	}
	if _, ok := settings["permissions"]; !ok {
		t.Error("the image deny rules must survive")
	}
	if _, ok := settings["$schema"]; !ok {
		t.Error("every other key must survive")
	}
}

// The overlay carries the API key, so the rewritten file must not widen its
// permissions.
func TestPointSettingsAt_keeps_the_file_private(t *testing.T) {
	path := writeSettings(t, `{"env":{"ANTHROPIC_BASE_URL":"https://api.featherless.ai"}}`)
	if err := PointSettingsAt(path, "http://127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("mode = %o, want no group or world access", mode)
	}
}
