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

// Mutations (add / rename / delete / slugify and collision handling) moved to
// Go — see internal/claudeconfig/claudeconfig_test.go. Only the read/launch
// helpers remain in bash, tested above.
