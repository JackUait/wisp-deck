package bash_test

import (
	"testing"
)

func TestBuildAILaunchCmd_appends_settings_for_claude(t *testing.T) {
	env := []string{"WISP_DECK_CLAUDE_SETTINGS=/cfg/work.json"}
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "/proj"}, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, `claude /proj --settings "/cfg/work.json"`)
}

func TestBuildAILaunchCmd_no_settings_when_env_empty(t *testing.T) {
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "/proj"}, nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "--settings")
}

func TestBuildAILaunchCmd_settings_on_resume(t *testing.T) {
	env := []string{"WISP_DECK_RESUME=1", "WISP_DECK_CLAUDE_SETTINGS=/cfg/work.json"}
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude"}, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, `claude -c --settings "/cfg/work.json"`)
}

func TestBuildAILaunchCmd_settings_ignored_for_opencode(t *testing.T) {
	env := []string{"WISP_DECK_CLAUDE_SETTINGS=/cfg/work.json"}
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"opencode", "opencode", "/proj"}, env)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "--settings")
}
