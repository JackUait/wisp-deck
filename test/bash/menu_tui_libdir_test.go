package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The AI-tools settings panel installs a tool by sourcing lib/install.sh, so the
// menu needs to be told where lib/ lives. menu-tui.sh must pass --lib-dir.
func TestMenuTui_passes_lib_dir_to_the_main_menu(t *testing.T) {
	dir := t.TempDir()
	argLog := filepath.Join(dir, "args")
	// menu-tui.sh runs the binary with 2>/dev/null, so record args to a file.
	binDir := mockCommand(t, dir, "wisp-deck-tui",
		fmt.Sprintf(`printf '%%s\n' "$@" > %q; echo '{"selected":false}'`, argLog))
	mockCommand(t, dir, "jq", `echo "false"`)
	env := buildEnv(t, []string{binDir}, "XDG_CONFIG_HOME="+dir)
	writeTempFile(t, dir, "projects", "app:/tmp/app\n")

	runBashSnippet(t, `
error(){ :; }
AI_TOOLS_AVAILABLE=(claude)
source `+projectRoot(t)+`/lib/menu-tui.sh
select_project_interactive `+dir+`/projects || true
`, env)

	raw, err := os.ReadFile(argLog)
	if err != nil {
		t.Fatalf("wisp-deck-tui was never invoked: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(raw)), "\n")

	var libDir string
	for i, a := range args {
		if a == "--lib-dir" && i+1 < len(args) {
			libDir = args[i+1]
		}
	}
	if libDir == "" {
		t.Fatalf("menu-tui.sh must pass --lib-dir; args were: %v", args)
	}
	if _, err := os.Stat(filepath.Join(libDir, "install.sh")); err != nil {
		t.Errorf("--lib-dir %q must contain install.sh: %v", libDir, err)
	}
}

// Inside a running session WISP_DECK_LIB_DIR is exported; it must win over the
// module-location fallback so a symlinked install resolves the same lib/.
func TestMenuTui_lib_dir_honours_WISP_DECK_LIB_DIR(t *testing.T) {
	dir := t.TempDir()
	argLog := filepath.Join(dir, "args")
	binDir := mockCommand(t, dir, "wisp-deck-tui",
		fmt.Sprintf(`printf '%%s\n' "$@" > %q; echo '{"selected":false}'`, argLog))
	mockCommand(t, dir, "jq", `echo "false"`)
	env := buildEnv(t, []string{binDir}, "XDG_CONFIG_HOME="+dir,
		"WISP_DECK_LIB_DIR=/custom/lib")
	writeTempFile(t, dir, "projects", "app:/tmp/app\n")

	runBashSnippet(t, `
error(){ :; }
AI_TOOLS_AVAILABLE=(claude)
source `+projectRoot(t)+`/lib/menu-tui.sh
select_project_interactive `+dir+`/projects || true
`, env)

	raw, _ := os.ReadFile(argLog)
	args := strings.Split(strings.TrimSpace(string(raw)), "\n")
	for i, a := range args {
		if a == "--lib-dir" && i+1 < len(args) {
			if args[i+1] != "/custom/lib" {
				t.Errorf("--lib-dir = %q, want /custom/lib", args[i+1])
			}
			return
		}
	}
	t.Fatalf("no --lib-dir in args: %v", args)
}

func TestMenuTui_passes_resolved_codex_to_main_menu(t *testing.T) {
	dir := t.TempDir()
	argLog := filepath.Join(dir, "args")
	binDir := mockCommand(t, dir, "wisp-deck-tui",
		fmt.Sprintf(`printf '%%s\n' "$@" > %q; echo '{"selected":false}'`, argLog))
	mockCommand(t, dir, "jq", `echo "false"`)
	env := buildEnv(t, []string{binDir}, "XDG_CONFIG_HOME="+dir)
	writeTempFile(t, dir, "projects", "app:/tmp/app\n")

	runBashSnippet(t, `
error(){ :; }
AI_TOOLS_AVAILABLE=(claude)
CODEX_CMD="/opt/Codex App/codex"
source `+projectRoot(t)+`/lib/menu-tui.sh
select_project_interactive `+dir+`/projects || true
`, env)

	raw, err := os.ReadFile(argLog)
	if err != nil {
		t.Fatalf("wisp-deck-tui was never invoked: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(raw)), "\n")
	for i, arg := range args {
		if arg == "--codex" && i+1 < len(args) {
			if args[i+1] != "/opt/Codex App/codex" {
				t.Fatalf("--codex = %q", args[i+1])
			}
			return
		}
	}
	t.Fatalf("menu-tui.sh did not pass --codex: %v", args)
}
