package bash_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// ============================================================
// TestGhostTab_* — migrated from test/ghost-tab.bats (13 tests)
// ============================================================

// ---------- Section 1: OS Check ----------

func TestGhostTab_OsCheck_rejects_non_Darwin_platform(t *testing.T) {
	root := projectRoot(t)
	script := fmt.Sprintf(`
source %q
uname() { echo "Linux"; }
export -f uname
if [ "$(uname)" != "Darwin" ]; then
  error "This setup script only supports macOS."
  exit 1
fi
`, filepath.Join(root, "lib/tui.sh"))

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 1)
	assertContains(t, out, "macOS")
}

// ---------- Section 2: Supporting Files Validation ----------

func TestGhostTab_SupportingFiles_fails_when_files_missing(t *testing.T) {
	dir := t.TempDir()
	shareDir := filepath.Join(dir, "empty-share")
	if err := os.MkdirAll(shareDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	root := projectRoot(t)
	script := fmt.Sprintf(`
source %q
SHARE_DIR=%q
if [ ! -f "$SHARE_DIR/wrapper.sh" ] || [ ! -d "$SHARE_DIR/templates" ]; then
  error "Supporting files not found in $SHARE_DIR. Re-clone the repository."
  exit 1
fi
`, filepath.Join(root, "lib/tui.sh"), shareDir)

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 1)
	assertContains(t, out, "Re-clone")
}

func TestGhostTab_SupportingFiles_passes_when_all_present(t *testing.T) {
	dir := t.TempDir()
	shareDir := filepath.Join(dir, "full-share")
	os.MkdirAll(filepath.Join(shareDir, "templates"), 0755)
	writeTempFile(t, shareDir, "wrapper.sh", "")

	root := projectRoot(t)
	script := fmt.Sprintf(`
source %q
SHARE_DIR=%q
if [ ! -f "$SHARE_DIR/wrapper.sh" ] || [ ! -d "$SHARE_DIR/templates" ]; then
  error "Supporting files not found in $SHARE_DIR. Re-clone the repository."
  exit 1
fi
echo "ok"
`, filepath.Join(root, "lib/tui.sh"), shareDir)

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "ok" {
		t.Errorf("expected output %q, got %q", "ok", strings.TrimSpace(out))
	}
}

// ---------- Section 3: Config Migration ----------

func TestGhostTab_Migration_renames_vibecode_editor_to_ghost_tab(t *testing.T) {
	dir := t.TempDir()
	configHome := filepath.Join(dir, "config")
	oldDir := filepath.Join(configHome, "vibecode-editor")
	newDir := filepath.Join(configHome, "ghost-tab")
	os.MkdirAll(oldDir, 0755)
	writeTempFile(t, oldDir, "projects", "proj:path")

	script := fmt.Sprintf(`
OLD_PROJECTS_DIR=%q
NEW_PROJECTS_DIR=%q
if [ -d "$OLD_PROJECTS_DIR" ] && [ ! -d "$NEW_PROJECTS_DIR" ]; then
  mv "$OLD_PROJECTS_DIR" "$NEW_PROJECTS_DIR"
fi
cat %q
`, oldDir, newDir, filepath.Join(newDir, "projects"))

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "proj:path" {
		t.Errorf("expected projects content %q, got %q", "proj:path", strings.TrimSpace(out))
	}

	// Verify old dir gone, new dir exists
	if _, err := os.Stat(newDir); os.IsNotExist(err) {
		t.Error("expected ghost-tab dir to exist")
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Error("expected vibecode-editor dir to be gone")
	}
}

func TestGhostTab_Migration_skips_when_ghost_tab_already_exists(t *testing.T) {
	dir := t.TempDir()
	configHome := filepath.Join(dir, "config")
	oldDir := filepath.Join(configHome, "vibecode-editor")
	newDir := filepath.Join(configHome, "ghost-tab")
	os.MkdirAll(oldDir, 0755)
	os.MkdirAll(newDir, 0755)
	writeTempFile(t, oldDir, "projects", "old")
	writeTempFile(t, newDir, "projects", "new")

	script := fmt.Sprintf(`
OLD_PROJECTS_DIR=%q
NEW_PROJECTS_DIR=%q
if [ -d "$OLD_PROJECTS_DIR" ] && [ ! -d "$NEW_PROJECTS_DIR" ]; then
  mv "$OLD_PROJECTS_DIR" "$NEW_PROJECTS_DIR"
fi
cat %q
`, oldDir, newDir, filepath.Join(newDir, "projects"))

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "new" {
		t.Errorf("expected projects content %q, got %q", "new", strings.TrimSpace(out))
	}

	// Both dirs should still exist
	if _, err := os.Stat(oldDir); os.IsNotExist(err) {
		t.Error("expected vibecode-editor dir to still exist")
	}
	if _, err := os.Stat(newDir); os.IsNotExist(err) {
		t.Error("expected ghost-tab dir to still exist")
	}
}

// ---------- Section 4: Ghostty Config ----------

func TestGhostTab_GhosttyConfig_merge_option_adds_command_line(t *testing.T) {
	dir := t.TempDir()
	configFile := writeTempFile(t, dir, "config", "font-size = 14\n")

	root := projectRoot(t)
	// ghostty-config.sh uses success() from tui.sh, so source both
	script := fmt.Sprintf(`
source %q
source %q
merge_ghostty_config %q "command = ~/.config/ghost-tab/wrapper.sh"
`, filepath.Join(root, "lib/tui.sh"), filepath.Join(root, "lib/ghostty-config.sh"), configFile)

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Appended")

	content, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	assertContains(t, string(content), "font-size = 14")
	assertContains(t, string(content), "command = ~/.config/ghost-tab/wrapper.sh")
}

func TestGhostTab_GhosttyConfig_backup_replace_creates_backup(t *testing.T) {
	dir := t.TempDir()
	configFile := writeTempFile(t, dir, "config", "old content\n")
	templateFile := writeTempFile(t, dir, "template", "new template content\n")

	root := projectRoot(t)
	// ghostty-config.sh uses success() from tui.sh, so source both
	script := fmt.Sprintf(`
source %q
source %q
backup_replace_ghostty_config %q %q
`, filepath.Join(root, "lib/tui.sh"), filepath.Join(root, "lib/ghostty-config.sh"), configFile, templateFile)

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Backed up")
	assertContains(t, out, "Replaced")

	// Verify backup exists
	matches, _ := filepath.Glob(filepath.Join(dir, "config.backup.*"))
	if len(matches) != 1 {
		t.Errorf("expected 1 backup file, found %d", len(matches))
	}

	// Verify config matches template
	content, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	if strings.TrimSpace(string(content)) != "new template content" {
		t.Errorf("expected config to be %q, got %q", "new template content", strings.TrimSpace(string(content)))
	}
}

func TestGhostTab_GhosttyConfig_invalid_choice_warns_and_skips(t *testing.T) {
	dir := t.TempDir()
	configFile := writeTempFile(t, dir, "config", "original content\n")

	root := projectRoot(t)
	script := fmt.Sprintf(`
source %q
config_choice="3"
case "$config_choice" in
  1) echo "merge" ;;
  2) echo "backup" ;;
  *) warn "Invalid choice, skipping config setup" ;;
esac
`, filepath.Join(root, "lib/tui.sh"))

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Invalid choice")

	// Config unchanged
	content, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	if strings.TrimSpace(string(content)) != "original content" {
		t.Errorf("expected config unchanged, got %q", strings.TrimSpace(string(content)))
	}
}

func TestGhostTab_GhosttyConfig_creates_new_when_none_exists(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "ghostty-config")
	templateFile := writeTempFile(t, dir, "template-config", "command = ~/.config/ghost-tab/wrapper.sh\n")

	// Verify config doesn't exist yet
	if _, err := os.Stat(configFile); !os.IsNotExist(err) {
		t.Fatal("expected config to not exist initially")
	}

	script := fmt.Sprintf(`cp %q %q && cat %q`, templateFile, configFile, configFile)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)

	// Verify config was created
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		t.Error("expected config file to exist after copy")
	}
	assertContains(t, out, "command = ~/.config/ghost-tab/wrapper.sh")
}

// ---------- Section 5: Project Addition ----------

func TestGhostTab_Projects_writes_entry_to_file(t *testing.T) {
	dir := t.TempDir()
	projectsDir := filepath.Join(dir, "ghost-tab")
	os.MkdirAll(projectsDir, 0755)
	projectsFile := filepath.Join(projectsDir, "projects")

	expandedPath := filepath.Join(dir, "Code/myproject")
	os.MkdirAll(expandedPath, 0755)

	script := fmt.Sprintf(`
proj_name="myproject"
expanded_path=%q
projects_file=%q
if [ -d "$expanded_path" ]; then
  echo "$proj_name:$expanded_path" >> "$projects_file"
fi
cat "$projects_file"
`, expandedPath, projectsFile)

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)

	expected := fmt.Sprintf("myproject:%s", expandedPath)
	if strings.TrimSpace(out) != expected {
		t.Errorf("expected %q, got %q", expected, strings.TrimSpace(out))
	}
}

func TestGhostTab_Projects_adds_nonexistent_path_with_warning(t *testing.T) {
	dir := t.TempDir()
	projectsDir := filepath.Join(dir, "ghost-tab")
	os.MkdirAll(projectsDir, 0755)
	projectsFile := filepath.Join(projectsDir, "projects")

	root := projectRoot(t)
	script := fmt.Sprintf(`
source %q
proj_name="futureproject"
proj_path="/nonexistent/path/futureproject"
expanded_path="/nonexistent/path/futureproject"
projects_file=%q

if [ -d "$expanded_path" ]; then
  echo "$proj_name:$expanded_path" >> "$projects_file"
else
  warn "Path $proj_path does not exist yet — adding anyway"
  echo "$proj_name:$expanded_path" >> "$projects_file"
fi
`, filepath.Join(root, "lib/tui.sh"), projectsFile)

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "does not exist yet")

	// Verify file content
	content, err := os.ReadFile(projectsFile)
	if err != nil {
		t.Fatalf("failed to read projects file: %v", err)
	}
	if strings.TrimSpace(string(content)) != "futureproject:/nonexistent/path/futureproject" {
		t.Errorf("expected %q, got %q", "futureproject:/nonexistent/path/futureproject", strings.TrimSpace(string(content)))
	}
}

// ---------- Section 6: Summary ----------

func TestGhostTab_Summary_shows_all_installed_components(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".claude"), 0755)
	os.MkdirAll(filepath.Join(dir, ".config/ghost-tab"), 0755)

	writeTempFile(t, dir, ".claude/statusline-wrapper.sh", "")
	writeTempFile(t, dir, ".claude/settings.json",
		`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"GHOST_TAB_MARKER_FILE"}]}]}}`)
	writeTempFile(t, dir, ".config/ghost-tab/ai-tool", "claude")

	root := projectRoot(t)
	script := fmt.Sprintf(`
export HOME=%q
source %q
if [ -f "$HOME/.claude/statusline-wrapper.sh" ]; then
  success "Status line:     ~/.claude/statusline-wrapper.sh"
fi
if grep -q "GHOST_TAB_MARKER_FILE" "$HOME/.claude/settings.json" 2>/dev/null; then
  success "Sound:           Waiting indicator hooks"
fi
`, dir, filepath.Join(root, "lib/tui.sh"))

	env := buildEnv(t, nil, fmt.Sprintf("HOME=%s", dir))
	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Status line")
	assertContains(t, out, "Sound")
}

func TestGhostTab_Summary_omits_missing_components(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".claude"), 0755)

	root := projectRoot(t)
	script := fmt.Sprintf(`
export HOME=%q
source %q
if [ -f "$HOME/.claude/statusline-wrapper.sh" ]; then
  success "Status line:     ~/.claude/statusline-wrapper.sh"
fi
if grep -q "GHOST_TAB_MARKER_FILE" "$HOME/.claude/settings.json" 2>/dev/null; then
  success "Sound:           Waiting indicator hooks"
fi
echo "done"
`, dir, filepath.Join(root, "lib/tui.sh"))

	env := buildEnv(t, nil, fmt.Sprintf("HOME=%s", dir))
	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "Status line")
	assertNotContains(t, out, "Sound")
	assertNotContains(t, out, "Tab animation")
}

// ============================================================
// TestGhostTab_Flags_* — argument parsing (terminal selection removed)
// ============================================================

func TestGhostTab_Flags_unknown_flag_shows_usage(t *testing.T) {
	root := projectRoot(t)
	dir := t.TempDir()

	// Mirror bin/ghost-tab's arg-parsing case (Ghostty-only: no terminal flag).
	scriptContent := fmt.Sprintf(`#!/bin/bash
source %q

case "${1:-}" in
  --*)
    error "Unknown flag: $1"
    echo "Usage: ghost-tab"
    exit 1
    ;;
esac
`, filepath.Join(root, "lib/tui.sh"))

	scriptPath := writeTempFile(t, dir, "test-unknown-flag.sh", scriptContent)
	os.Chmod(scriptPath, 0755)

	out, code := runBashSnippet(t, fmt.Sprintf("bash %q --bogus", scriptPath), nil)
	assertExitCode(t, code, 1)
	assertContains(t, out, "Unknown flag")
	assertContains(t, out, "Usage:")
}

// ============================================================
// Terminal-support removal regression tests (Ghostty-only)
// ============================================================

func TestTerminalSupport_removed_files_do_not_exist(t *testing.T) {
	root := projectRoot(t)
	deletedFiles := []string{
		"lib/terminals/iterm2.sh",
		"lib/terminals/wezterm.sh",
		"lib/terminals/kitty.sh",
		"lib/terminals/adapter.sh",
		"lib/terminals/registry.sh",
		"lib/terminal-select-tui.sh",
		"cmd/ghost-tab-tui/select_terminal.go",
		"internal/tui/terminal_selector.go",
		"internal/models/terminal.go",
	}
	for _, f := range deletedFiles {
		t.Run(f, func(t *testing.T) {
			path := filepath.Join(root, f)
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("%s should not exist — only Ghostty is supported now", f)
			}
		})
	}
}

func TestGhostTab_does_not_reference_terminal_selection(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "bin", "ghost-tab"))
	if err != nil {
		t.Fatalf("failed to read bin/ghost-tab: %v", err)
	}
	content := string(data)
	for _, ref := range []string{
		"select_terminal_interactive",
		"load_terminal_adapter",
		"get_terminal_display_name",
		"save_terminal_preference",
		"--terminal",
	} {
		if strings.Contains(content, ref) {
			t.Errorf("bin/ghost-tab still references %q — terminal selection was removed", ref)
		}
	}
}

func TestWrapper_does_not_source_terminal_registry_or_adapter(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "wrapper.sh"))
	if err != nil {
		t.Fatalf("failed to read wrapper.sh: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "terminals/registry") {
		t.Errorf("wrapper.sh still sources terminals/registry — it was removed")
	}
	if strings.Contains(content, "terminals/adapter") {
		t.Errorf("wrapper.sh still sources terminals/adapter — it was removed")
	}
	if !strings.Contains(content, "terminals/ghostty") {
		t.Errorf("wrapper.sh should source terminals/ghostty (the only terminal)")
	}
}

// ============================================================
// TestClaudeWrapper_* — migrated from test/claude-wrapper.bats (7 tests)
// ============================================================

// createWrapperTestEnv sets up the elaborate test environment used by
// claude-wrapper.bats tests. It returns the temp dir, wrapper dir, bin dir,
// and share dir.
func createWrapperTestEnv(t *testing.T) (tmpDir, wrapperDir, binDir, shareDir string) {
	t.Helper()

	tmpDir = t.TempDir()
	wrapperDir = filepath.Join(tmpDir, "wrapper")
	binDir = filepath.Join(tmpDir, "bin")
	shareDir = filepath.Join(tmpDir, "share")

	os.MkdirAll(filepath.Join(wrapperDir, "lib"), 0755)
	os.MkdirAll(binDir, 0755)
	os.MkdirAll(filepath.Join(shareDir, "cmd/ghost-tab-tui"), 0755)

	root := projectRoot(t)

	// Copy real lib files for sourcing
	libFiles := []string{
		"tui.sh", "ai-tools.sh", "projects.sh", "process.sh", "input.sh",
		"update.sh", "menu-tui.sh", "project-actions.sh",
		"tmux-session.sh",
	}
	for _, f := range libFiles {
		src := filepath.Join(root, "lib", f)
		dst := filepath.Join(wrapperDir, "lib", f)
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("failed to read %s: %v", src, err)
		}
		if err := os.WriteFile(dst, data, 0644); err != nil {
			t.Fatalf("failed to write %s: %v", dst, err)
		}
	}

	// Create minimal ghost-tab-tui source for testing builds
	writeTempFile(t, shareDir, "cmd/ghost-tab-tui/main.go", `package main
import "fmt"
func main() {
  fmt.Println("ghost-tab-tui test version")
}
`)

	// Create minimal go.mod at SHARE_DIR root
	writeTempFile(t, shareDir, "go.mod", `module github.com/user/ghost-tab

go 1.21
`)

	// Create wrapper test script
	wrapperScript := fmt.Sprintf(`#!/bin/bash
export PATH="$PATH"

# Self-healing: Check if ghost-tab-tui exists, rebuild if missing
TUI_BIN="$HOME/.local/bin/ghost-tab-tui"
if ! command -v ghost-tab-tui &>/dev/null; then
  # Simple inline rebuild without TUI functions (not loaded yet)
  if command -v go &>/dev/null; then
    printf 'Rebuilding ghost-tab-tui...\n' >&2
    mkdir -p "$HOME/.local/bin"
    # Build from module root with relative path to cmd
    if (cd "$SHARE_DIR" && go build -o "$HOME/.local/bin/ghost-tab-tui" ./cmd/ghost-tab-tui) 2>/dev/null; then
      printf 'ghost-tab-tui rebuilt successfully\n' >&2
      export PATH="$HOME/.local/bin:$PATH"
    else
      printf '\033[31mError:\033[0m Failed to rebuild ghost-tab-tui\n' >&2
      printf 'Run \033[1mghost-tab\033[0m to reinstall.\n' >&2
      printf 'Press any key to exit...\n' >&2
      read -rsn1
      exit 1
    fi
  else
    printf '\033[31mError:\033[0m ghost-tab-tui binary not found and Go not installed\n' >&2
    printf 'Run \033[1mghost-tab\033[0m to reinstall.\n' >&2
    printf 'Press any key to exit...\n' >&2
    read -rsn1
    exit 1
  fi
fi

# Now proceed with normal wrapper startup
_WRAPPER_DIR="$(cd "$(dirname "$0")" && pwd)"

if [ ! -d "$_WRAPPER_DIR/lib" ]; then
  printf '\033[31mError:\033[0m Ghost Tab libraries not found\n' >&2
  exit 1
fi

# Load minimal libs for testing
for _gt_lib in tui; do
  # shellcheck disable=SC1090
  source "$_WRAPPER_DIR/lib/${_gt_lib}.sh"
done

success "Wrapper started successfully"
echo "tui-command: $(command -v ghost-tab-tui)"
`)
	wrapperPath := filepath.Join(wrapperDir, "test-wrapper.sh")
	if err := os.WriteFile(wrapperPath, []byte(wrapperScript), 0755); err != nil {
		t.Fatalf("failed to write wrapper: %v", err)
	}

	return tmpDir, wrapperDir, binDir, shareDir
}

func TestClaudeWrapper_SelfHealing_continues_normally_when_ghost_tab_tui_exists(t *testing.T) {
	_, wrapperDir, binDir, shareDir := createWrapperTestEnv(t)

	// Create a fake ghost-tab-tui binary
	writeTempFile(t, binDir, "ghost-tab-tui", "#!/bin/bash\necho \"ghost-tab-tui v1.0.0\"\n")
	os.Chmod(filepath.Join(binDir, "ghost-tab-tui"), 0755)

	env := buildEnv(t, []string{binDir}, fmt.Sprintf("SHARE_DIR=%s", shareDir))
	out, code := runBashSnippet(t,
		fmt.Sprintf("bash %q", filepath.Join(wrapperDir, "test-wrapper.sh")),
		env,
	)

	assertExitCode(t, code, 0)
	assertContains(t, out, "Wrapper started successfully")
	assertNotContains(t, out, "Rebuilding")
}

func TestClaudeWrapper_SelfHealing_rebuilds_ghost_tab_tui_when_missing_and_Go_available(t *testing.T) {
	// Skip if Go not installed on test system
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go not installed on test system")
	}

	tmpDir, wrapperDir, _, shareDir := createWrapperTestEnv(t)

	// Create isolated environment without ghost-tab-tui
	homeDir := filepath.Join(tmpDir, "home-rebuild")
	os.MkdirAll(filepath.Join(homeDir, ".local/bin"), 0755)

	goPath, _ := exec.LookPath("go")
	goBinDir := filepath.Dir(goPath)

	// Minimal PATH with go but not ghost-tab-tui
	minimalPath := fmt.Sprintf("/usr/local/bin:/usr/bin:/bin:%s", goBinDir)

	env := buildEnv(t, nil,
		fmt.Sprintf("SHARE_DIR=%s", shareDir),
		fmt.Sprintf("HOME=%s", homeDir),
		fmt.Sprintf("PATH=%s", minimalPath),
	)

	out, code := runBashSnippet(t,
		fmt.Sprintf("bash %q", filepath.Join(wrapperDir, "test-wrapper.sh")),
		env,
	)

	assertExitCode(t, code, 0)
	assertContains(t, out, "Rebuilding ghost-tab-tui")
	assertContains(t, out, "ghost-tab-tui rebuilt successfully")
	assertContains(t, out, "Wrapper started successfully")

	// Verify binary was created
	builtBin := filepath.Join(homeDir, ".local/bin/ghost-tab-tui")
	if _, err := os.Stat(builtBin); os.IsNotExist(err) {
		t.Error("expected ghost-tab-tui binary to be created")
	}
}

func TestClaudeWrapper_SelfHealing_fails_gracefully_when_ghost_tab_tui_missing_and_Go_not_installed(t *testing.T) {
	tmpDir, wrapperDir, _, shareDir := createWrapperTestEnv(t)

	homeDir := filepath.Join(tmpDir, "home-no-go")
	os.MkdirAll(filepath.Join(homeDir, ".local/bin"), 0755)

	// Minimal PATH without go or ghost-tab-tui
	env := buildEnv(t, nil,
		fmt.Sprintf("PATH=%s", "/usr/local/bin:/usr/bin:/bin"),
		fmt.Sprintf("SHARE_DIR=%s", shareDir),
		fmt.Sprintf("HOME=%s", homeDir),
	)

	// Pipe empty stdin to satisfy 'read -rsn1'
	script := fmt.Sprintf(`echo '' | bash %q`, filepath.Join(wrapperDir, "test-wrapper.sh"))
	out, code := runBashSnippet(t, script, env)

	assertExitCode(t, code, 1)
	assertContains(t, out, "ghost-tab-tui binary not found and Go not installed")
	assertContains(t, out, "ghost-tab")
	assertContains(t, out, "reinstall")
}

func TestClaudeWrapper_SelfHealing_fails_gracefully_when_rebuild_fails(t *testing.T) {
	// Skip if Go not installed on test system
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go not installed on test system")
	}

	tmpDir, wrapperDir, _, _ := createWrapperTestEnv(t)

	// Create a separate share dir with invalid Go source
	shareBad := filepath.Join(tmpDir, "share-bad")
	os.MkdirAll(filepath.Join(shareBad, "cmd/ghost-tab-tui"), 0755)
	writeTempFile(t, shareBad, "go.mod", `module github.com/user/ghost-tab

go 1.21
`)
	writeTempFile(t, shareBad, "cmd/ghost-tab-tui/main.go", `package main
this is invalid go code!
`)

	homeDir := filepath.Join(tmpDir, "home-bad-build")
	os.MkdirAll(filepath.Join(homeDir, ".local/bin"), 0755)

	goPath, _ := exec.LookPath("go")
	goBinDir := filepath.Dir(goPath)
	minimalPath := fmt.Sprintf("/usr/local/bin:/usr/bin:/bin:%s", goBinDir)

	env := buildEnv(t, nil,
		fmt.Sprintf("SHARE_DIR=%s", shareBad),
		fmt.Sprintf("HOME=%s", homeDir),
		fmt.Sprintf("PATH=%s", minimalPath),
	)

	// Pipe empty stdin to satisfy 'read -rsn1'
	script := fmt.Sprintf(`echo '' | bash %q`, filepath.Join(wrapperDir, "test-wrapper.sh"))
	out, code := runBashSnippet(t, script, env)

	assertExitCode(t, code, 1)
	assertContains(t, out, "Failed to rebuild ghost-tab-tui")
	assertContains(t, out, "ghost-tab")
	assertContains(t, out, "reinstall")
}

func TestClaudeWrapper_SelfHealing_adds_rebuilt_binary_to_PATH(t *testing.T) {
	// Skip if Go not installed on test system
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go not installed on test system")
	}

	tmpDir, wrapperDir, _, shareDir := createWrapperTestEnv(t)

	homeDir := filepath.Join(tmpDir, "home-path-test")
	os.MkdirAll(filepath.Join(homeDir, ".local/bin"), 0755)

	goPath, _ := exec.LookPath("go")
	goBinDir := filepath.Dir(goPath)
	minimalPath := fmt.Sprintf("/usr/local/bin:/usr/bin:/bin:%s", goBinDir)

	env := buildEnv(t, nil,
		fmt.Sprintf("SHARE_DIR=%s", shareDir),
		fmt.Sprintf("HOME=%s", homeDir),
		fmt.Sprintf("PATH=%s", minimalPath),
	)

	out, code := runBashSnippet(t,
		fmt.Sprintf("bash %q", filepath.Join(wrapperDir, "test-wrapper.sh")),
		env,
	)

	assertExitCode(t, code, 0)
	assertContains(t, out, "Rebuilding ghost-tab-tui")

	// Check that the tui-command output contains .local/bin/ghost-tab-tui
	re := regexp.MustCompile(`tui-command:.*\.local/bin/ghost-tab-tui`)
	if !re.MatchString(out) {
		t.Errorf("expected tui-command to contain .local/bin/ghost-tab-tui, got:\n%s", out)
	}
}

func TestClaudeWrapper_SelfHealing_does_not_add_noticeable_latency_when_binary_exists(t *testing.T) {
	_, wrapperDir, binDir, shareDir := createWrapperTestEnv(t)

	// Create a fake ghost-tab-tui binary
	writeTempFile(t, binDir, "ghost-tab-tui", "#!/bin/bash\necho \"ghost-tab-tui v1.0.0\"\n")
	os.Chmod(filepath.Join(binDir, "ghost-tab-tui"), 0755)

	env := buildEnv(t, []string{binDir}, fmt.Sprintf("SHARE_DIR=%s", shareDir))

	start := time.Now()
	out, code := runBashSnippet(t,
		fmt.Sprintf("bash %q", filepath.Join(wrapperDir, "test-wrapper.sh")),
		env,
	)
	elapsed := time.Since(start)

	assertExitCode(t, code, 0)
	assertContains(t, out, "Wrapper started successfully")

	// Should complete in under 2 seconds (very generous)
	if elapsed >= 2*time.Second {
		t.Errorf("expected execution in under 2s, took %v", elapsed)
	}
}

// ============================================================
// TestGhostTab_NativeInstall_* — Task 4: no Homebrew in main flow
// ============================================================

func TestGhostTab_does_not_reference_homebrew_in_main_flow(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "bin", "ghost-tab"))
	if err != nil {
		t.Fatalf("failed to read bin/ghost-tab: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "Homebrew/install/HEAD/install.sh") {
		t.Errorf("bin/ghost-tab still references Homebrew installer URL")
	}
	if strings.Contains(content, "ensure_brew_pkg") {
		t.Errorf("bin/ghost-tab still calls ensure_brew_pkg")
	}
}

func TestWrapper_sources_update_lib(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "wrapper.sh"))
	if err != nil {
		t.Fatalf("failed to read wrapper.sh: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "update.sh") {
		t.Errorf("wrapper.sh does not source update.sh")
	}
}

func TestWrapper_does_not_contain_inline_brew_check(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "wrapper.sh"))
	if err != nil {
		t.Fatalf("failed to read wrapper.sh: %v", err)
	}
	if strings.Contains(string(data), "brew outdated") {
		t.Errorf("wrapper.sh still contains inline brew update check")
	}
}

func TestGhosttyClaudeWrapper_file_does_not_exist_in_repo(t *testing.T) {
	root := projectRoot(t)
	path := filepath.Join(root, "ghostty", "claude-wrapper.sh")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("ghostty/claude-wrapper.sh should not exist — it was the old bash entry point; use wrapper.sh instead")
	}
}

func TestGhosttyConfig_template_uses_new_wrapper_path(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "ghostty", "config"))
	if err != nil {
		t.Fatalf("failed to read ghostty/config: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "claude-wrapper.sh") {
		t.Errorf("ghostty/config template still references claude-wrapper.sh — update to ~/.config/ghost-tab/wrapper.sh")
	}
	if !strings.Contains(content, "ghost-tab/wrapper.sh") {
		t.Errorf("ghostty/config template must use ~/.config/ghost-tab/wrapper.sh as the command")
	}
}

func TestGhostTab_does_not_reference_claude_wrapper_migration(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "bin", "ghost-tab"))
	if err != nil {
		t.Fatalf("failed to read bin/ghost-tab: %v", err)
	}
	if strings.Contains(string(data), "claude-wrapper.sh") {
		t.Errorf("bin/ghost-tab still contains legacy claude-wrapper.sh migration code — remove it")
	}
}

func TestWrapper_uses_go_tui_for_project_selection(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "wrapper.sh"))
	if err != nil {
		t.Fatalf("failed to read wrapper.sh: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "select_project_interactive") {
		t.Errorf("wrapper.sh should use select_project_interactive (Go TUI), not a bash draw_menu")
	}
	if strings.Contains(content, "draw_menu()") {
		t.Errorf("wrapper.sh should not contain a bash draw_menu function — use ghost-tab-tui main-menu instead")
	}
}

func TestClaudeWrapper_PlainTerminal_action_execs_shell_instead_of_exiting(t *testing.T) {
	dir := t.TempDir()

	// Create a marker shell that proves exec happened
	markerShell := writeTempFile(t, dir, "marker-shell.sh", "#!/bin/bash\necho \"SHELL_EXEC_OK\"\n")
	os.Chmod(markerShell, 0755)

	// Build a test script that simulates the plain-terminal action
	testScript := fmt.Sprintf(`#!/bin/bash
# Simulate the action handling from claude-wrapper.sh
_selected_project_action="plain-terminal"

case "$_selected_project_action" in
  plain-terminal)
    exec "$SHELL"
    ;;
esac
echo "SHOULD_NOT_REACH"
`)
	scriptPath := writeTempFile(t, dir, "plain-terminal-test.sh", testScript)
	os.Chmod(scriptPath, 0755)

	env := buildEnv(t, nil, fmt.Sprintf("SHELL=%s", markerShell))
	out, code := runBashSnippet(t, fmt.Sprintf("bash %q", scriptPath), env)

	assertExitCode(t, code, 0)
	assertContains(t, out, "SHELL_EXEC_OK")
	assertNotContains(t, out, "SHOULD_NOT_REACH")
}

// ============================================================
// Settings menu removal regression tests
// ============================================================

func TestSettingsMenu_go_source_files_do_not_exist(t *testing.T) {
	root := projectRoot(t)
	deletedFiles := []string{
		"internal/tui/settings.go",
		"cmd/ghost-tab-tui/settings_menu.go",
		"test/internal/tui/settings_test.go",
		"lib/settings-menu-tui.sh",
	}
	for _, f := range deletedFiles {
		t.Run(f, func(t *testing.T) {
			path := filepath.Join(root, f)
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("%s should not exist — settings menu was removed", f)
			}
		})
	}
}

func TestWrapper_does_not_reference_settings_menu_tui(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "wrapper.sh"))
	if err != nil {
		t.Fatalf("failed to read wrapper.sh: %v", err)
	}
	if strings.Contains(string(data), "settings-menu-tui") {
		t.Errorf("wrapper.sh still references settings-menu-tui — it should be removed")
	}
}
