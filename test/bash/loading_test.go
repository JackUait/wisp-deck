package bash_test

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// --- get_loading_art ---

func TestLoading_get_loading_art_returns_nonempty(t *testing.T) {
	out, code := runBashFunc(t, "lib/loading.sh", "get_loading_art", nil, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) == "" {
		t.Error("get_loading_art() returned empty output")
	}
}

func TestLoading_get_loading_art_contains_wisp_deck_box(t *testing.T) {
	out, code := runBashFunc(t, "lib/loading.sh", "get_loading_art", nil, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "+---")
	assertContains(t, out, "d8888b")
}

func TestLoading_get_loading_art_renders_wisp_deck_wordmark(t *testing.T) {
	out, code := runBashFunc(t, "lib/loading.sh", "get_loading_art", nil, nil)
	assertExitCode(t, code, 0)
	// The loader shows the "Wisp Deck" wordmark, not the old "Ghost Tab" art.
	// `d888b` is the peak of the colossal "W" (only in "Wisp"); `d88""88b` is the
	// rounded "o" glyph that only appears in "Ghost".
	assertContains(t, out, `d888b`)
	assertNotContains(t, out, `d88""88b`)
}

func TestLoading_get_loading_art_meets_minimum_size(t *testing.T) {
	out, code := runBashFunc(t, "lib/loading.sh", "get_loading_art", nil, nil)
	assertExitCode(t, code, 0)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 10 {
		t.Errorf("art has %d lines, want >= 10", len(lines))
	}

	maxWidth := 0
	for _, line := range lines {
		if len(line) > maxWidth {
			maxWidth = len(line)
		}
	}
	if maxWidth < 85 {
		t.Errorf("art max width is %d, want >= 85", maxWidth)
	}
}

func TestLoading_get_loading_art_has_equal_line_widths(t *testing.T) {
	out, code := runBashFunc(t, "lib/loading.sh", "get_loading_art", nil, nil)
	assertExitCode(t, code, 0)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) == 0 {
		t.Fatal("no lines")
	}
	expected := len(lines[0])
	for i, line := range lines {
		if len(line) != expected {
			t.Errorf("line %d has %d chars, want %d (same as line 0)", i, len(line), expected)
		}
	}
}

// --- _detect_term_size ---

func TestLoading_detect_term_size_returns_two_positive_numbers(t *testing.T) {
	out, code := runBashFunc(t, "lib/loading.sh", "_detect_term_size", nil, nil)
	assertExitCode(t, code, 0)
	parts := strings.Fields(strings.TrimSpace(out))
	if len(parts) != 2 {
		t.Fatalf("expected 2 values, got %d: %q", len(parts), out)
	}
	for _, p := range parts {
		num, err := strconv.Atoi(p)
		if err != nil {
			t.Errorf("non-numeric value: %q", p)
		}
		if num <= 0 {
			t.Errorf("expected positive number, got %d", num)
		}
	}
}

func TestLoading_detect_term_size_works_in_posix_mode(t *testing.T) {
	// Regression test: Ghostty 1.2.x runs bash with --posix via its /bin/sh -c
	// expansion path. In POSIX mode, process substitution < <(...) is a syntax
	// error that prevented loading.sh from being sourced at all.
	root := projectRoot(t)
	modulePath := filepath.Join(root, "lib", "loading.sh")
	script := fmt.Sprintf("source %q && _detect_term_size", modulePath)
	cmd := exec.Command("bash", "--posix", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash --posix failed (syntax error in POSIX mode?): %v\noutput: %s", err, out)
	}
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) != 2 {
		t.Fatalf("expected 2 values, got %d: %q", len(parts), string(out))
	}
	for _, p := range parts {
		num, err := strconv.Atoi(p)
		if err != nil {
			t.Errorf("non-numeric value: %q", p)
		}
		if num <= 0 {
			t.Errorf("expected positive number, got %d", num)
		}
	}
}

// --- render_loading_frame ---

func TestLoading_render_loading_frame_contains_ansi_color_codes(t *testing.T) {
	root := projectRoot(t)
	script := fmt.Sprintf(
		`source %q/lib/loading.sh && render_loading_frame claude 0 80 24`,
		root)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	// Should contain ANSI 256-color escape: \033[38;5;XXXm
	assertContains(t, out, "\033[38;5;")
}

func TestLoading_render_loading_frame_contains_art_content(t *testing.T) {
	root := projectRoot(t)
	script := fmt.Sprintf(
		`source %q/lib/loading.sh && render_loading_frame claude 0 80 24`,
		root)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	// Should contain recognizable art content
	if len(out) < 100 {
		t.Errorf("render_loading_frame output too short (%d bytes), expected substantial output", len(out))
	}
}

func TestLoading_render_loading_frame_centers_art_on_large_terminal(t *testing.T) {
	root := projectRoot(t)
	// Large terminal: 200 cols, 50 rows
	script := fmt.Sprintf(
		`source %q/lib/loading.sh && render_loading_frame claude 0 200 50`,
		root)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	// Art is 12 lines tall, 88 chars wide.
	// Terminal coordinates are 1-based, so centering needs +1:
	//   row = (50-12)/2 + 1 = 20   (19 rows above, 19 below)
	//   col = (200-88)/2 + 1 = 57  (56 cols left, 56 right)
	assertContains(t, out, "\033[20;57H")
}

// --- get_tool_palette ---

func TestLoading_get_tool_palette_returns_claude_palette(t *testing.T) {
	out, code := runBashFunc(t, "lib/loading.sh", "get_tool_palette", []string{"claude"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "130 166 172 208 209 214 215 220")
}

func TestLoading_get_tool_palette_returns_opencode_palette(t *testing.T) {
	out, code := runBashFunc(t, "lib/loading.sh", "get_tool_palette", []string{"opencode"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "60 61 62 99 135 141 147 183")
}

func TestTmuxSession_get_tool_accent_opencode_is_purple(t *testing.T) {
	out, code := runBashFunc(t, "lib/tmux-session.sh", "get_tool_accent", []string{"opencode"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "141")
}

func TestTmuxSession_get_tool_accent_defaults_to_orange(t *testing.T) {
	for _, tool := range []string{"claude", "unknown", ""} {
		out, code := runBashFunc(t, "lib/tmux-session.sh", "get_tool_accent", []string{tool}, nil)
		assertExitCode(t, code, 0)
		assertContains(t, out, "209")
	}
}

func TestLoading_get_tool_palette_defaults_to_claude_for_unknown(t *testing.T) {
	out, code := runBashFunc(t, "lib/loading.sh", "get_tool_palette", []string{"unknown"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "130 166 172 208 209 214 215 220")
}

func TestLoading_get_tool_palette_defaults_to_claude_for_empty(t *testing.T) {
	out, code := runBashFunc(t, "lib/loading.sh", "get_tool_palette", nil, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "130 166 172 208 209 214 215 220")
}

func TestLoading_render_loading_frame_uses_tool_palette(t *testing.T) {
	root := projectRoot(t)
	// OpenCode palette starts with color 60
	script := fmt.Sprintf(
		`source %q/lib/loading.sh && render_loading_frame opencode 0 90 24`,
		root)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	// Should contain ANSI color code from the purple palette
	assertContains(t, out, "38;5;60m")
}

func TestLoading_render_loading_frame_honors_palette_override(t *testing.T) {
	root := projectRoot(t)
	// A 5th arg overrides the tool palette so a chosen theme preset colours the
	// splash — here a green ramp; its colour 78 must appear, the tool's not.
	script := fmt.Sprintf(
		`source %q/lib/loading.sh && render_loading_frame claude 0 90 24 "22 28 34 35 41 77 78 120"`,
		root)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "38;5;78m")
	assertNotContains(t, out, "38;5;208m") // claude orange must not leak through
}

func TestLoading_render_loading_frame_shifts_colors_between_frames(t *testing.T) {
	root := projectRoot(t)
	script0 := fmt.Sprintf(
		`source %q/lib/loading.sh && render_loading_frame claude 0 80 24`,
		root)
	script1 := fmt.Sprintf(
		`source %q/lib/loading.sh && render_loading_frame claude 1 80 24`,
		root)
	out0, _ := runBashSnippet(t, script0, nil)
	out1, _ := runBashSnippet(t, script1, nil)
	if out0 == out1 {
		t.Error("expected different output for different frames")
	}
}

// --- show_loading_screen / stop_loading_screen ---

// TestLoading_show_loading_screen_rerenders_on_size_change verifies the first-frame
// race-condition fix: when stty reports a small/wrong size on the first call but the
// correct larger size on the second call (simulating a PTY that hasn't reported its
// final window size yet), show_loading_screen must:
//  1. Detect the size change after the brief sleep
//  2. Clear the screen (\033[2J)
//  3. Re-render frame 0 with the corrected (larger) dimensions
//
// The art is 10 lines × 88 chars wide.
// For the corrected terminal (40 rows × 160 cols):
//
//	row = (40-10)/2 + 1 = 16
//	col = (160-88)/2 + 1 = 37
//
// So we expect \033[16;37H in the output (the corrected first-line cursor position).
func TestLoading_show_loading_screen_rerenders_on_size_change(t *testing.T) {
	root := projectRoot(t)
	dir := t.TempDir()

	// Counter file: stty reads it to know which call number this is.
	counterFile := writeTempFile(t, dir, "stty_count", "0")

	// Mock stty: first call returns small size (10 40), subsequent calls return correct size (40 160).
	// We use a counter file to track invocations across subshell boundaries.
	sttyBody := fmt.Sprintf(`
count=$(cat %q 2>/dev/null || echo 0)
count=$((count + 1))
echo "$count" > %q
if [ "$count" -le 1 ]; then
  echo "10 40"
else
  echo "40 160"
fi
`, counterFile, counterFile)
	binDir := mockCommand(t, dir, "stty", sttyBody)
	env := buildEnv(t, []string{binDir})

	// show_loading_screen then immediately stop (background loop never meaningfully runs).
	script := fmt.Sprintf(`
		source %q/lib/loading.sh
		show_loading_screen claude
		stop_loading_screen
	`, root)
	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)

	// Must contain a second clear-screen escape (after the initial one in show_loading_screen).
	// We count occurrences: initial show emits one \033[2J, re-render emits a second.
	clearSeq := "\033[2J"
	count := strings.Count(out, clearSeq)
	if count < 2 {
		t.Errorf("expected at least 2 occurrences of \\033[2J (screen clear), got %d\noutput: %q", count, out)
	}

	// The corrected render must position the art at row=16, col=37 (40×160 terminal).
	assertContains(t, out, "\033[16;37H")

	// Tighter ordering assertion: the second \033[2J (the re-clear) must appear
	// before \033[16;37H (the corrected cursor position), proving the re-render
	// happens after the re-clear and not from an earlier accidental clear.
	posFirstClear := strings.Index(out, clearSeq)
	posSecondClear := strings.Index(out[posFirstClear+len(clearSeq):], clearSeq)
	posCorrectedPos := strings.Index(out, "\033[16;37H")
	if posSecondClear < 0 {
		t.Fatalf("could not find second %q in output", clearSeq)
	}
	// posSecondClear is relative to after the first clear; make it absolute.
	absSecondClear := posFirstClear + len(clearSeq) + posSecondClear
	if absSecondClear >= posCorrectedPos {
		t.Errorf("expected second \\033[2J (at %d) to come before \\033[16;37H (at %d)",
			absSecondClear, posCorrectedPos)
	}
}

func TestLoading_show_loading_screen_no_rerender_when_size_unchanged(t *testing.T) {
	root := projectRoot(t)
	dir := t.TempDir()

	// Mock stty always returns the same size (30 120).
	binDir := mockCommand(t, dir, "stty", `echo "30 120"`)
	env := buildEnv(t, []string{binDir})

	script := fmt.Sprintf(`
		source %q/lib/loading.sh
		show_loading_screen claude
		stop_loading_screen
	`, root)
	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)

	// Only the initial \033[2J from show_loading_screen — no extra clear from re-render.
	clearSeq := "\033[2J"
	count := strings.Count(out, clearSeq)
	if count != 1 {
		t.Errorf("expected exactly 1 occurrence of \\033[2J (no re-render), got %d\noutput: %q", count, out)
	}
}

// The loader hides the cursor while animating and hands off straight to the
// Bubbletea menu (alt screen), which keeps the cursor hidden and restores it
// on exit. If stop_loading_screen re-showed the cursor, it would blink at the
// bottom-right over the still-visible splash during the menu binary's startup
// gap. So a planned stop must NOT emit the show-cursor sequence \033[?25h.
func TestLoading_stop_loading_screen_keeps_cursor_hidden(t *testing.T) {
	root := projectRoot(t)
	script := fmt.Sprintf(`
		source %q/lib/loading.sh
		show_loading_screen claude
		stop_loading_screen
	`, root)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	// No show-cursor escape anywhere: it stays hidden for the menu handoff.
	assertNotContains(t, out, "\033[?25h")
	// The hide-cursor escape must still be present (loader hid it up front).
	assertContains(t, out, "\033[?25l")
}

func TestLoading_show_loading_screen_renders_first_frame_before_background_loop(t *testing.T) {
	root := projectRoot(t)
	// Call show then immediately stop — no sleep in between.
	// The first frame must be rendered synchronously so the user always
	// sees the loading art, even when stop comes within the 100ms
	// background-process startup delay.
	script := fmt.Sprintf(`
		source %q/lib/loading.sh
		show_loading_screen claude
		stop_loading_screen
	`, root)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	// Should contain rendered art content even with immediate stop
	assertContains(t, out, "d8888b")
}
