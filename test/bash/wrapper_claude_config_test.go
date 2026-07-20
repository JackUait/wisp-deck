package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWrapperResolvesActiveConfigForClaude(t *testing.T) {
	root := projectRoot(t)
	libPath := filepath.Join(root, "lib/claude-configs.sh")
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	_ = os.MkdirAll(cfgDir, 0o755)
	writeTempFile(t, cfgDir, "work.json", "{}")
	writeTempFile(t, dir, "claude-config", "work.json")

	script := `
source ` + libPath + `
SELECTED_AI_TOOL=claude
GT_CONFIG_DIR="` + dir + `"
WISP_DECK_CLAUDE_SETTINGS=""
if [ "$SELECTED_AI_TOOL" = "claude" ]; then
  WISP_DECK_CLAUDE_SETTINGS="$(resolve_claude_config_path "$GT_CONFIG_DIR/claude-configs" "$GT_CONFIG_DIR/claude-config")"
fi
echo "RESULT=$WISP_DECK_CLAUDE_SETTINGS"
`
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	if !strings.Contains(out, "RESULT="+filepath.Join(cfgDir, "work.json")) {
		t.Fatalf("got %q", out)
	}
}

func TestWrapperNoConfigWhenStandard(t *testing.T) {
	root := projectRoot(t)
	libPath := filepath.Join(root, "lib/claude-configs.sh")
	dir := t.TempDir()
	script := `
source ` + libPath + `
SELECTED_AI_TOOL=claude
GT_CONFIG_DIR="` + dir + `"
WISP_DECK_CLAUDE_SETTINGS="$(resolve_claude_config_path "$GT_CONFIG_DIR/claude-configs" "$GT_CONFIG_DIR/claude-config")"
echo "RESULT=[$WISP_DECK_CLAUDE_SETTINGS]"
`
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "RESULT=[]")
}

func TestWrapperPublishesClaudeProviderAndCodexPathBeforeLaunch(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	wrapper := string(data)
	resolve := strings.Index(wrapper, `WISP_DECK_CLAUDE_PROVIDER="$(get_claude_config_provider`)
	build := strings.Index(wrapper, `AI_LAUNCH_CMD="$(build_ai_launch_cmd`)
	if resolve < 0 || build < 0 || resolve > build {
		t.Fatal("wrapper must resolve the trusted provider marker before building the launch")
	}
	for _, want := range []string{
		`export WISP_DECK_CLAUDE_PROVIDER WISP_DECK_CLAUDE_CONFIG WISP_DECK_CODEX_CMD`,
		`-e "WISP_DECK_CLAUDE_PROVIDER=$WISP_DECK_CLAUDE_PROVIDER"`,
		`-e "WISP_DECK_CODEX_CMD=$WISP_DECK_CODEX_CMD"`,
	} {
		if !strings.Contains(wrapper, want) {
			t.Fatalf("wrapper missing %q", want)
		}
	}
}
