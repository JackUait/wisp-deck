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

func TestEnsureCodex_installs_globally_via_npm_when_absent(t *testing.T) {
	dir := t.TempDir()
	npmLog := filepath.Join(dir, "npm_calls")
	// codex is NOT mocked → not yet installed. npm is available.
	binDir := mockCommand(t, dir, "npm", fmt.Sprintf(`echo "$@" >> %q; exit 0`, npmLog))
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr")
	env := buildEnv(t, nil, "PATH="+binDir+":/bin")
	out, code := runBashSnippet(t, installSnippet(t, `ensure_codex`), env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Codex installed")

	calls, _ := os.ReadFile(npmLog)
	if !strings.Contains(string(calls), "install -g @openai/codex") {
		t.Errorf("npm calls = %q, want an `install -g @openai/codex`", string(calls))
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
