package bash_test

import (
	"os"
	"path/filepath"
	"testing"
)

// The dispatcher delegates mutations to `ghost-tab-tui claude-config <action>`
// (the single Go source of truth). This test mocks the binary and asserts the
// dispatcher invokes it with the right action and arguments.
func TestConfigMenu_dispatch_add_invokes_binary_then_quits(t *testing.T) {
	dir := t.TempDir()
	cfgRoot := filepath.Join(dir, "ghost-tab")
	_ = os.MkdirAll(cfgRoot, 0o755)
	calls := filepath.Join(dir, "calls.log")

	// Mock ghost-tab-tui: claude-config-menu returns add once then quit;
	// claude-config records its arguments.
	bin := mockCommand(t, dir, "ghost-tab-tui", `
state="`+dir+`/n"
n=$(cat "$state" 2>/dev/null || echo 0)
echo $((n+1)) > "$state"
case "$1" in
  claude-config-menu)
    if [ "$n" = "0" ]; then echo '{"action":"add","name":"Work"}'; else echo '{"action":"quit"}'; fi ;;
  claude-config)
    shift; echo "$@" >> "`+calls+`" ;;
  *) echo '{}' ;;
esac
`)
	env := buildEnv(t, []string{bin}, "XDG_CONFIG_HOME="+dir)

	root := projectRoot(t)
	script := `
source ` + root + `/lib/claude-configs.sh
source ` + root + `/lib/config-tui.sh
manage_claude_configs_interactive
`
	_, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("binary was not invoked: %v", err)
	}
	got := string(data)
	for _, want := range []string{"add", "--list", "--dir", "--name", "Work"} {
		assertContains(t, got, want)
	}
}
