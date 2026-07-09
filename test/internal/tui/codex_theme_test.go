package tui_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/wisp-deck/internal/tui"
)

// Codex's auto accent is 256-color 36 (#00af87), the nearest match to OpenAI's
// brand #10a37f. Deliberately not 78 (the `green` preset) or 80 (`cyan`):
// reusing a preset's Primary would make codex's auto theme indistinguishable
// from a manually-selected preset.

func TestThemeForTool_Codex(t *testing.T) {
	theme := tui.ThemeForTool("codex")

	if theme.Name != "codex" {
		t.Errorf("Name = %q, want codex", theme.Name)
	}
	if theme.Primary != lipgloss.Color("36") {
		t.Errorf("Primary = %q, want 36", theme.Primary)
	}
}

func TestThemeForTool_codex_primary_collides_with_no_preset(t *testing.T) {
	codex := tui.ThemeForTool("codex").Primary
	for _, name := range tui.ThemePresets {
		if name == "auto" {
			continue // resolves per-tool, not a fixed color
		}
		if p := tui.ResolveTheme("claude", name).Primary; p == codex {
			t.Errorf("codex Primary %q collides with preset %q", codex, name)
		}
	}
}

// A codex user who picks a named preset gets the claude preset table, so the
// codex mascot must be painted with claude's color-slot semantics.
func TestResolveTheme_codex_named_preset_uses_claude_table(t *testing.T) {
	got := tui.ResolveTheme("codex", "rose")
	want := tui.ResolveTheme("claude", "rose")
	if got.Primary != want.Primary {
		t.Errorf("codex+rose Primary = %q, want %q", got.Primary, want.Primary)
	}
}

func TestResolveTheme_codex_auto_follows_tool(t *testing.T) {
	for _, pref := range []string{"", "auto", "bogus"} {
		if got := tui.ResolveTheme("codex", pref).Primary; got != lipgloss.Color("36") {
			t.Errorf("ResolveTheme(codex, %q).Primary = %q, want 36", pref, got)
		}
	}
}

// --- Ghost ---

func TestGhostForTool_codex_has_its_own_shape(t *testing.T) {
	codex := strings.Join(tui.GhostForTool("codex", false), "\n")
	claude := strings.Join(tui.GhostForTool("claude", false), "\n")
	if codex == claude {
		t.Error("codex ghost must not be the claude ghost verbatim")
	}
	if !strings.Contains(codex, "38;5;36m") {
		t.Errorf("awake codex ghost should use Primary 36:\n%s", codex)
	}
}

func TestGhostForTool_codex_sleeping_differs_from_awake(t *testing.T) {
	awake := strings.Join(tui.GhostForTool("codex", false), "\n")
	sleeping := strings.Join(tui.GhostForTool("codex", true), "\n")
	if awake == sleeping {
		t.Error("codex sleeping ghost must differ from the awake one")
	}
}

// Shape comes from the tool, color from the theme: painting the codex shape with
// the claude palette must keep the codex shape.
func TestGhostForTheme_codex_shape_survives_recolor(t *testing.T) {
	codexClaudePalette := tui.GhostForTheme("codex", false, tui.ThemeForTool("claude"))
	codexOwn := tui.GhostForTool("codex", false)
	if len(codexClaudePalette) != len(codexOwn) {
		t.Fatalf("recolored codex has %d lines, want %d", len(codexClaudePalette), len(codexOwn))
	}
	joined := strings.Join(codexClaudePalette, "\n")
	if !strings.Contains(joined, "38;5;209m") {
		t.Error("codex shape painted with the claude palette should use claude Primary (209)")
	}
}

// Awake mascots are 15 lines and 28 visible columns; sleeping mascots are 16.
// stripAnsi lives in mainmenu_test.go.
//
// Note: claude's and opencode's SLEEPING art has a 25-column blush row at index
// 6 — a pre-existing raggedness shared by both. The codex art is drawn to a
// uniform 28 columns rather than reproducing that defect, so this test asserts
// on codex only.
func TestGhostCodex_matches_the_shared_grid(t *testing.T) {
	for _, tc := range []struct {
		sleeping  bool
		wantLines int
	}{
		{false, 15},
		{true, 16},
	} {
		lines := tui.GhostForTool("codex", tc.sleeping)
		if len(lines) != tc.wantLines {
			t.Errorf("sleeping=%v: %d lines, want %d", tc.sleeping, len(lines), tc.wantLines)
		}
		for i, line := range lines {
			if w := len([]rune(stripAnsi(line))); w != 28 {
				t.Errorf("sleeping=%v line %d visible width %d, want 28: %q",
					tc.sleeping, i, w, stripAnsi(line))
			}
		}
	}
}

// The awake codex mascot occupies the same box as the other two, so the main
// menu and loading screen lay it out unchanged.
func TestGhostCodex_awake_box_matches_other_tools(t *testing.T) {
	want := len(tui.GhostForTool("claude", false))
	if got := len(tui.GhostForTool("codex", false)); got != want {
		t.Errorf("awake codex has %d lines, want %d (same as claude)", got, want)
	}
}

func TestAIToolDisplayName_codex(t *testing.T) {
	if got := tui.AIToolDisplayName("codex"); got != "Codex" {
		t.Errorf("AIToolDisplayName(codex) = %q, want %q", got, "Codex")
	}
}
