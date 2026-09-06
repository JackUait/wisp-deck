package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- statusline-wrapper: subscription panes correct the context figures ---
// Claude Code hardcodes the context window of its own models, so on a
// subscription pane the JSON it hands the statusline misstates the real
// context of the backend model. The wrapper must pipe the input through
// `wisp-deck-tui statusline-context` (which knows the catalog's real windows)
// before ccstatusline renders the context percentage — and must fall back to
// the untouched input when the rewriter is missing or produces nothing.

// setupContextWrapperTest builds the hermetic wrapper env plus two recording
// mocks: wisp-deck-tui logs its argv (and answers statusline-context with a
// recognizable rewrite), npx records the stdin it received.
func setupContextWrapperTest(t *testing.T, tuiBody string) (env []string, npxInput, tuiLog string) {
	t.Helper()
	dir := t.TempDir()
	fakeHome := filepath.Join(dir, "home")
	writeTempFile(t, fakeHome, ".claude/statusline-command.sh", `echo "GITINFO"`)

	npxInput = filepath.Join(fakeHome, "npx-input.json")
	mockCommand(t, dir, "npx", fmt.Sprintf(`cat > %q; echo "12.3%%"`, npxInput))
	// The wrapper prefers the ccstatusline binary over npx, so the capture
	// has to sit on the binary too or the rewritten JSON is never recorded.
	mockCommand(t, dir, "ccstatusline", fmt.Sprintf(`cat > %q; echo "12.3%%"`, npxInput))
	mockCommand(t, dir, "ps", `
if [[ "$*" == *"comm="* ]]; then echo "sh"; else echo "1"; fi
`)
	tuiLog = filepath.Join(fakeHome, "tui-calls.log")
	mockCommand(t, dir, "wisp-deck-tui", fmt.Sprintf(
		"printf '%%s\\n' \"$*\" >> %q\n%s", tuiLog, tuiBody))

	binDir := filepath.Join(dir, "bin")
	env = buildEnv(t, []string{binDir}, "HOME="+fakeHome)
	return env, npxInput, tuiLog
}

func runContextWrapper(t *testing.T, env []string, stdinData string) (string, int) {
	t.Helper()
	wrapperPath := filepath.Join(projectRoot(t), "templates", "statusline-wrapper.sh")
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)
	return runBashSnippet(t, script, env)
}

const contextStdin = `{"model":{"id":"glm-5.2","display_name":"GLM 5.2"},` +
	`"workspace":{"current_dir":"/tmp"},` +
	`"context_window":{"context_window_size":200000,"used_percentage":50}}`

// On a subscription pane ccstatusline must receive the REWRITTEN json, not
// Claude Code's original figures.
func TestSubContext_wrapper_feeds_rewritten_json_to_ccstatusline(t *testing.T) {
	env, npxInput, _ := setupContextWrapperTest(t,
		`if [ "$1" = "statusline-context" ]; then cat >/dev/null; printf '{"REWRITTEN":1}'; fi`)
	env = append(env, "CLAUDE_CONFIG_DIR=", "WISP_DECK_CLAUDE_CONFIG=glm.json")

	out, code := runContextWrapper(t, env, contextStdin)
	assertExitCode(t, code, 0)
	assertContains(t, out, "12.3%")
	data, err := os.ReadFile(npxInput)
	if err != nil {
		t.Fatalf("ccstatusline never received input: %v", err)
	}
	assertContains(t, string(data), `"REWRITTEN":1`)
	assertNotContains(t, string(data), `"used_percentage":50`)
}

// The rewriter runs with the statusline JSON on stdin — the recorded call
// must name the statusline-context subcommand.
func TestSubContext_wrapper_invokes_statusline_context(t *testing.T) {
	env, _, tuiLog := setupContextWrapperTest(t,
		`if [ "$1" = "statusline-context" ]; then cat >/dev/null; printf '{}'; fi`)
	env = append(env, "CLAUDE_CONFIG_DIR=", "WISP_DECK_CLAUDE_CONFIG=glm.json")

	_, code := runContextWrapper(t, env, contextStdin)
	assertExitCode(t, code, 0)
	data, _ := os.ReadFile(tuiLog)
	if !strings.Contains(string(data), "statusline-context") {
		t.Fatalf("statusline-context not invoked; calls: %q", string(data))
	}
}

// A standard-Claude pane keeps Claude Code's own figures: no rewrite call,
// original JSON straight to ccstatusline.
func TestSubContext_wrapper_standard_pane_keeps_original_input(t *testing.T) {
	env, npxInput, tuiLog := setupContextWrapperTest(t,
		`if [ "$1" = "statusline-context" ]; then cat >/dev/null; printf '{"REWRITTEN":1}'; fi`)
	env = append(env, "CLAUDE_CONFIG_DIR=", "WISP_DECK_CLAUDE_CONFIG=")

	_, code := runContextWrapper(t, env, contextStdin)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(npxInput)
	if err != nil {
		t.Fatalf("ccstatusline never received input: %v", err)
	}
	assertContains(t, string(data), `"used_percentage":50`)
	assertNotContains(t, string(data), "REWRITTEN")
	// Give the (possible) backgrounded refresher a beat, then confirm no
	// statusline-context call was recorded on a standard pane.
	time.Sleep(100 * time.Millisecond)
	if log, err := os.ReadFile(tuiLog); err == nil && strings.Contains(string(log), "statusline-context") {
		t.Fatalf("statusline-context invoked on a standard pane: %q", string(log))
	}
}

// A rewriter that produces nothing (crash, absent subcommand) must not blank
// the context percentage: the wrapper falls back to the original input.
func TestSubContext_wrapper_falls_back_when_rewriter_outputs_nothing(t *testing.T) {
	env, npxInput, _ := setupContextWrapperTest(t,
		`if [ "$1" = "statusline-context" ]; then cat >/dev/null; exit 1; fi`)
	env = append(env, "CLAUDE_CONFIG_DIR=", "WISP_DECK_CLAUDE_CONFIG=glm.json")

	out, code := runContextWrapper(t, env, contextStdin)
	assertExitCode(t, code, 0)
	assertContains(t, out, "12.3%")
	data, err := os.ReadFile(npxInput)
	if err != nil {
		t.Fatalf("ccstatusline never received input: %v", err)
	}
	assertContains(t, string(data), `"used_percentage":50`)
}

// No wisp-deck-tui on PATH at all: the subscription pane still renders with
// Claude Code's original figures rather than dying or blanking.
func TestSubContext_wrapper_survives_missing_tui_binary(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	fakeHome := filepath.Join(dir, "home")
	writeTempFile(t, fakeHome, ".claude/statusline-command.sh", `echo "GITINFO"`)
	npxInput := filepath.Join(fakeHome, "npx-input.json")
	mockCommand(t, dir, "npx", fmt.Sprintf(`cat > %q; echo "12.3%%"`, npxInput))
	// The wrapper prefers the ccstatusline binary over npx, so the capture
	// has to sit on the binary too or the rewritten JSON is never recorded.
	mockCommand(t, dir, "ccstatusline", fmt.Sprintf(`cat > %q; echo "12.3%%"`, npxInput))
	mockCommand(t, dir, "ps", `
if [[ "$*" == *"comm="* ]]; then echo "sh"; else echo "1"; fi
`)
	binDir := filepath.Join(dir, "bin")
	env := buildEnv(t, []string{binDir}, "HOME="+fakeHome,
		"PATH="+binDir+":/usr/bin:/bin",
		"CLAUDE_CONFIG_DIR=", "WISP_DECK_CLAUDE_CONFIG=glm.json")

	out, code := runContextWrapper(t, env, contextStdin)
	assertExitCode(t, code, 0)
	assertContains(t, out, "12.3%")
	data, err := os.ReadFile(npxInput)
	if err != nil {
		t.Fatalf("ccstatusline never received input: %v", err)
	}
	assertContains(t, string(data), `"used_percentage":50`)
}
