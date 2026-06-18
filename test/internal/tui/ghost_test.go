package tui_test

import (
	"strings"
	"testing"

	"github.com/jackuait/ghost-tab/internal/tui"
)

func TestGhostForTool_AllTools(t *testing.T) {
	tools := []string{"claude", "codex", "opencode"}

	for _, tool := range tools {
		t.Run(tool+"_awake", func(t *testing.T) {
			lines := tui.GhostForTool(tool, false)
			if len(lines) != 15 {
				t.Errorf("GhostForTool(%q, false): expected 15 lines, got %d", tool, len(lines))
			}
		})

		t.Run(tool+"_sleeping", func(t *testing.T) {
			lines := tui.GhostForTool(tool, true)
			if len(lines) != 15 {
				t.Errorf("GhostForTool(%q, true): expected 15 lines, got %d", tool, len(lines))
			}
		})
	}
}

func TestGhostForTool_SleepingVariants(t *testing.T) {
	tools := []string{"claude", "codex", "opencode"}

	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {
			awake := tui.GhostForTool(tool, false)
			sleeping := tui.GhostForTool(tool, true)

			awakeStr := tui.RenderGhost(awake)
			sleepingStr := tui.RenderGhost(sleeping)

			if awakeStr == sleepingStr {
				t.Errorf("GhostForTool(%q): awake and sleeping variants should differ", tool)
			}
		})
	}
}

func TestGhostForTool_Unknown(t *testing.T) {
	unknown := tui.GhostForTool("unknown-tool", false)
	claude := tui.GhostForTool("claude", false)

	if len(unknown) != 15 {
		t.Errorf("GhostForTool(unknown): expected 15 lines, got %d", len(unknown))
	}

	// Unknown tool should fall back to claude ghost
	unknownStr := tui.RenderGhost(unknown)
	claudeStr := tui.RenderGhost(claude)
	if unknownStr != claudeStr {
		t.Errorf("GhostForTool(unknown): expected fallback to claude ghost")
	}
}

func TestRenderZzz(t *testing.T) {
	result := tui.RenderZzz()

	if result == "" {
		t.Error("RenderZzz(): expected non-empty string")
	}

	// Should contain z and Z characters
	if !strings.Contains(result, "z") {
		t.Error("RenderZzz(): expected to contain lowercase 'z'")
	}
	if !strings.Contains(result, "Z") {
		t.Error("RenderZzz(): expected to contain uppercase 'Z'")
	}
}

func TestRenderGhost(t *testing.T) {
	lines := []string{"line1", "line2", "line3"}
	result := tui.RenderGhost(lines)

	expected := "line1\nline2\nline3"
	if result != expected {
		t.Errorf("RenderGhost(): expected %q, got %q", expected, result)
	}
}

func TestRenderGhost_Empty(t *testing.T) {
	result := tui.RenderGhost([]string{})
	if result != "" {
		t.Errorf("RenderGhost(empty): expected empty string, got %q", result)
	}
}

func TestGhostForToolWithTheme_UsesThemeColors(t *testing.T) {
	theme := tui.ThemeForTool("claude")
	expectedColor := tui.AnsiFromThemeColor(theme.Primary)

	lines := tui.GhostForTool("claude", false)
	rendered := tui.RenderGhost(lines)

	if !strings.Contains(rendered, expectedColor) {
		t.Errorf("awake claude ghost should contain theme Primary color %q", expectedColor)
	}
}

func TestGhostForToolWithTheme_SleepingUsesDimmedColors(t *testing.T) {
	theme := tui.ThemeForTool("claude")
	sleepColor := tui.AnsiFromThemeColor(theme.SleepPrimary)

	lines := tui.GhostForTool("claude", true)
	rendered := tui.RenderGhost(lines)

	if !strings.Contains(rendered, sleepColor) {
		t.Errorf("sleeping claude ghost should contain theme SleepPrimary color %q", sleepColor)
	}
}
