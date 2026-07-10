package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Theming ---
//
// Codex's auto theme resolves to `teal` (accent 36). `teal` is deliberately not
// a user-selectable preset: adding a Settings row shifts positional indices
// across several files and breaks index-hardcoded tests, and nothing requires
// picking teal by hand.

func TestTheme_resolve_codex_auto_is_teal(t *testing.T) {
	for _, pref := range []string{"auto", "", "bogus"} {
		out, code := runBashFunc(t, "lib/theme.sh", "gt_resolve_theme", []string{pref, "codex"}, nil)
		assertExitCode(t, code, 0)
		if got := strings.TrimSpace(out); got != "teal" {
			t.Errorf("gt_resolve_theme %q codex = %q, want teal", pref, got)
		}
	}
}

func TestTheme_resolve_codex_named_preset_still_wins(t *testing.T) {
	out, code := runBashFunc(t, "lib/theme.sh", "gt_resolve_theme", []string{"rose", "codex"}, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "rose" {
		t.Errorf("got %q, want rose", got)
	}
}

func TestTheme_teal_accent_and_palette(t *testing.T) {
	out, code := runBashFunc(t, "lib/theme.sh", "get_theme_accent", []string{"teal"}, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "36" {
		t.Errorf("get_theme_accent teal = %q, want 36", got)
	}

	out, code = runBashFunc(t, "lib/theme.sh", "get_theme_palette", []string{"teal"}, nil)
	assertExitCode(t, code, 0)
	// An 8-stop dark→light ramp, like every other preset.
	if got := len(strings.Fields(out)); got != 8 {
		t.Errorf("get_theme_palette teal has %d stops, want 8: %q", got, out)
	}
	if !strings.Contains(out, "36") {
		t.Errorf("teal ramp %q should include the brand accent 36", out)
	}
}

// The bash accent table must agree with the Go theme's Primary for codex.
func TestTheme_teal_accent_matches_get_tool_accent(t *testing.T) {
	a, _ := runBashFunc(t, "lib/tmux-session.sh", "get_tool_accent", []string{"codex"}, nil)
	b, _ := runBashFunc(t, "lib/theme.sh", "get_theme_accent", []string{"teal"}, nil)
	if strings.TrimSpace(a) != strings.TrimSpace(b) {
		t.Errorf("get_tool_accent codex = %q but get_theme_accent teal = %q",
			strings.TrimSpace(a), strings.TrimSpace(b))
	}
}

func TestLoading_get_tool_palette_codex_is_teal_ramp(t *testing.T) {
	out, code := runBashFunc(t, "lib/loading.sh", "get_tool_palette", []string{"codex"}, nil)
	assertExitCode(t, code, 0)
	if got := len(strings.Fields(out)); got != 8 {
		t.Errorf("codex palette has %d stops, want 8: %q", got, out)
	}
	claude, _ := runBashFunc(t, "lib/loading.sh", "get_tool_palette", []string{"claude"}, nil)
	if strings.TrimSpace(out) == strings.TrimSpace(claude) {
		t.Error("codex palette must not fall through to the claude default")
	}
}

// --- Installer ---

func TestEnsureCodex_skips_install_when_already_present(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "codex", `exit 0`)
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr")
	env := buildEnv(t, nil, "PATH="+binDir+":/bin")
	out, code := runBashSnippet(t, installSnippet(t, `ensure_codex`), env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Codex already installed")
}

// mockNpmWithPrefix builds an npm mock that mimics a real npm: `install -g`
// writes the launcher into <prefix>/bin, `prefix -g` prints the prefix. The
// prefix directory is deliberately NOT on PATH — exactly the lazy-nvm setup
// where npm's global bin dir is invisible to `command -v`.
func mockNpmWithPrefix(t *testing.T, dir string) (binDir, npmPrefix, npmLog string) {
	t.Helper()
	npmPrefix = filepath.Join(dir, "nvm-prefix")
	if err := os.MkdirAll(filepath.Join(npmPrefix, "bin"), 0755); err != nil {
		t.Fatalf("failed to create npm prefix bin: %v", err)
	}
	npmLog = filepath.Join(dir, "npm_calls")
	binDir = mockCommand(t, dir, "npm", fmt.Sprintf(`echo "$@" >> %q
if [ "$1" = "prefix" ]; then echo %q; exit 0; fi
if [ "$1" = "install" ]; then
  printf '#!/bin/bash\nexit 0\n' > %q
  chmod +x %q
fi
exit 0`, npmLog, npmPrefix,
		filepath.Join(npmPrefix, "bin", "codex"), filepath.Join(npmPrefix, "bin", "codex")))
	return binDir, npmPrefix, npmLog
}

func TestEnsureCodex_installs_globally_via_npm_when_absent(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	// codex is NOT mocked → not yet installed. npm is available.
	binDir, _, npmLog := mockNpmWithPrefix(t, dir)
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr", "dirname", "mkdir", "chmod")
	env := buildEnv(t, nil, "HOME="+home,
		"PATH="+binDir+":"+filepath.Join(home, ".local", "bin")+":/bin")
	out, code := runBashSnippet(t, installSnippet(t, `ensure_codex`), env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Codex installed")

	calls, _ := os.ReadFile(npmLog)
	if !strings.Contains(string(calls), "install -g @openai/codex") {
		t.Errorf("npm calls = %q, want an `install -g @openai/codex`", string(calls))
	}
}

// Regression: under lazy-nvm setups `npm install -g` exits 0 but drops the
// launcher into npm's global prefix, which is not on PATH. ensure_codex used
// to declare success anyway, leaving `command -v codex` failing forever — the
// settings panel showed the install as failed no matter how often it ran. The
// fix links the launcher into ~/.local/bin so codex is actually reachable.
func TestEnsureCodex_links_launcher_into_local_bin_when_npm_prefix_off_path(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	binDir, npmPrefix, _ := mockNpmWithPrefix(t, dir)
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr", "dirname", "mkdir", "chmod")
	env := buildEnv(t, nil, "HOME="+home,
		"PATH="+binDir+":"+filepath.Join(home, ".local", "bin")+":/bin")

	out, code := runBashSnippet(t,
		installSnippet(t, `ensure_codex && command -v codex`), env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Codex installed")

	link := filepath.Join(home, ".local", "bin", "codex")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("expected %s to be a symlink to the npm launcher: %v", link, err)
	}
	if want := filepath.Join(npmPrefix, "bin", "codex"); target != want {
		t.Errorf("symlink target = %q, want %q", target, want)
	}
}

// A codex that IS reachable after `npm install -g` (global bin already on
// PATH) must not get a redundant ~/.local/bin link.
func TestEnsureCodex_skips_link_when_install_lands_on_path(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	// npm "installs" codex straight into the mock bin dir, which is on PATH.
	binDir := mockCommand(t, dir, "npm", fmt.Sprintf(
		`if [ "$1" = "install" ]; then printf '#!/bin/bash\nexit 0\n' > %q; chmod +x %q; fi; exit 0`,
		filepath.Join(dir, "bin", "codex"), filepath.Join(dir, "bin", "codex")))
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr", "dirname", "mkdir", "chmod")
	env := buildEnv(t, nil, "HOME="+home, "PATH="+binDir+":/bin")

	out, code := runBashSnippet(t, installSnippet(t, `ensure_codex`), env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Codex installed")

	if _, err := os.Lstat(filepath.Join(home, ".local", "bin", "codex")); err == nil {
		t.Error("no ~/.local/bin/codex link should be created when codex is already reachable")
	}
}

func TestEnsureCodex_warns_when_node_missing(t *testing.T) {
	dir := t.TempDir()
	// Neither codex nor npm on PATH.
	binDir := mockCommand(t, dir, "placeholder", `:`)
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr")
	env := buildEnv(t, nil, "PATH="+binDir+":/bin")
	out, code := runBashSnippet(t, installSnippet(t, `ensure_codex || true`), env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Node.js")
}

// --- Selector priority ---
//
// select_ai_tool_interactive picks the primary tool from a multi-selection by a
// fixed priority: claude > opencode > codex. Codex is appended last so claude
// keeps first-available priority.

func selectPrimaryTool(t *testing.T, tools ...string) string {
	t.Helper()
	tmpDir := t.TempDir()
	lines := strings.Join(tools, `\n`)
	mockCommand(t, tmpDir, "wisp-deck-tui",
		`[ "$1" = "multi-select-ai-tool" ] && { echo '{"confirmed":true}'; exit 0; }; exit 1`)
	mockCommand(t, tmpDir, "jq", fmt.Sprintf(`#!/bin/bash
if [ "$2" = ".confirmed" ]; then echo "true"
elif [ "$2" = ".tools[]" ]; then printf "%s\n"
fi
exit 0
`, lines))

	script := fmt.Sprintf(`
set -euo pipefail
export PATH="%s/bin:$PATH"
error() { echo "ERROR: $*" >&2; }
source %q
select_ai_tool_interactive
echo "SELECTED_TOOL=${_selected_ai_tool:-}"
`, tmpDir, projectRoot(t)+"/lib/ai-select-tui.sh")

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "SELECTED_TOOL=") {
			return strings.TrimPrefix(line, "SELECTED_TOOL=")
		}
	}
	t.Fatalf("no SELECTED_TOOL in output: %q", out)
	return ""
}

func TestAiSelect_priority_claude_beats_codex(t *testing.T) {
	if got := selectPrimaryTool(t, "codex", "claude"); got != "claude" {
		t.Errorf("got %q, want claude", got)
	}
}

func TestAiSelect_priority_opencode_beats_codex(t *testing.T) {
	if got := selectPrimaryTool(t, "codex", "opencode"); got != "opencode" {
		t.Errorf("got %q, want opencode", got)
	}
}

func TestAiSelect_codex_alone_is_selected(t *testing.T) {
	if got := selectPrimaryTool(t, "codex"); got != "codex" {
		t.Errorf("got %q, want codex", got)
	}
}
