package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func configTuiSnippet(t *testing.T, body string) string {
	t.Helper()
	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	configPath := filepath.Join(root, "lib", "config-tui.sh")
	return fmt.Sprintf("source %q && source %q && %s", tuiPath, configPath, body)
}

func TestConfigMenuInteractive_dispatches_quit(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "ghost-tab-tui", `echo '{"action":"quit"}'`)
	mockCommand(t, dir, "jq", `
		if [ "$1" = "-r" ] && [ "$2" = ".action" ]; then
			read -r input
			# Extract action value from JSON using bash
			action="${input#*\"action\":\"}"
			action="${action%%\"*}"
			echo "$action"
		fi
	`)
	env := buildEnv(t, []string{binDir})

	snippet := configTuiSnippet(t, `config_menu_interactive`)
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
}

func TestConfigMenuInteractive_returns_1_when_binary_missing(t *testing.T) {
	dir := t.TempDir()
	emptyBin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(emptyBin, 0755); err != nil {
		t.Fatal(err)
	}
	// Minimal PATH that excludes ~/.local/bin where ghost-tab-tui lives
	env := buildEnv(t, nil, "PATH="+emptyBin+":/usr/bin:/bin:/usr/sbin:/sbin")

	snippet := configTuiSnippet(t, `config_menu_interactive`)
	out, code := runBashSnippet(t, snippet, env)
	if code == 0 {
		t.Error("expected non-zero exit when binary missing")
	}
	assertContains(t, out, "ghost-tab-tui")
}

func TestConfigMenuInteractive_returns_1_on_tui_failure(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "ghost-tab-tui", `exit 1`)
	env := buildEnv(t, []string{binDir})

	snippet := configTuiSnippet(t, `config_menu_interactive`)
	_, code := runBashSnippet(t, snippet, env)
	if code == 0 {
		t.Error("expected non-zero exit on TUI failure")
	}
}

func TestConfigMenuInteractive_passes_version(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "captured_args")
	binDir := mockCommand(t, dir, "ghost-tab-tui", fmt.Sprintf(`
echo "$*" > %q
echo '{"action":"quit"}'
`, argsFile))
	mockCommand(t, dir, "jq", `
		if [ "$1" = "-r" ] && [ "$2" = ".action" ]; then
			read -r input
			action="${input#*\"action\":\"}"
			action="${action%%\"*}"
			echo "$action"
		fi
	`)

	root := projectRoot(t)
	versionContent, _ := os.ReadFile(filepath.Join(root, "VERSION"))

	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
	)

	snippet := configTuiSnippet(t, `config_menu_interactive`)
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	captured, _ := os.ReadFile(argsFile)
	args := string(captured)
	assertContains(t, args, "--version")
	assertContains(t, args, strings.TrimSpace(string(versionContent)))
	// Terminal selection was removed; no terminal flag should be passed.
	assertNotContains(t, args, "--terminal-name")
}
