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
// TestWispDeck_* — migrated from test/wisp-deck.bats (13 tests)
// ============================================================

// ---------- Section 1: OS Check ----------

func TestWispDeck_OsCheck_rejects_non_Darwin_platform(t *testing.T) {
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

// TestWrapper_tab_title_uses_nerd_font_ghost_icon asserts the initial Ghostty
// tab title set by wrapper.sh uses the same nerd-font ghost glyph as the TUI
// header wordmark (iconGhost = "\U000F02A0" in render_projects.go), not the
// emoji 👻, so the tab icon matches the in-app branding.
func TestWrapper_tab_title_uses_nerd_font_ghost_icon(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "wrapper.sh"))
	if err != nil {
		t.Fatalf("failed to read wrapper.sh: %v", err)
	}
	content := string(data)

	const nerdGhost = "\U000F02A0" // nf-md-ghost — must match iconGhost in render_projects.go
	if !strings.Contains(content, nerdGhost+"  Wisp Deck") {
		t.Errorf("wrapper.sh tab title should use the nerd-font ghost glyph %q followed by two spaces before \"Wisp Deck\" (the glyph hugs the text with only one)", nerdGhost)
	}
	if strings.Contains(content, "👻") {
		t.Error("wrapper.sh should not use the 👻 emoji for the tab title; use the nerd-font ghost glyph to match the header")
	}
}

// ---------- Section 2: Supporting Files Validation ----------

func TestWispDeck_SupportingFiles_fails_when_files_missing(t *testing.T) {
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

func TestWispDeck_SupportingFiles_passes_when_all_present(t *testing.T) {
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

func TestWispDeck_Migration_renames_vibecode_editor_to_wisp_deck(t *testing.T) {
	dir := t.TempDir()
	configHome := filepath.Join(dir, "config")
	oldDir := filepath.Join(configHome, "vibecode-editor")
	newDir := filepath.Join(configHome, "wisp-deck")
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
		t.Error("expected wisp-deck dir to exist")
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Error("expected vibecode-editor dir to be gone")
	}
}

func TestWispDeck_Migration_skips_when_wisp_deck_already_exists(t *testing.T) {
	dir := t.TempDir()
	configHome := filepath.Join(dir, "config")
	oldDir := filepath.Join(configHome, "vibecode-editor")
	newDir := filepath.Join(configHome, "wisp-deck")
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
		t.Error("expected wisp-deck dir to still exist")
	}
}

// ---------- Section 4: Ghostty Config ----------

func TestWispDeck_GhosttyConfig_merge_option_adds_command_line(t *testing.T) {
	dir := t.TempDir()
	configFile := writeTempFile(t, dir, "config", "font-size = 14\n")

	root := projectRoot(t)
	// ghostty-config.sh uses success() from tui.sh, so source both
	script := fmt.Sprintf(`
source %q
source %q
merge_ghostty_config %q "command = ~/.config/wisp-deck/wrapper.sh"
`, filepath.Join(root, "lib/tui.sh"), filepath.Join(root, "lib/ghostty-config.sh"), configFile)

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Appended")

	content, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	assertContains(t, string(content), "font-size = 14")
	assertContains(t, string(content), "command = ~/.config/wisp-deck/wrapper.sh")
}

func TestWispDeck_GhosttyConfig_backup_replace_creates_backup(t *testing.T) {
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

func TestWispDeck_GhosttyConfig_invalid_choice_warns_and_skips(t *testing.T) {
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

func TestWispDeck_GhosttyConfig_creates_new_when_none_exists(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "ghostty-config")
	templateFile := writeTempFile(t, dir, "template-config", "command = ~/.config/wisp-deck/wrapper.sh\n")

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
	assertContains(t, out, "command = ~/.config/wisp-deck/wrapper.sh")
}

// ---------- Section 5: Project Addition ----------

func TestWispDeck_Projects_writes_entry_to_file(t *testing.T) {
	dir := t.TempDir()
	projectsDir := filepath.Join(dir, "wisp-deck")
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

func TestWispDeck_Projects_adds_nonexistent_path_with_warning(t *testing.T) {
	dir := t.TempDir()
	projectsDir := filepath.Join(dir, "wisp-deck")
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

func TestWispDeck_Summary_shows_all_installed_components(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".claude"), 0755)
	os.MkdirAll(filepath.Join(dir, ".config/wisp-deck"), 0755)

	writeTempFile(t, dir, ".claude/statusline-wrapper.sh", "")
	writeTempFile(t, dir, ".claude/settings.json",
		`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"WISP_DECK_MARKER_FILE"}]}]}}`)
	writeTempFile(t, dir, ".config/wisp-deck/ai-tool", "claude")

	root := projectRoot(t)
	script := fmt.Sprintf(`
export HOME=%q
source %q
if [ -f "$HOME/.claude/statusline-wrapper.sh" ]; then
  success "Status line:     ~/.claude/statusline-wrapper.sh"
fi
if grep -q "WISP_DECK_MARKER_FILE" "$HOME/.claude/settings.json" 2>/dev/null; then
  success "Sound:           Waiting indicator hooks"
fi
`, dir, filepath.Join(root, "lib/tui.sh"))

	env := buildEnv(t, nil, fmt.Sprintf("HOME=%s", dir))
	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Status line")
	assertContains(t, out, "Sound")
}

func TestWispDeck_Summary_omits_missing_components(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".claude"), 0755)

	root := projectRoot(t)
	script := fmt.Sprintf(`
export HOME=%q
source %q
if [ -f "$HOME/.claude/statusline-wrapper.sh" ]; then
  success "Status line:     ~/.claude/statusline-wrapper.sh"
fi
if grep -q "WISP_DECK_MARKER_FILE" "$HOME/.claude/settings.json" 2>/dev/null; then
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
// TestWispDeck_Flags_* — argument parsing (terminal selection removed)
// ============================================================

func TestWispDeck_Flags_unknown_flag_shows_usage(t *testing.T) {
	root := projectRoot(t)
	dir := t.TempDir()

	// Mirror bin/wisp-deck's arg-parsing case (Ghostty-only: no terminal flag).
	scriptContent := fmt.Sprintf(`#!/bin/bash
source %q

case "${1:-}" in
  --*)
    error "Unknown flag: $1"
    echo "Usage: wisp-deck"
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
		"cmd/wisp-deck-tui/select_terminal.go",
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

func TestWispDeck_does_not_reference_terminal_selection(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "bin", "wisp-deck"))
	if err != nil {
		t.Fatalf("failed to read bin/wisp-deck: %v", err)
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
			t.Errorf("bin/wisp-deck still references %q — terminal selection was removed", ref)
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
	os.MkdirAll(filepath.Join(shareDir, "cmd/wisp-deck-tui"), 0755)

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

	// Create minimal wisp-deck-tui source for testing builds
	writeTempFile(t, shareDir, "cmd/wisp-deck-tui/main.go", `package main
import "fmt"
func main() {
  fmt.Println("wisp-deck-tui test version")
}
`)

	// Create minimal go.mod at SHARE_DIR root
	writeTempFile(t, shareDir, "go.mod", `module github.com/user/wisp-deck

go 1.21
`)

	// Create wrapper test script
	wrapperScript := fmt.Sprintf(`#!/bin/bash
export PATH="$PATH"

# Self-healing: Check if wisp-deck-tui exists, rebuild if missing
TUI_BIN="$HOME/.local/bin/wisp-deck-tui"
if ! command -v wisp-deck-tui &>/dev/null; then
  # Simple inline rebuild without TUI functions (not loaded yet)
  if command -v go &>/dev/null; then
    printf 'Rebuilding wisp-deck-tui...\n' >&2
    mkdir -p "$HOME/.local/bin"
    # Build from module root with relative path to cmd
    if (cd "$SHARE_DIR" && go build -o "$HOME/.local/bin/wisp-deck-tui" ./cmd/wisp-deck-tui) 2>/dev/null; then
      printf 'wisp-deck-tui rebuilt successfully\n' >&2
      export PATH="$HOME/.local/bin:$PATH"
    else
      printf '\033[31mError:\033[0m Failed to rebuild wisp-deck-tui\n' >&2
      printf 'Run \033[1mwisp-deck\033[0m to reinstall.\n' >&2
      printf 'Press any key to exit...\n' >&2
      read -rsn1
      exit 1
    fi
  else
    printf '\033[31mError:\033[0m wisp-deck-tui binary not found and Go not installed\n' >&2
    printf 'Run \033[1mwisp-deck\033[0m to reinstall.\n' >&2
    printf 'Press any key to exit...\n' >&2
    read -rsn1
    exit 1
  fi
fi

# Now proceed with normal wrapper startup
_WRAPPER_DIR="$(cd "$(dirname "$0")" && pwd)"

if [ ! -d "$_WRAPPER_DIR/lib" ]; then
  printf '\033[31mError:\033[0m Wisp Deck libraries not found\n' >&2
  exit 1
fi

# Load minimal libs for testing
for _gt_lib in tui; do
  # shellcheck disable=SC1090
  source "$_WRAPPER_DIR/lib/${_gt_lib}.sh"
done

success "Wrapper started successfully"
echo "tui-command: $(command -v wisp-deck-tui)"
`)
	wrapperPath := filepath.Join(wrapperDir, "test-wrapper.sh")
	if err := os.WriteFile(wrapperPath, []byte(wrapperScript), 0755); err != nil {
		t.Fatalf("failed to write wrapper: %v", err)
	}

	return tmpDir, wrapperDir, binDir, shareDir
}

func TestClaudeWrapper_SelfHealing_continues_normally_when_wisp_deck_tui_exists(t *testing.T) {
	_, wrapperDir, binDir, shareDir := createWrapperTestEnv(t)

	// Create a fake wisp-deck-tui binary
	writeTempFile(t, binDir, "wisp-deck-tui", "#!/bin/bash\necho \"wisp-deck-tui v1.0.0\"\n")
	os.Chmod(filepath.Join(binDir, "wisp-deck-tui"), 0755)

	env := buildEnv(t, []string{binDir}, fmt.Sprintf("SHARE_DIR=%s", shareDir))
	out, code := runBashSnippet(t,
		fmt.Sprintf("bash %q", filepath.Join(wrapperDir, "test-wrapper.sh")),
		env,
	)

	assertExitCode(t, code, 0)
	assertContains(t, out, "Wrapper started successfully")
	assertNotContains(t, out, "Rebuilding")
}

func TestClaudeWrapper_SelfHealing_rebuilds_wisp_deck_tui_when_missing_and_Go_available(t *testing.T) {
	// Skip if Go not installed on test system
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go not installed on test system")
	}

	tmpDir, wrapperDir, _, shareDir := createWrapperTestEnv(t)

	// Create isolated environment without wisp-deck-tui
	homeDir := filepath.Join(tmpDir, "home-rebuild")
	os.MkdirAll(filepath.Join(homeDir, ".local/bin"), 0755)

	goPath, _ := exec.LookPath("go")
	goBinDir := filepath.Dir(goPath)

	// Minimal PATH with go but not wisp-deck-tui
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
	assertContains(t, out, "Rebuilding wisp-deck-tui")
	assertContains(t, out, "wisp-deck-tui rebuilt successfully")
	assertContains(t, out, "Wrapper started successfully")

	// Verify binary was created
	builtBin := filepath.Join(homeDir, ".local/bin/wisp-deck-tui")
	if _, err := os.Stat(builtBin); os.IsNotExist(err) {
		t.Error("expected wisp-deck-tui binary to be created")
	}
}

func TestClaudeWrapper_SelfHealing_fails_gracefully_when_wisp_deck_tui_missing_and_Go_not_installed(t *testing.T) {
	tmpDir, wrapperDir, _, shareDir := createWrapperTestEnv(t)

	homeDir := filepath.Join(tmpDir, "home-no-go")
	os.MkdirAll(filepath.Join(homeDir, ".local/bin"), 0755)

	// Minimal PATH without go or wisp-deck-tui
	env := buildEnv(t, nil,
		fmt.Sprintf("PATH=%s", "/usr/local/bin:/usr/bin:/bin"),
		fmt.Sprintf("SHARE_DIR=%s", shareDir),
		fmt.Sprintf("HOME=%s", homeDir),
	)

	// Pipe empty stdin to satisfy 'read -rsn1'
	script := fmt.Sprintf(`echo '' | bash %q`, filepath.Join(wrapperDir, "test-wrapper.sh"))
	out, code := runBashSnippet(t, script, env)

	assertExitCode(t, code, 1)
	assertContains(t, out, "wisp-deck-tui binary not found and Go not installed")
	assertContains(t, out, "wisp-deck")
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
	os.MkdirAll(filepath.Join(shareBad, "cmd/wisp-deck-tui"), 0755)
	writeTempFile(t, shareBad, "go.mod", `module github.com/user/wisp-deck

go 1.21
`)
	writeTempFile(t, shareBad, "cmd/wisp-deck-tui/main.go", `package main
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
	assertContains(t, out, "Failed to rebuild wisp-deck-tui")
	assertContains(t, out, "wisp-deck")
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
	assertContains(t, out, "Rebuilding wisp-deck-tui")

	// Check that the tui-command output contains .local/bin/wisp-deck-tui
	re := regexp.MustCompile(`tui-command:.*\.local/bin/wisp-deck-tui`)
	if !re.MatchString(out) {
		t.Errorf("expected tui-command to contain .local/bin/wisp-deck-tui, got:\n%s", out)
	}
}

func TestClaudeWrapper_SelfHealing_does_not_add_noticeable_latency_when_binary_exists(t *testing.T) {
	_, wrapperDir, binDir, shareDir := createWrapperTestEnv(t)

	// Create a fake wisp-deck-tui binary
	writeTempFile(t, binDir, "wisp-deck-tui", "#!/bin/bash\necho \"wisp-deck-tui v1.0.0\"\n")
	os.Chmod(filepath.Join(binDir, "wisp-deck-tui"), 0755)

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
// TestWispDeck_NativeInstall_* — Task 4: no Homebrew in main flow
// ============================================================

func TestWispDeck_does_not_reference_homebrew_in_main_flow(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "bin", "wisp-deck"))
	if err != nil {
		t.Fatalf("failed to read bin/wisp-deck: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "Homebrew/install/HEAD/install.sh") {
		t.Errorf("bin/wisp-deck still references Homebrew installer URL")
	}
	if strings.Contains(content, "ensure_brew_pkg") {
		t.Errorf("bin/wisp-deck still calls ensure_brew_pkg")
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

// The reference Ghostty config templates must NOT ship an active bare/tilde
// command line. A bare path (or a "~/..." path, which the exec'd bash never
// expands) breaks with "failed to launch the wrapper" on Ghostty 1.2.x. The
// installer writes the correct "command = direct:/bin/bash -l <absolute>" line
// dynamically, so a static template can only ever be a broken footgun.
func TestGhosttyConfig_templates_ship_no_broken_command_line(t *testing.T) {
	root := projectRoot(t)
	for _, rel := range []string{"ghostty/config", "terminals/ghostty/config"} {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				t.Fatalf("failed to read %s: %v", rel, err)
			}
			for _, line := range strings.Split(string(data), "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "#") {
					continue // comments are documentation, not active config
				}
				if !strings.HasPrefix(trimmed, "command") {
					continue
				}
				// An active command line is only acceptable in the direct: form.
				if !strings.Contains(trimmed, "direct:/bin/bash -l") {
					t.Errorf("%s ships a broken command line %q — bare/tilde paths fail to launch on Ghostty 1.2.x; drop it or use the direct: form", rel, trimmed)
				}
			}
			if strings.Contains(string(data), "claude-wrapper.sh") {
				t.Errorf("%s still references claude-wrapper.sh — the old entry point", rel)
			}
		})
	}
}

func TestWispDeck_does_not_reference_claude_wrapper_migration(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "bin", "wisp-deck"))
	if err != nil {
		t.Fatalf("failed to read bin/wisp-deck: %v", err)
	}
	if strings.Contains(string(data), "claude-wrapper.sh") {
		t.Errorf("bin/wisp-deck still contains legacy claude-wrapper.sh migration code — remove it")
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
		t.Errorf("wrapper.sh should not contain a bash draw_menu function — use wisp-deck-tui main-menu instead")
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

// TestWispDeck_does_not_source_deleted_project_actions_tui guards against the
// regression where bin/wisp-deck kept `source lib/project-actions-tui.sh` after
// that file was deleted (commit 985e8e3). With `set -e`, sourcing the missing
// file aborts the installer with "No such file or directory", breaking
// `npx wisp-deck`.
func TestWispDeck_does_not_source_deleted_project_actions_tui(t *testing.T) {
	root := projectRoot(t)

	// The file must not exist in the repo/distribution.
	if _, err := os.Stat(filepath.Join(root, "lib/project-actions-tui.sh")); !os.IsNotExist(err) {
		t.Errorf("lib/project-actions-tui.sh should not exist — it was deleted")
	}

	// bin/wisp-deck must not try to source it.
	data, err := os.ReadFile(filepath.Join(root, "bin", "wisp-deck"))
	if err != nil {
		t.Fatalf("failed to read bin/wisp-deck: %v", err)
	}
	if strings.Contains(string(data), "project-actions-tui") {
		t.Errorf("bin/wisp-deck still sources project-actions-tui.sh — the file was deleted, so sourcing it breaks the installer")
	}
	// add_project_interactive lived in the deleted file; calling it would fail
	// at runtime with "command not found". Project adding now happens at runtime
	// via the main-menu TUI.
	if strings.Contains(string(data), "add_project_interactive") {
		t.Errorf("bin/wisp-deck still calls add_project_interactive — that function was defined in the deleted project-actions-tui.sh")
	}
}

// ============================================================
// Settings menu removal regression tests
// ============================================================

func TestSettingsMenu_go_source_files_do_not_exist(t *testing.T) {
	root := projectRoot(t)
	deletedFiles := []string{
		"internal/tui/settings.go",
		"cmd/wisp-deck-tui/settings_menu.go",
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
