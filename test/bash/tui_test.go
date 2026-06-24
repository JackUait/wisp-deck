package bash_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// --- success ---

func TestTui_success_outputs_checkmark_and_message(t *testing.T) {
	out, code := runBashFunc(t, "lib/tui.sh", "success", []string{"all good"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "\u2713") // ✓
	assertContains(t, out, "all good")
}

// --- warn ---

func TestTui_warn_outputs_exclamation_and_message(t *testing.T) {
	out, code := runBashFunc(t, "lib/tui.sh", "warn", []string{"be careful"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "!")
	assertContains(t, out, "be careful")
}

// --- error ---

func TestTui_error_outputs_cross_and_message(t *testing.T) {
	out, code := runBashFunc(t, "lib/tui.sh", "error", []string{"something broke"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "\u2717") // ✗
	assertContains(t, out, "something broke")
}

// --- info ---

func TestTui_info_outputs_arrow_and_message(t *testing.T) {
	out, code := runBashFunc(t, "lib/tui.sh", "info", []string{"starting now"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "\u2192") // →
	assertContains(t, out, "starting now")
}

// --- header ---

func TestTui_header_outputs_the_message(t *testing.T) {
	out, code := runBashFunc(t, "lib/tui.sh", "header", []string{"My Section"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "My Section")
}

// --- pad ---

func TestTui_pad_5_outputs_exactly_5_characters(t *testing.T) {
	out, code := runBashFunc(t, "lib/tui.sh", "pad", []string{"5"}, nil)
	assertExitCode(t, code, 0)
	if len(out) != 5 {
		t.Errorf("expected pad(5) to output exactly 5 characters, got %d: %q", len(out), out)
	}
}

func TestTui_pad_0_outputs_empty_string(t *testing.T) {
	out, code := runBashFunc(t, "lib/tui.sh", "pad", []string{"0"}, nil)
	assertExitCode(t, code, 0)
	if len(out) != 0 {
		t.Errorf("expected pad(0) to output empty string, got %d characters: %q", len(out), out)
	}
}

// --- moveto ---

func TestTui_moveto_3_7_outputs_correct_escape_sequence(t *testing.T) {
	root := projectRoot(t)
	modulePath := filepath.Join(root, "lib/tui.sh")
	script := fmt.Sprintf("source %q && moveto 3 7", modulePath)

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)

	expected := "\033[3;7H"
	if out != expected {
		t.Errorf("moveto(3, 7) = %q, want %q", out, expected)
	}
}

// --- tui_init_interactive ---

func TestTui_tui_init_interactive_sets_CYAN_non_empty(t *testing.T) {
	root := projectRoot(t)
	modulePath := filepath.Join(root, "lib/tui.sh")
	script := fmt.Sprintf(`source %q && tui_init_interactive && echo "$_CYAN"`, modulePath)

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)

	trimmed := strings.TrimRight(out, "\n")
	if trimmed == "" {
		t.Error("expected _CYAN to be non-empty after tui_init_interactive")
	}
}

func TestTui_tui_init_interactive_sets_HIDE_CURSOR_non_empty(t *testing.T) {
	root := projectRoot(t)
	modulePath := filepath.Join(root, "lib/tui.sh")
	script := fmt.Sprintf(`source %q && tui_init_interactive && echo "$_HIDE_CURSOR"`, modulePath)

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)

	trimmed := strings.TrimRight(out, "\n")
	if trimmed == "" {
		t.Error("expected _HIDE_CURSOR to be non-empty after tui_init_interactive")
	}
}

// --- set_tab_title ---

func TestTui_set_tab_title_includes_project_and_tool_separated_by_middot(t *testing.T) {
	root := projectRoot(t)
	modulePath := filepath.Join(root, "lib/tui.sh")
	script := fmt.Sprintf(`source %q && set_tab_title "ghost-tab" "claude"`, modulePath)

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "ghost-tab \u00b7 claude") // · is \u00b7
}

func TestTui_set_tab_title_outputs_OSC_escape_sequence(t *testing.T) {
	root := projectRoot(t)
	modulePath := filepath.Join(root, "lib/tui.sh")
	script := fmt.Sprintf(`source %q && set_tab_title "myproject" "opencode"`, modulePath)

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)

	// OSC 0 sets window title: \033]0;TITLE\007
	expected := "\033]0;myproject \u00b7 opencode\007"
	if out != expected {
		t.Errorf("set_tab_title output = %q, want %q", out, expected)
	}
}

func TestTui_set_tab_title_omits_tool_name_when_empty(t *testing.T) {
	root := projectRoot(t)
	modulePath := filepath.Join(root, "lib/tui.sh")
	script := fmt.Sprintf(`source %q && set_tab_title "myproject" ""`, modulePath)

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)

	expected := "\033]0;myproject\007"
	if out != expected {
		t.Errorf("set_tab_title output = %q, want %q", out, expected)
	}
}

// --- set_tab_title_waiting ---

// The waiting title must NOT prepend a dot — Ghostty's native bell icon is the
// only waiting indicator, so the waiting title matches the plain active title.
func TestTui_set_tab_title_waiting_has_no_dot_with_project_and_tool(t *testing.T) {
	root := projectRoot(t)
	modulePath := filepath.Join(root, "lib/tui.sh")
	script := fmt.Sprintf(`source %q && set_tab_title_waiting "ghost-tab" "claude"`, modulePath)

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "ghost-tab · claude")
	assertNotContains(t, out, "●")
}

func TestTui_set_tab_title_waiting_outputs_plain_OSC_escape(t *testing.T) {
	root := projectRoot(t)
	modulePath := filepath.Join(root, "lib/tui.sh")
	script := fmt.Sprintf(`source %q && set_tab_title_waiting "myproject" "opencode"`, modulePath)

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)

	expected := "\033]0;myproject · opencode\007"
	if out != expected {
		t.Errorf("set_tab_title_waiting output = %q, want %q", out, expected)
	}
}

func TestTui_set_tab_title_waiting_omits_tool_when_empty(t *testing.T) {
	root := projectRoot(t)
	modulePath := filepath.Join(root, "lib/tui.sh")
	script := fmt.Sprintf(`source %q && set_tab_title_waiting "myproject" ""`, modulePath)

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)

	expected := "\033]0;myproject\007"
	if out != expected {
		t.Errorf("set_tab_title_waiting output = %q, want %q", out, expected)
	}
}

// --- draw_logo ---

func TestTui_draw_logo_calls_ghost_tab_tui_show_logo(t *testing.T) {
	dir := t.TempDir()

	// Create a mock ghost-tab-tui that outputs MOCK_LOGO_OUTPUT when called with show-logo
	binDir := mockCommand(t, dir, "ghost-tab-tui", `
if [ "$1" = "show-logo" ]; then
  echo "MOCK_LOGO_OUTPUT"
  exit 0
fi
exit 1
`)

	root := projectRoot(t)
	modulePath := filepath.Join(root, "lib/tui.sh")
	script := fmt.Sprintf(`source %q && draw_logo "claude"`, modulePath)

	env := buildEnv(t, []string{binDir})
	out, code := runBashSnippet(t, script, env)

	assertExitCode(t, code, 0)
	trimmed := strings.TrimSpace(out)
	if trimmed != "MOCK_LOGO_OUTPUT" {
		t.Errorf("draw_logo output = %q, want %q", trimmed, "MOCK_LOGO_OUTPUT")
	}
}
