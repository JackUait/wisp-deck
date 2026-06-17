package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigMenu_dispatch_add_then_quit(t *testing.T) {
	dir := t.TempDir()
	cfgRoot := filepath.Join(dir, "ghost-tab")
	_ = os.MkdirAll(cfgRoot, 0o755)

	// Mock ghost-tab-tui: first call returns add, second returns quit.
	bin := mockCommand(t, dir, "ghost-tab-tui", `
state="`+dir+`/calls"
n=$(cat "$state" 2>/dev/null || echo 0)
echo $((n+1)) > "$state"
case "$1" in
  claude-config-menu)
    if [ "$n" = "0" ]; then echo '{"action":"add","name":"Work"}'; else echo '{"action":"quit"}'; fi ;;
  *) echo '{}' ;;
esac
`)
	env := buildEnv(t, []string{bin}, "XDG_CONFIG_HOME="+dir)

	root := projectRoot(t)
	script := `
source ` + root + `/lib/claude-configs.sh
source ` + root + `/lib/config-tui.sh
manage_claude_configs_interactive
ls "` + cfgRoot + `/claude-configs"
cat "` + cfgRoot + `/claude-configs.list"
`
	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "work.json")
	if !strings.Contains(out, "Work:work.json") {
		t.Fatalf("list should contain entry:\n%s", out)
	}
}
