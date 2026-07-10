package tui

import (
	"strings"
	"testing"
)

func TestGhostForTool_sleeping_returns_correct_tool(t *testing.T) {
	tests := []struct {
		name  string
		tool  string
		check func(t *testing.T, lines []string)
	}{
		{
			name: "claude sleeping has blush marks",
			tool: "claude",
			check: func(t *testing.T, lines []string) {
				if len(lines) != 16 {
					t.Fatalf("expected 16 lines (with blush), got %d", len(lines))
				}
				// Blush marks appear on line index 6 (below eyes line)
				// They use the SleepBlush color (ANSI 168 for claude)
				found := false
				for _, line := range lines {
					if strings.Contains(line, "\033[38;5;168m") {
						found = true
						break
					}
				}
				if !found {
					t.Error("claude sleeping ghost should contain SleepBlush (168) blush marks")
				}
			},
		},
		{
			name: "opencode sleeping has dim purple lower band",
			tool: "opencode",
			check: func(t *testing.T, lines []string) {
				found := false
				for _, line := range lines {
					if strings.Contains(line, "\033[38;5;61m") {
						found = true
						break
					}
				}
				if !found {
					t.Error("opencode sleeping ghost should contain SleepAccent (61) dim purple band")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := GhostForTool(tt.tool, true)
			tt.check(t, lines)
		})
	}
}

func TestGhostForTool_sleeping_body_has_color_variation(t *testing.T) {
	// The sleeping ghost should NOT be a flat single color —
	// upper body, lower body, and feet should use different sleep colors.
	lines := GhostForTool("claude", true)
	if len(lines) != 16 {
		t.Fatalf("expected 16 lines (with blush), got %d", len(lines))
	}

	// Line 4 (index 4) is upper body — should use SleepPrimary (166)
	// Line 9 (index 9) is lower body — should use SleepDim (130, darker)
	// Line 14 (index 14) is the feet row — should use SleepDarkFeet (94)
	upperLine := lines[4] // upper body (above eyes)
	lowerLine := lines[9] // lower body (below blush)
	feetLine := lines[14] // feet row

	// Upper body should contain SleepPrimary
	if !strings.Contains(upperLine, "\033[38;5;166m") {
		t.Error("upper body should use SleepPrimary color (166)")
	}

	// Lower body should contain SleepDim (130, distinct from SleepPrimary for depth)
	if !strings.Contains(lowerLine, "\033[38;5;130m") {
		t.Error("lower body should use SleepDim color (130) for depth")
	}

	// Feet should use SleepDarkFeet (94)
	if !strings.Contains(feetLine, "\033[38;5;94m") {
		t.Error("feet should use SleepDarkFeet color (94)")
	}
}

// stripAnsiSeq removes ANSI escape sequences so tests can measure the
// visible glyphs of a ghost line.
func stripAnsiSeq(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// The OpenCode mascot is Brace: a ghost first — a solid teardrop body in the
// Claude/Codex family (full-width block runs, no detached wings), topped with
// a pointed wisp tip and finished with scalloped feet. OpenCode identity
// lives in accents (belly emblem, blush, violet palette), not the silhouette.
func TestGhostForTool_opencode_awake_is_ghost(t *testing.T) {
	lines := GhostForTool("opencode", false)
	// Full ghost body: at least one row is a solid 24-block run, like the
	// Claude ghost. (The moth's widest run was 18.)
	wide := false
	for _, line := range lines {
		if strings.Contains(stripAnsiSeq(line), strings.Repeat("█", 24)) {
			wide = true
			break
		}
	}
	if !wide {
		t.Error("expected a solid 24-block body row — Brace must be a full ghost, not a slim moth")
	}
	// No detached wings: the widest body row is one solid run, no interior gaps.
	body := strings.TrimSpace(stripAnsiSeq(lines[6]))
	if strings.Contains(body, " ") {
		t.Errorf("body row must be solid (no wing gaps), got %q", body)
	}
	// Pointed wisp tip: the top row is a single centered 2-block tip.
	top := strings.TrimSpace(stripAnsiSeq(lines[0]))
	if top != "██" {
		t.Errorf("expected a single 2-block wisp tip on line 0, got %q", top)
	}
	// Ghost-family feet: the bottom row is scalloped (blocks with gaps).
	feet := strings.TrimSpace(stripAnsiSeq(lines[len(lines)-1]))
	if !strings.Contains(feet, "█ ") && !strings.Contains(feet, "█  ") {
		t.Errorf("expected scalloped ghost feet on the bottom row, got %q", feet)
	}
}

func TestGhostForTool_opencode_moth_visible_width_is_28(t *testing.T) {
	for _, sleeping := range []bool{false, true} {
		for i, line := range GhostForTool("opencode", sleeping) {
			w := len([]rune(stripAnsiSeq(line)))
			if w != 28 {
				t.Errorf("sleeping=%v line %d visible width = %d, want 28", sleeping, i, w)
			}
		}
	}
}

func TestGhostForTool_opencode_moth_has_blush(t *testing.T) {
	// Brace always blushes: the mauve blush color (139) appears awake AND asleep.
	for _, sleeping := range []bool{false, true} {
		joined := strings.Join(GhostForTool("opencode", sleeping), "\n")
		if !strings.Contains(joined, "\033[38;5;139m") {
			t.Errorf("sleeping=%v: Brace should have mauve blush (139)", sleeping)
		}
	}
}

func TestGhostForTool_opencode_awake_is_purple(t *testing.T) {
	// The OpenCode ghost should wear the purple palette, not the old grayscale.
	joined := strings.Join(GhostForTool("opencode", false), "\n")

	if !strings.Contains(joined, "\033[38;5;141m") {
		t.Error("opencode awake ghost should use brand purple (Primary 141)")
	}
	for _, gray := range []string{"\033[38;5;250m", "\033[38;5;255m", "\033[38;5;244m"} {
		if strings.Contains(joined, gray) {
			t.Errorf("opencode ghost should no longer use grayscale code %q", gray)
		}
	}
}

func TestGhostForTool_awake_has_open_eyes(t *testing.T) {
	// Awake ghosts should have EyeWhite (255) in the eye lines
	lines := GhostForTool("claude", false)
	if len(lines) != 15 {
		t.Fatalf("expected 15 lines, got %d", len(lines))
	}
	found := false
	for _, line := range lines {
		if strings.Contains(line, "\033[38;5;255m") {
			found = true
			break
		}
	}
	if !found {
		t.Error("awake claude ghost should have EyeWhite (255) for open eyes")
	}
}

func TestGhostForTool_sleeping_has_closed_eyes(t *testing.T) {
	// Sleeping ghosts should NOT have EyeWhite — eyes are closed
	lines := GhostForTool("claude", true)
	for _, line := range lines {
		if strings.Contains(line, "\033[38;5;255m") {
			t.Error("sleeping ghost should not have EyeWhite (255) — eyes should be closed")
		}
	}
}

func TestGhostForTool_sleeping_line_count(t *testing.T) {
	tools := []string{"claude", "opencode"}
	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {
			lines := GhostForTool(tool, true)
			if len(lines) != 16 {
				t.Errorf("expected 16 lines (with blush), got %d", len(lines))
			}
		})
	}
}

func TestGhostForTheme_uses_passed_theme_colors(t *testing.T) {
	// The claude ghost shape painted with the green preset should use the
	// green Primary (78) for its body and NOT the claude orange Primary (209).
	joined := strings.Join(GhostForTheme("claude", false, ResolveTheme("claude", "green")), "\n")

	if !strings.Contains(joined, "\033[38;5;78m") {
		t.Error("green-themed claude ghost should use green Primary (78)")
	}
	if strings.Contains(joined, "\033[38;5;209m") {
		t.Error("green-themed claude ghost should NOT use claude orange Primary (209)")
	}
}

func TestGhostForTheme_keeps_tool_shape(t *testing.T) {
	// Painting the opencode shape with the claude palette must still produce the
	// opencode shape (15 awake lines), proving shape comes from tool, color from theme.
	lines := GhostForTheme("opencode", false, themes["claude"])
	if len(lines) != 15 {
		t.Fatalf("expected 15 lines for awake opencode shape, got %d", len(lines))
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "\033[38;5;209m") {
		t.Error("opencode shape painted with claude palette should use claude Primary (209)")
	}
}

func TestGhostForTool_delegates_to_per_tool_theme(t *testing.T) {
	// GhostForTool must still match GhostForTheme with the tool's own palette.
	for _, tool := range []string{"claude", "opencode"} {
		for _, sleeping := range []bool{false, true} {
			got := strings.Join(GhostForTool(tool, sleeping), "\n")
			want := strings.Join(GhostForTheme(tool, sleeping, ThemeForTool(tool)), "\n")
			if got != want {
				t.Errorf("GhostForTool(%q,%v) should equal GhostForTheme with per-tool palette", tool, sleeping)
			}
		}
	}
}

func TestRenderGhost_sleeping(t *testing.T) {
	lines := GhostForTool("claude", true)
	result := RenderGhost(lines)
	if result == "" {
		t.Error("RenderGhost should not return empty string")
	}
	// Should contain newlines joining the lines
	if !strings.Contains(result, "\n") {
		t.Error("RenderGhost should join lines with newlines")
	}
	// Should have exactly 15 newlines (16 lines joined)
	if strings.Count(result, "\n") != 15 {
		t.Errorf("expected 15 newlines, got %d", strings.Count(result, "\n"))
	}
}
