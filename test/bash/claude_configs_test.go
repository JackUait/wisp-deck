package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadClaudeConfigs_skips_comments_blanks(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "list", "# header\n\nWork:work.json\nPersonal:personal.json\n")
	out, code := runBashFunc(t, "lib/claude-configs.sh", "load_claude_configs",
		[]string{filepath.Join(dir, "list")}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Work:work.json")
	assertContains(t, out, "Personal:personal.json")
	assertNotContains(t, out, "header")
}

func TestActivePointer_get_set_and_standard_clears(t *testing.T) {
	dir := t.TempDir()
	ptr := filepath.Join(dir, "claude-config")
	if _, code := runBashFunc(t, "lib/claude-configs.sh", "set_active_claude_config",
		[]string{ptr, "work.json"}, nil); code != 0 {
		t.Fatalf("set failed")
	}
	out, _ := runBashFunc(t, "lib/claude-configs.sh", "get_active_claude_config", []string{ptr}, nil)
	assertContains(t, out, "work.json")
	if _, code := runBashFunc(t, "lib/claude-configs.sh", "set_active_claude_config",
		[]string{ptr, "standard"}, nil); code != 0 {
		t.Fatalf("set standard failed")
	}
	if _, err := os.Stat(ptr); !os.IsNotExist(err) {
		t.Fatalf("pointer should be removed for standard")
	}
}

func TestResolveClaudeConfigPath_existing_vs_missing(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, cfgDir, "work.json", "{}")
	ptr := filepath.Join(dir, "claude-config")
	writeTempFile(t, dir, "claude-config", "work.json")
	out, _ := runBashFunc(t, "lib/claude-configs.sh", "resolve_claude_config_path",
		[]string{cfgDir, ptr}, nil)
	if strings.TrimSpace(out) != filepath.Join(cfgDir, "work.json") {
		t.Fatalf("got %q", out)
	}
	writeTempFile(t, dir, "claude-config", "missing.json")
	out2, _ := runBashFunc(t, "lib/claude-configs.sh", "resolve_claude_config_path",
		[]string{cfgDir, ptr}, nil)
	if strings.TrimSpace(out2) != "" {
		t.Fatalf("expected empty for missing file, got %q", out2)
	}
}

func TestGetClaudeConfigProviderReadsOnlyKnownStringMarkers(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "ChatGPT",
			content: `{"env":{"WISP_DECK_SUBSCRIPTION_PROVIDER":"openai-chatgpt"}}`,
			want:    "openai-chatgpt",
		},
		{
			name:    "known API provider",
			content: `{"env":{"WISP_DECK_SUBSCRIPTION_PROVIDER":"mimo"}}`,
			want:    "mimo",
		},
		{
			name:    "moonshot",
			content: `{"env":{"WISP_DECK_SUBSCRIPTION_PROVIDER":"moonshot"}}`,
			want:    "moonshot",
		},
		{
			// Moonshot's flat-rate coding subscription is a distinct gateway
			// from its open platform; a marker the allowlist drops falls back to
			// display-name matching, which cannot tell the two Kimi products
			// apart and would strand the profile on the gateway that 401s it.
			name:    "moonshot coding",
			content: `{"env":{"WISP_DECK_SUBSCRIPTION_PROVIDER":"moonshot-coding"}}`,
			want:    "moonshot-coding",
		},
		{
			name:    "unknown marker",
			content: `{"env":{"WISP_DECK_SUBSCRIPTION_PROVIDER":"$(touch /tmp/no)"}}`,
		},
		{
			name:    "non-string marker",
			content: `{"env":{"WISP_DECK_SUBSCRIPTION_PROVIDER":["openai-chatgpt"]}}`,
		},
		{name: "malformed JSON", content: `{`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := writeTempFile(t, dir, strings.ReplaceAll(test.name, " ", "-")+".json", test.content)
			out, code := runBashFunc(t, "lib/claude-configs.sh", "get_claude_config_provider",
				[]string{settings}, nil)
			assertExitCode(t, code, 0)
			if got := strings.TrimSpace(out); got != test.want {
				t.Fatalf("provider = %q, want %q", got, test.want)
			}
		})
	}
	out, code := runBashFunc(t, "lib/claude-configs.sh", "get_claude_config_provider",
		[]string{filepath.Join(dir, "missing.json")}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("missing settings provider = %q", out)
	}
}

// get_active_claude_config_name maps the active pointer to its display name so
// the compact-view ledger can show which subscription/plan is in use. Standard
// (no pointer) reads as "Standard Claude", mirroring the menu's PLAN label.
func TestActiveConfigName_standard_when_no_pointer(t *testing.T) {
	dir := t.TempDir()
	ptr := filepath.Join(dir, "claude-config")
	list := filepath.Join(dir, "claude-configs.list")
	writeTempFile(t, dir, "claude-configs.list", "Work:work.json\n")
	out, code := runBashFunc(t, "lib/claude-configs.sh", "get_active_claude_config_name",
		[]string{ptr, list}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "Standard Claude" {
		t.Fatalf("got %q, want %q", strings.TrimSpace(out), "Standard Claude")
	}
}

func TestActiveConfigName_maps_active_file_to_list_name(t *testing.T) {
	dir := t.TempDir()
	ptr := filepath.Join(dir, "claude-config")
	list := filepath.Join(dir, "claude-configs.list")
	writeTempFile(t, dir, "claude-config", "work.json")
	writeTempFile(t, dir, "claude-configs.list", "Work Max:work.json\nPersonal:personal.json\n")
	out, code := runBashFunc(t, "lib/claude-configs.sh", "get_active_claude_config_name",
		[]string{ptr, list}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "Work Max" {
		t.Fatalf("got %q, want %q", strings.TrimSpace(out), "Work Max")
	}
}

func TestActiveConfigName_unknown_file_falls_back_to_standard(t *testing.T) {
	dir := t.TempDir()
	ptr := filepath.Join(dir, "claude-config")
	list := filepath.Join(dir, "claude-configs.list")
	writeTempFile(t, dir, "claude-config", "ghost.json")
	writeTempFile(t, dir, "claude-configs.list", "Work:work.json\n")
	out, code := runBashFunc(t, "lib/claude-configs.sh", "get_active_claude_config_name",
		[]string{ptr, list}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "Standard Claude" {
		t.Fatalf("got %q, want %q", strings.TrimSpace(out), "Standard Claude")
	}
}

// Mutations (add / rename / delete / slugify and collision handling) moved to
// Go — see internal/claudeconfig/claudeconfig_test.go. Only the read/launch
// helpers remain in bash, tested above.
