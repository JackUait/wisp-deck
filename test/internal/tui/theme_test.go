package tui_test

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/ghost-tab/internal/tui"
)

func TestThemeForTool_Claude(t *testing.T) {
	theme := tui.ThemeForTool("claude")

	if theme.Name != "claude" {
		t.Errorf("Expected Name to be 'claude', got %q", theme.Name)
	}
	if theme.Primary != lipgloss.Color("209") {
		t.Errorf("Expected Primary to be '209', got %q", theme.Primary)
	}
}

func TestThemeForTool_OpenCode(t *testing.T) {
	theme := tui.ThemeForTool("opencode")

	if theme.Primary != lipgloss.Color("141") {
		t.Errorf("Expected Primary to be '141', got %q", theme.Primary)
	}
}

func TestThemeForTool_Unknown(t *testing.T) {
	theme := tui.ThemeForTool("unknown-tool")

	if theme.Name != "claude" {
		t.Errorf("Expected unknown tool to fall back to claude theme, got Name=%q", theme.Name)
	}
	if theme.Primary != lipgloss.Color("209") {
		t.Errorf("Expected unknown tool Primary to be '209' (claude), got %q", theme.Primary)
	}
}

func TestApplyTheme(t *testing.T) {
	tools := []string{"claude", "opencode"}

	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {
			theme := tui.ThemeForTool(tool)
			// ApplyTheme should not panic for any valid theme
			tui.ApplyTheme(theme)
		})
	}
}

func TestApplyTheme_UnknownFallback(t *testing.T) {
	// Applying a theme from an unknown tool (falls back to claude) should work
	theme := tui.ThemeForTool("unknown-tool")
	tui.ApplyTheme(theme)
	// No panic means success
}

func TestAnsiFromThemeColor(t *testing.T) {
	result := tui.AnsiFromThemeColor(lipgloss.Color("209"))
	expected := "\033[38;5;209m"
	if result != expected {
		t.Errorf("AnsiFromThemeColor(209): expected %q, got %q", expected, result)
	}
}

func TestAllThemes(t *testing.T) {
	expectedNames := map[string]string{
		"claude":   "claude",
		"opencode": "opencode",
	}

	for tool, expectedName := range expectedNames {
		theme := tui.ThemeForTool(tool)
		if theme.Name != expectedName {
			t.Errorf("ThemeForTool(%q): expected Name=%q, got %q", tool, expectedName, theme.Name)
		}
	}
}

func TestThemeTextColors(t *testing.T) {
	expectedText := map[string]lipgloss.Color{
		"claude":   lipgloss.Color("223"),
		"opencode": lipgloss.Color("189"),
	}

	for tool, expected := range expectedText {
		t.Run(tool, func(t *testing.T) {
			theme := tui.ThemeForTool(tool)
			if theme.Text != expected {
				t.Errorf("ThemeForTool(%q).Text: expected %q, got %q", tool, expected, theme.Text)
			}
		})
	}
}

// TestThemeBrightColors corresponds to the BATS ai_tool_bright_color tests.
// The bash bright_color function uses the same ANSI-256 code for claude as the
// normal color (209) while the Go theme uses a slightly different shade (208) for
// visual distinction. OpenCode bright is 255 in both implementations.
func TestThemeBrightColors(t *testing.T) {
	expectedBright := map[string]lipgloss.Color{
		"claude":   lipgloss.Color("208"),
		"opencode": lipgloss.Color("147"),
	}

	for tool, expected := range expectedBright {
		t.Run(tool, func(t *testing.T) {
			theme := tui.ThemeForTool(tool)
			if theme.Bright != expected {
				t.Errorf("ThemeForTool(%q).Bright: expected %q, got %q", tool, expected, theme.Bright)
			}
		})
	}
}

// TestThemeBrightColor_UnknownFallback corresponds to the BATS test
// "ai_tool_bright_color: unknown returns bold white". In bash, unknown tools
// get bold white (\033[1;37m). In Go, unknown tools fall back to the claude
// theme, so Bright is "208" (claude's bright orange).
func TestThemeBrightColor_UnknownFallback(t *testing.T) {
	theme := tui.ThemeForTool("unknown-tool")
	if theme.Bright != lipgloss.Color("208") {
		t.Errorf("Expected unknown tool Bright to fall back to claude's '208', got %q", theme.Bright)
	}
}

// TestThemeDimColors corresponds to the bash ai_tool_dim_color function.
// Bash uses dim-modified ANSI codes (e.g., \033[2;38;5;209m for claude dim).
// Go stores the dim color as a separate ANSI-256 value in the Dim field.
func TestThemeDimColors(t *testing.T) {
	expectedDim := map[string]lipgloss.Color{
		"claude":   lipgloss.Color("166"),
		"opencode": lipgloss.Color("99"),
	}

	for tool, expected := range expectedDim {
		t.Run(tool, func(t *testing.T) {
			theme := tui.ThemeForTool(tool)
			if theme.Dim != expected {
				t.Errorf("ThemeForTool(%q).Dim: expected %q, got %q", tool, expected, theme.Dim)
			}
		})
	}
}

// TestThemeDimColor_UnknownFallback verifies unknown tools fall back to
// claude's dim color.
func TestThemeDimColor_UnknownFallback(t *testing.T) {
	theme := tui.ThemeForTool("unknown-tool")
	if theme.Dim != lipgloss.Color("166") {
		t.Errorf("Expected unknown tool Dim to fall back to claude's '166', got %q", theme.Dim)
	}
}

// TestAnsiFromThemeColor_AllTools verifies that AnsiFromThemeColor produces
// correct ANSI escape sequences for each tool's Primary color. This corresponds
// to the BATS ai_tool_color tests which check the exact ANSI escape codes.
func TestAnsiFromThemeColor_AllTools(t *testing.T) {
	tests := []struct {
		tool     string
		color    lipgloss.Color
		expected string
	}{
		{"claude", lipgloss.Color("209"), "\033[38;5;209m"},
		{"opencode", lipgloss.Color("141"), "\033[38;5;141m"},
	}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			theme := tui.ThemeForTool(tt.tool)
			result := tui.AnsiFromThemeColor(theme.Primary)
			if result != tt.expected {
				t.Errorf("AnsiFromThemeColor(%q Primary): expected %q, got %q", tt.tool, tt.expected, result)
			}
		})
	}
}
