package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pressing U in the menu ran `npx wisp-deck@latest`, which is the *first-time
// setup* script: it re-asked "Select AI Tools", re-offered the Ghostty config
// merge, and dropped the user into a wall of installer output instead of the
// menu they came from. An update must never re-ask a question the user has
// already answered.

// --- wisp_deck_is_configured ---

func TestUpdateNoninteractive_is_configured_follows_the_saved_ai_tool(t *testing.T) {
	tests := []struct {
		name    string
		content string
		write   bool
		want    int
	}{
		{"configured", "claude\n", true, 0},
		{"empty file is not an answer", "", true, 1},
		{"never set up", "", false, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.write {
				if err := os.MkdirAll(filepath.Join(dir, "wisp-deck"), 0o755); err != nil {
					t.Fatal(err)
				}
				writeTempFile(t, filepath.Join(dir, "wisp-deck"), "ai-tool", tt.content)
			}
			env := buildEnv(t, nil, "XDG_CONFIG_HOME="+dir)
			_, code := runBashSnippet(t, updateSnippet(t, `wisp_deck_is_configured`), env)
			assertExitCode(t, code, tt.want)
		})
	}
}

// --- resolve_setup_ai_tool ---

// An update reuses the saved answer and never launches the selector. This is
// the exact screen the user reported: the multi-select AI tool list appearing
// after an update they triggered from the running menu.
func TestUpdateNoninteractive_resolve_ai_tool_reuses_the_saved_answer(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "wisp-deck"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, filepath.Join(dir, "wisp-deck"), "ai-tool", "codex\n")

	tuiCalls := filepath.Join(dir, "tui-calls")
	binDir := mockCommand(t, dir, "wisp-deck-tui", `echo "$@" >> `+tuiCalls)
	env := buildEnv(t, []string{binDir}, "XDG_CONFIG_HOME="+dir)

	out, code := runBashSnippet(t, updateSnippet(t,
		`resolve_setup_ai_tool 1 && echo "TOOL=$_selected_ai_tool"`), env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "TOOL=codex")

	if _, err := os.Stat(tuiCalls); err == nil {
		data, _ := os.ReadFile(tuiCalls)
		t.Errorf("update mode launched the AI tool selector: %q", string(data))
	}
}

// A first-time setup still asks — the selector is the whole point of it.
func TestUpdateNoninteractive_resolve_ai_tool_still_asks_on_a_fresh_setup(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "wisp-deck-tui",
		`echo '{"confirmed":true,"tools":["opencode"]}'`)
	jqDir := binDir
	env := buildEnv(t, []string{jqDir}, "XDG_CONFIG_HOME="+dir)

	root := projectRoot(t)
	snippet := "source " + filepath.Join(root, "lib", "tui.sh") +
		" && source " + filepath.Join(root, "lib", "ai-select-tui.sh") +
		" && source " + filepath.Join(root, "lib", "update.sh") +
		` && resolve_setup_ai_tool 0 && echo "TOOL=$_selected_ai_tool"`
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "TOOL=opencode")
}

// Update mode with no saved answer at all still has to produce a tool rather
// than stopping to ask mid-update; Claude Code is the default the installer
// has always shipped.
func TestUpdateNoninteractive_resolve_ai_tool_defaults_when_nothing_is_saved(t *testing.T) {
	dir := t.TempDir()
	tuiCalls := filepath.Join(dir, "tui-calls")
	binDir := mockCommand(t, dir, "wisp-deck-tui", `echo "$@" >> `+tuiCalls)
	env := buildEnv(t, []string{binDir}, "XDG_CONFIG_HOME="+dir)

	out, code := runBashSnippet(t, updateSnippet(t,
		`resolve_setup_ai_tool 1 && echo "TOOL=$_selected_ai_tool"`), env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "TOOL=claude")
	if _, err := os.Stat(tuiCalls); err == nil {
		t.Error("update mode launched the AI tool selector with no saved answer")
	}
}

// --- bin/wisp-deck ---

// The flag has to be accepted: the installer rejects every unknown --flag, so
// without this the update exits 1 before it does anything.
func TestUpdateNoninteractive_installer_accepts_the_update_flag(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "bin", "wisp-deck"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "--update)") {
		t.Error("bin/wisp-deck does not accept --update; the updater's invocation exits 1")
	}
	if !strings.Contains(content, "resolve_setup_ai_tool") {
		t.Error("bin/wisp-deck still inlines the AI tool selector instead of resolving it")
	}
	// The Ghostty merge prompt blocks on /dev/tty. During an update the screen
	// is owned by the progress view and stdin is not the user's, so a prompt
	// there hangs forever with nothing on screen to explain it.
	if !strings.Contains(content, "UPDATE_MODE") {
		t.Error("bin/wisp-deck has no update mode to gate its prompts on")
	}
}
