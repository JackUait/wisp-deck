package bash_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ---------- menu-tui.sh tests (TestMenu_*) ----------

func TestMenu_selects_project_and_parses_JSON(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "wisp-deck-tui", `echo '{"action":"select-project","name":"proj1","path":"/tmp/p1","ai_tool":"claude"}'`)
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")
	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q
echo "name=$_selected_project_name"
echo "path=$_selected_project_path"
echo "action=$_selected_project_action"
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "name=proj1")
	assertContains(t, out, "path=/tmp/p1")
	assertContains(t, out, "action=select-project")
}

func TestMenu_passes_correct_flags_to_main_menu(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "captured_args")
	binDir := mockCommand(t, dir, "wisp-deck-tui", fmt.Sprintf(`
echo "$*" > %q
echo '{"action":"quit"}'
`, argsFile))
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")

	configDir := filepath.Join(dir, "config", "wisp-deck")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, dir, "config/wisp-deck/settings", "ghost_display=static\n")

	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude" "opencode")
SELECTED_AI_TOOL="opencode"
_update_version="2.0.0"
select_project_interactive %q || true
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	_, _ = runBashSnippet(t, script, env)

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("failed to read captured args: %v", err)
	}
	args := string(data)
	assertContains(t, args, "main-menu")
	assertContains(t, args, "--projects-file")
	assertContains(t, args, "--ai-tool")
	assertContains(t, args, "opencode")
	assertContains(t, args, "--ai-tools")
	assertContains(t, args, "claude,opencode")
	assertContains(t, args, "--ghost-display")
	assertContains(t, args, "static")
	assertContains(t, args, "--update-version")
	assertContains(t, args, "2.0.0")
}

func TestMenu_handles_AI_tool_change(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "wisp-deck-tui", `echo '{"action":"select-project","name":"proj1","path":"/tmp/p1","ai_tool":"opencode"}'`)
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")
	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude" "opencode")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q
echo "ai_tool=$_selected_ai_tool"
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "ai_tool=opencode")
}

func TestMenu_handles_quit_action(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "wisp-deck-tui", `echo '{"action":"quit"}'`)
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")
	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	_, code := runBashSnippet(t, script, env)
	if code == 0 {
		t.Errorf("expected non-zero exit code for quit, got 0")
	}
}

func TestMenu_handles_open_once_action(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "wisp-deck-tui", `echo '{"action":"open-once","name":"temp","path":"/tmp/temp","ai_tool":"claude"}'`)
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")
	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q
echo "action=$_selected_project_action"
echo "name=$_selected_project_name"
echo "path=$_selected_project_path"
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "action=open-once")
	assertContains(t, out, "name=temp")
	assertContains(t, out, "path=/tmp/temp")
}

func TestMenu_handles_plain_terminal_action(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "wisp-deck-tui", `echo '{"action":"plain-terminal","ai_tool":"claude"}'`)
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")
	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q
echo "action=$_selected_project_action"
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "action=plain-terminal")
}

func TestMenu_handles_settings_action(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "wisp-deck-tui", `echo '{"action":"settings","ai_tool":"claude"}'`)
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")
	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q
echo "action=$_selected_project_action"
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "action=settings")
}

func TestMenu_reads_ghost_display_from_settings(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "captured_args")
	binDir := mockCommand(t, dir, "wisp-deck-tui", fmt.Sprintf(`
echo "$*" > %q
echo '{"action":"quit"}'
`, argsFile))
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")

	writeTempFile(t, dir, "config/wisp-deck/settings", "ghost_display=none\n")

	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q || true
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	_, _ = runBashSnippet(t, script, env)

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("args file not found: %v", err)
	}
	args := string(data)
	assertContains(t, args, "--ghost-display")
	assertContains(t, args, "none")
}

func TestMenu_defaults_ghost_display_to_animated(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "captured_args")
	binDir := mockCommand(t, dir, "wisp-deck-tui", fmt.Sprintf(`
echo "$*" > %q
echo '{"action":"quit"}'
`, argsFile))
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")
	// No settings file

	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q || true
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	_, _ = runBashSnippet(t, script, env)

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("args file not found: %v", err)
	}
	args := string(data)
	assertContains(t, args, "--ghost-display")
	assertContains(t, args, "animated")
}

func TestMenu_defaults_usage_bars_to_7d(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "captured_args")
	binDir := mockCommand(t, dir, "wisp-deck-tui", fmt.Sprintf(`
echo "$*" > %q
echo '{"action":"quit"}'
`, argsFile))
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")
	// No settings file

	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q || true
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	_, _ = runBashSnippet(t, script, env)

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("args file not found: %v", err)
	}
	assertContains(t, string(data), "--usage-bars 7d")
}

func TestMenu_reads_usage_bars_from_settings(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "captured_args")
	binDir := mockCommand(t, dir, "wisp-deck-tui", fmt.Sprintf(`
echo "$*" > %q
echo '{"action":"quit"}'
`, argsFile))
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")

	writeTempFile(t, dir, "config/wisp-deck/settings", "usage_bars=both\n")

	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q || true
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	_, _ = runBashSnippet(t, script, env)

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("args file not found: %v", err)
	}
	assertContains(t, string(data), "--usage-bars both")
}

func TestMenu_validates_null_name_on_select_project(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "wisp-deck-tui", `echo '{"action":"select-project","name":null,"path":"/tmp/p1","ai_tool":"claude"}'`)
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")
	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	out, code := runBashSnippet(t, script, env)
	if code == 0 {
		t.Error("expected non-zero exit code for null name")
	}
	assertContains(t, out, "invalid project name")
}

func TestMenu_validates_null_path_on_select_project(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "wisp-deck-tui", `echo '{"action":"select-project","name":"proj1","path":null,"ai_tool":"claude"}'`)
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")
	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	out, code := runBashSnippet(t, script, env)
	if code == 0 {
		t.Error("expected non-zero exit code for null path")
	}
	assertContains(t, out, "invalid project path")
}

func TestMenu_handles_jq_parse_failure(t *testing.T) {
	dir := t.TempDir()
	// wisp-deck-tui outputs invalid JSON
	binDir := mockCommand(t, dir, "wisp-deck-tui", `echo 'not json at all'`)
	// Mock jq to fail
	mockCommand(t, dir, "jq", `cat > /dev/null; exit 1`)
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")
	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	out, code := runBashSnippet(t, script, env)
	if code == 0 {
		t.Error("expected non-zero exit code for jq parse failure")
	}
	assertContains(t, out, "Failed to parse")
}

func TestMenu_handles_binary_missing(t *testing.T) {
	dir := t.TempDir()
	// Don't put wisp-deck-tui in PATH at all
	// Create a bin dir with no wisp-deck-tui
	binDir := filepath.Join(dir, "emptybin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")
	root := projectRoot(t)

	// Use a PATH that does NOT include wisp-deck-tui, but does include jq, bash, etc.
	env := buildEnv(t, nil,
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
		// Override PATH to remove any real wisp-deck-tui but keep system commands
		"PATH="+binDir+":/usr/bin:/bin:/usr/sbin:/sbin",
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	out, code := runBashSnippet(t, script, env)
	if code == 0 {
		t.Error("expected non-zero exit code for missing binary")
	}
	assertContains(t, out, "wisp-deck-tui binary not found")
}

func TestMenu_handles_wisp_deck_tui_failure(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "wisp-deck-tui", `exit 1`)
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")
	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	_, code := runBashSnippet(t, script, env)
	if code == 0 {
		t.Error("expected non-zero exit code for wisp-deck-tui failure")
	}
}

func TestMenu_omits_update_version_flag_when_empty(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "captured_args")
	binDir := mockCommand(t, dir, "wisp-deck-tui", fmt.Sprintf(`
echo "$*" > %q
echo '{"action":"quit"}'
`, argsFile))
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")
	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q || true
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	_, _ = runBashSnippet(t, script, env)

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("args file not found: %v", err)
	}
	assertNotContains(t, string(data), "--update-version")
}

func TestMenu_reads_tab_title_from_settings(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "captured_args")
	binDir := mockCommand(t, dir, "wisp-deck-tui", fmt.Sprintf(`
echo "$*" > %q
echo '{"action":"quit"}'
`, argsFile))
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")

	writeTempFile(t, dir, "config/wisp-deck/settings", "tab_title=project\n")

	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q || true
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	_, _ = runBashSnippet(t, script, env)

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("args file not found: %v", err)
	}
	assertContains(t, string(data), "--tab-title project")
}

func TestMenu_defaults_tab_title_to_full(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "captured_args")
	binDir := mockCommand(t, dir, "wisp-deck-tui", fmt.Sprintf(`
echo "$*" > %q
echo '{"action":"quit"}'
`, argsFile))
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")
	// No settings file

	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q || true
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	_, _ = runBashSnippet(t, script, env)

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("args file not found: %v", err)
	}
	args := string(data)
	assertContains(t, args, "--tab-title")
	assertContains(t, args, "full")
}

func TestMenu_persists_ai_tool_change_to_file(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "wisp-deck-tui", `echo '{"action":"select-project","name":"proj1","path":"/tmp/p1","ai_tool":"opencode"}'`)
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")

	configDir := filepath.Join(dir, "config", "wisp-deck")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}

	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude" "opencode")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	_, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)

	aiToolFile := filepath.Join(dir, "config", "wisp-deck", "ai-tool")
	data, err := os.ReadFile(aiToolFile)
	if err != nil {
		t.Fatalf("ai-tool file not created: %v", err)
	}
	if strings.TrimSpace(string(data)) != "opencode" {
		t.Errorf("ai-tool file content = %q, want %q", strings.TrimSpace(string(data)), "opencode")
	}
}

func TestMenu_does_not_write_ai_tool_file_when_unchanged(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "wisp-deck-tui", `echo '{"action":"select-project","name":"proj1","path":"/tmp/p1","ai_tool":"claude"}'`)
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")

	configDir := filepath.Join(dir, "config", "wisp-deck")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}

	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	_, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)

	aiToolFile := filepath.Join(dir, "config", "wisp-deck", "ai-tool")
	if _, err := os.Stat(aiToolFile); err == nil {
		t.Error("ai-tool file should not exist when tool is unchanged")
	}
}

func TestMenu_sets_selected_ai_tool_for_settings_action(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "wisp-deck-tui", `echo '{"action":"settings","ai_tool":"opencode"}'`)
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")
	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude" "opencode")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q
echo "ai_tool=$_selected_ai_tool"
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "ai_tool=opencode")
}

func TestMenu_ai_tool_persists_between_sessions(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config", "wisp-deck")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}

	root := projectRoot(t)
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")

	// Session 1: user cycles to opencode and selects a project
	binDir := mockCommand(t, dir, "wisp-deck-tui", `echo '{"action":"select-project","name":"proj1","path":"/tmp/p1","ai_tool":"opencode"}'`)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script1 := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude" "opencode" "opencode")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	_, code := runBashSnippet(t, script1, env)
	assertExitCode(t, code, 0)

	// Verify file was written
	aiToolFile := filepath.Join(dir, "config", "wisp-deck", "ai-tool")
	data, err := os.ReadFile(aiToolFile)
	if err != nil {
		t.Fatalf("ai-tool file not created: %v", err)
	}
	if strings.TrimSpace(string(data)) != "opencode" {
		t.Errorf("ai-tool file = %q, want %q", strings.TrimSpace(string(data)), "opencode")
	}

	// Session 2: simulate wrapper reading the file and passing to TUI
	argsFile := filepath.Join(dir, "captured_args")
	// Recreate the mock to capture args this time
	binDir2 := filepath.Join(dir, "bin2")
	if err := os.MkdirAll(binDir2, 0755); err != nil {
		t.Fatal(err)
	}
	mockScript := fmt.Sprintf(`#!/bin/bash
# Capture the --ai-tool flag
ai_flag=""
while [[ $# -gt 0 ]]; do
  if [[ "$1" == "--ai-tool" ]]; then
    ai_flag="$2"
    break
  fi
  shift
done
echo "$ai_flag" > %q
echo "{\"action\":\"select-project\",\"name\":\"proj1\",\"path\":\"/tmp/p1\",\"ai_tool\":\"$ai_flag\"}"
`, argsFile)
	if err := os.WriteFile(filepath.Join(binDir2, "wisp-deck-tui"), []byte(mockScript), 0755); err != nil {
		t.Fatal(err)
	}

	env2 := buildEnv(t, []string{binDir2},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script2 := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude" "opencode" "opencode")
# Read the saved preference like the wrapper does
AI_TOOL_PREF_FILE="%s"
SELECTED_AI_TOOL=""
if [ -f "$AI_TOOL_PREF_FILE" ]; then
  SELECTED_AI_TOOL="$(cat "$AI_TOOL_PREF_FILE" 2>/dev/null | tr -d '[:space:]')"
fi
validate_ai_tool
_update_version=""
select_project_interactive %q
echo "ai_tool=$_selected_ai_tool"
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/ai-tools.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		aiToolFile,
		projectsFile)

	out2, code2 := runBashSnippet(t, script2, env2)
	assertExitCode(t, code2, 0)

	// Verify the TUI received "opencode"
	capturedData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("captured args not found: %v", err)
	}
	if strings.TrimSpace(string(capturedData)) != "opencode" {
		t.Errorf("captured ai_tool = %q, want %q", strings.TrimSpace(string(capturedData)), "opencode")
	}

	assertContains(t, out2, "ai_tool=opencode")

	// File should still have opencode
	finalData, err := os.ReadFile(aiToolFile)
	if err != nil {
		t.Fatalf("ai-tool file not found: %v", err)
	}
	if strings.TrimSpace(string(finalData)) != "opencode" {
		t.Errorf("ai-tool file = %q, want %q", strings.TrimSpace(string(finalData)), "opencode")
	}
}

func TestMenu_persists_ai_tool_on_quit(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "wisp-deck-tui", `echo '{"action":"quit","ai_tool":"opencode"}'`)
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")

	// Pre-set ai-tool file to "claude"
	writeTempFile(t, dir, "config/wisp-deck/ai-tool", "claude")

	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude" "opencode")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q || true
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	_, _ = runBashSnippet(t, script, env)

	aiToolFile := filepath.Join(dir, "config", "wisp-deck", "ai-tool")
	data, err := os.ReadFile(aiToolFile)
	if err != nil {
		t.Fatalf("ai-tool file not found: %v", err)
	}
	if strings.TrimSpace(string(data)) != "opencode" {
		t.Errorf("ai-tool should be 'opencode' after quit with tool change, got %q", strings.TrimSpace(string(data)))
	}
}

// The sound preference lives in a JSON document whose strict bash reader
// spawns python3 — too expensive for the launch critical path. The menu
// therefore only forwards --sound-file and the Go binary reads the document
// itself; bash must never pre-read the preference before the picker.
func TestMenu_forwards_sound_file_without_prereading_preference(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "captured_args")
	calledFile := filepath.Join(dir, "get_sound_name_called")
	binDir := mockCommand(t, dir, "wisp-deck-tui", fmt.Sprintf(`
echo "$*" > %q
echo '{"action":"quit"}'
`, argsFile))
	projectsFile := writeTempFile(t, dir, "projects", "test:/tmp/test\n")
	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
_update_version=""
# If the menu pre-reads the sound preference it re-enters the critical path.
get_sound_name() { touch %q; echo "Bottle"; }
select_project_interactive %q || true
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		calledFile,
		projectsFile)

	_, _ = runBashSnippet(t, script, env)

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("args file not found: %v", err)
	}
	assertContains(t, string(data), "--sound-file")
	assertNotContains(t, string(data), "--sound-name")
	assertNotContains(t, string(data), "--sound-enabled")
	if _, err := os.Stat(calledFile); err == nil {
		t.Error("select_project_interactive called get_sound_name; the sound preference must be read by the Go binary, not on the bash launch path")
	}
}

func TestMenu_passes_settings_file_flag(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "captured_args")
	binDir := mockCommand(t, dir, "wisp-deck-tui", fmt.Sprintf(`
echo "$*" > %q
echo '{"action":"quit"}'
`, argsFile))
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")
	settingsDir := filepath.Join(dir, "config", "wisp-deck")
	os.MkdirAll(settingsDir, 0755)
	settingsFile := writeTempFile(t, settingsDir, "settings", "ghost_display=animated\n")
	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
_update_version=""
get_sound_name() { echo ""; }
select_project_interactive %q
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	runBashSnippet(t, script, env)

	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("args file not found: %v", err)
	}
	assertContains(t, string(args), "--settings-file")
	assertContains(t, string(args), settingsFile)
}

func TestMenu_passes_sound_file_flag(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "captured_args")
	binDir := mockCommand(t, dir, "wisp-deck-tui", fmt.Sprintf(`
echo "$*" > %q
echo '{"action":"quit"}'
`, argsFile))
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")
	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
_update_version=""
get_sound_name() { echo "Bottle"; }
select_project_interactive %q
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	runBashSnippet(t, script, env)

	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("args file not found: %v", err)
	}
	assertContains(t, string(args), "--sound-file")
	assertContains(t, string(args), "claude-features.json")
}

// ---------- ai-tools.sh validate_ai_tool tests (TestAITools_*) ----------

func TestAITools_validate_persists_fallback_to_file(t *testing.T) {
	dir := t.TempDir()
	// ai-tool file has "opencode" but opencode is not available
	writeTempFile(t, dir, "config/wisp-deck/ai-tool", "opencode")
	aiToolFile := filepath.Join(dir, "config", "wisp-deck", "ai-tool")

	root := projectRoot(t)
	env := buildEnv(t, nil,
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="opencode"
validate_ai_tool %q
echo "tool=$SELECTED_AI_TOOL"
`, filepath.Join(root, "lib/ai-tools.sh"),
		aiToolFile)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "tool=claude")

	// File should be updated to "claude"
	data, err := os.ReadFile(aiToolFile)
	if err != nil {
		t.Fatalf("ai-tool file not found: %v", err)
	}
	if strings.TrimSpace(string(data)) != "claude" {
		t.Errorf("ai-tool file should be 'claude' after fallback, got %q", strings.TrimSpace(string(data)))
	}
}

func TestAITools_validate_does_not_write_when_valid(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config", "wisp-deck")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	aiToolFile := filepath.Join(configDir, "ai-tool")

	root := projectRoot(t)
	env := buildEnv(t, nil,
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q
AI_TOOLS_AVAILABLE=("claude" "opencode")
SELECTED_AI_TOOL="opencode"
validate_ai_tool %q
echo "tool=$SELECTED_AI_TOOL"
`, filepath.Join(root, "lib/ai-tools.sh"),
		aiToolFile)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "tool=opencode")

	// File should NOT be created (tool was valid, no change needed)
	if _, err := os.Stat(aiToolFile); err == nil {
		t.Error("ai-tool file should not be created when tool is valid")
	}
}

func TestAITools_validate_without_file_arg_does_not_write(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config", "wisp-deck")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	aiToolFile := filepath.Join(configDir, "ai-tool")

	root := projectRoot(t)
	env := buildEnv(t, nil,
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	script := fmt.Sprintf(`
source %q
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="opencode"
validate_ai_tool
echo "tool=$SELECTED_AI_TOOL"
`, filepath.Join(root, "lib/ai-tools.sh"))

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "tool=claude")

	// File should NOT be created (no file arg passed)
	if _, err := os.Stat(aiToolFile); err == nil {
		t.Error("ai-tool file should not be created when no file arg passed")
	}
}

func TestMenu_add_worktree_from_bash_is_unreachable(t *testing.T) {
	// After the Go-level change, main-menu should never return add-worktree.
	// Worktree creation is now handled entirely within the Go TUI (MainMenuModel).
	// This test documents the expected JSON interface — add-worktree should
	// not appear as an action from main-menu anymore.
	t.Log("add-worktree is now handled entirely in Go; bash never receives this action")
}

// select_project_interactive runs in the latency-critical path between the menu
// closing and the project session opening. Each jq spawn costs ~25ms; the
// result must be parsed with a single jq invocation, not one per field.
func TestMenu_parses_result_with_single_jq_invocation(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "wisp-deck-tui", `echo '{"action":"select-project","name":"proj1","path":"/tmp/p1","ai_tool":"claude"}'`)
	realJq, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("jq not installed")
	}
	// Counting jq shim that delegates to the real binary.
	mockCommand(t, dir, "jq", fmt.Sprintf(`echo x >> "$GT_JQ_COUNT"; exec %q "$@"`, realJq))
	countFile := filepath.Join(dir, "jq-count")
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")
	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
		"GT_JQ_COUNT="+countFile,
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q
echo "name=$_selected_project_name"
echo "path=$_selected_project_path"
echo "action=$_selected_project_action"
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "name=proj1")
	assertContains(t, out, "path=/tmp/p1")
	assertContains(t, out, "action=select-project")

	data, _ := os.ReadFile(countFile)
	if n := strings.Count(string(data), "x"); n != 1 {
		t.Errorf("expected exactly 1 jq invocation, got %d", n)
	}
}

// The menu wrapper reads the update flag itself (via get_update_version) so a
// pending update reaches the TUI without any caller having to thread it in.
func TestMenu_auto_populates_update_version_from_flag(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "captured_args")
	binDir := mockCommand(t, dir, "wisp-deck-tui", fmt.Sprintf(`
echo "$*" > %q
echo '{"action":"quit"}'
`, argsFile))
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")

	configDir := filepath.Join(dir, "config", "wisp-deck")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, configDir, "update-available", "2.9.9")
	installDir := filepath.Join(dir, "install")
	if err := os.MkdirAll(installDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, installDir, ".version", "2.6.0")

	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
		"WISP_DECK_INSTALL_DIR="+installDir,
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
select_project_interactive %q || true
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/update.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	_, _ = runBashSnippet(t, script, env)

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("failed to read captured args: %v", err)
	}
	assertContains(t, string(data), "--update-version")
	assertContains(t, string(data), "2.9.9")
}

// A stale flag matching the installed version must NOT produce the flag.
func TestMenu_skips_update_version_when_flag_matches_installed(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "captured_args")
	binDir := mockCommand(t, dir, "wisp-deck-tui", fmt.Sprintf(`
echo "$*" > %q
echo '{"action":"quit"}'
`, argsFile))
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")

	configDir := filepath.Join(dir, "config", "wisp-deck")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, configDir, "update-available", "2.6.0")
	installDir := filepath.Join(dir, "install")
	if err := os.MkdirAll(installDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, installDir, ".version", "2.6.0")

	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
		"WISP_DECK_INSTALL_DIR="+installDir,
	)

	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
select_project_interactive %q || true
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/update.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)

	_, _ = runBashSnippet(t, script, env)

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("failed to read captured args: %v", err)
	}
	assertNotContains(t, string(data), "--update-version")
}
