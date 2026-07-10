package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- filter_disabled_ai_tools ---

func TestFilterDisabledAITools_removes_listed_tools(t *testing.T) {
	dir := t.TempDir()
	disabled := writeTempFile(t, dir, "disabled-tools", "codex\n")
	out, code := runBashFunc(t, "lib/ai-tools.sh", "filter_disabled_ai_tools",
		[]string{disabled, "claude", "opencode", "codex"}, nil)
	assertExitCode(t, code, 0)
	if got := strings.Fields(out); len(got) != 2 || got[0] != "claude" || got[1] != "opencode" {
		t.Errorf("got %v, want [claude opencode]", got)
	}
}

func TestFilterDisabledAITools_missing_file_keeps_all(t *testing.T) {
	dir := t.TempDir()
	out, code := runBashFunc(t, "lib/ai-tools.sh", "filter_disabled_ai_tools",
		[]string{filepath.Join(dir, "nope"), "claude", "codex"}, nil)
	assertExitCode(t, code, 0)
	if got := strings.Fields(out); len(got) != 2 {
		t.Errorf("got %v, want both tools kept", got)
	}
}

// Safety valve: a disable list that would leave zero tools is ignored, so a
// launch can never end up tool-less.
func TestFilterDisabledAITools_all_disabled_keeps_all(t *testing.T) {
	dir := t.TempDir()
	disabled := writeTempFile(t, dir, "disabled-tools", "claude\ncodex\n")
	script := fmt.Sprintf(`source %q
filter_disabled_ai_tools %q claude codex 2>/dev/null`,
		projectRoot(t)+"/lib/ai-tools.sh", disabled)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	if got := strings.Fields(out); len(got) != 2 {
		t.Errorf("got %v, want disable list ignored when it empties the set", got)
	}
}

// Wrapper wiring: the same build+filter sequence wrapper.sh runs. No mapfile —
// wrapper.sh must run under macOS's stock bash 3.2.
func TestFilterDisabledAITools_wrapper_style_rebuild(t *testing.T) {
	dir := t.TempDir()
	disabled := writeTempFile(t, dir, "disabled-tools", "codex\n")
	script := fmt.Sprintf(`
source %q
AI_TOOLS_AVAILABLE=(claude codex)
_filtered=()
while IFS= read -r _t; do _filtered+=("$_t"); done \
  < <(filter_disabled_ai_tools %q "${AI_TOOLS_AVAILABLE[@]}")
AI_TOOLS_AVAILABLE=("${_filtered[@]}")
echo "${AI_TOOLS_AVAILABLE[*]}"
`, projectRoot(t)+"/lib/ai-tools.sh", disabled)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "claude" {
		t.Errorf("got %q, want claude", got)
	}
}

// --- remove_opencode / remove_codex ---

// mockNpmForRemoval logs calls; `prefix -g` prints prefixDir.
func mockNpmForRemoval(t *testing.T, dir, prefixDir string) (binDir, npmLog string) {
	t.Helper()
	npmLog = filepath.Join(dir, "npm_calls")
	binDir = mockCommand(t, dir, "npm", fmt.Sprintf(`echo "$@" >> %q
if [ "$1" = "prefix" ]; then echo %q; fi
exit 0`, npmLog, prefixDir))
	return binDir, npmLog
}

func TestRemoveCodex_uninstalls_and_removes_npm_launcher_link(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	prefix := filepath.Join(dir, "nvm-prefix")
	if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".local", "bin", "codex")
	if err := os.Symlink(filepath.Join(prefix, "bin", "codex"), link); err != nil {
		t.Fatal(err)
	}
	binDir, npmLog := mockNpmForRemoval(t, dir, prefix)
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr", "readlink")
	env := buildEnv(t, nil, "HOME="+home, "PATH="+binDir+":/bin")

	out, code := runBashSnippet(t, installSnippet(t, `remove_codex`), env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Codex removed")

	calls, _ := os.ReadFile(npmLog)
	if !strings.Contains(string(calls), "uninstall -g @openai/codex") {
		t.Errorf("npm calls = %q, want `uninstall -g @openai/codex`", string(calls))
	}
	if _, err := os.Lstat(link); err == nil {
		t.Error("npm-prefix launcher symlink should have been deleted")
	}
}

func TestRemoveCodex_leaves_foreign_launcher_alone(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	// A codex the user installed themselves — symlink pointing elsewhere.
	link := filepath.Join(home, ".local", "bin", "codex")
	if err := os.Symlink("/opt/custom/codex", link); err != nil {
		t.Fatal(err)
	}
	binDir, _ := mockNpmForRemoval(t, dir, filepath.Join(dir, "nvm-prefix"))
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr", "readlink")
	env := buildEnv(t, nil, "HOME="+home, "PATH="+binDir+":/bin")

	_, code := runBashSnippet(t, installSnippet(t, `remove_codex`), env)
	assertExitCode(t, code, 0)
	if _, err := os.Lstat(link); err != nil {
		t.Error("a launcher symlink not owned by npm must not be deleted")
	}
}

func TestRemoveOpencode_uninstalls_right_package(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	binDir, npmLog := mockNpmForRemoval(t, dir, filepath.Join(dir, "nvm-prefix"))
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr", "readlink")
	env := buildEnv(t, nil, "HOME="+home, "PATH="+binDir+":/bin")

	out, code := runBashSnippet(t, installSnippet(t, `remove_opencode`), env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "OpenCode removed")
	calls, _ := os.ReadFile(npmLog)
	if !strings.Contains(string(calls), "uninstall -g opencode-ai") {
		t.Errorf("npm calls = %q, want `uninstall -g opencode-ai`", string(calls))
	}
}

func TestRemoveCodex_fails_without_npm(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "placeholder", `:`)
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr")
	env := buildEnv(t, nil, "PATH="+binDir+":/bin")
	out, code := runBashSnippet(t, installSnippet(t, `remove_codex`), env)
	if code == 0 {
		t.Error("remove_codex should fail when npm is missing")
	}
	assertContains(t, out, "npm")
}

func TestRemoveCodex_fails_when_uninstall_fails(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "npm", `if [ "$1" = "uninstall" ]; then exit 1; fi; exit 0`)
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr", "readlink")
	env := buildEnv(t, nil, "PATH="+binDir+":/bin")
	out, code := runBashSnippet(t, installSnippet(t, `remove_codex`), env)
	if code == 0 {
		t.Error("remove_codex should propagate npm uninstall failure")
	}
	assertContains(t, out, "Failed to remove Codex")
}
