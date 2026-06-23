package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// restoreClaudeTheme resets the global theme state after a test mutates it via
// ApplyTheme, so theme order doesn't leak into other tests in the package.
func restoreClaudeTheme(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { ApplyTheme(themes["claude"]) })
}

func TestApplyTheme_opencode_diff_chrome_is_purple(t *testing.T) {
	restoreClaudeTheme(t)
	ApplyTheme(ThemeForTool("opencode"))

	purple := lipgloss.Color("141")
	if got := diffBoxStyle.GetBorderTopForeground(); got != purple {
		t.Errorf("diff box border = %v, want %v", got, purple)
	}
	if got := diffRuleStyle.GetForeground(); got != purple {
		t.Errorf("diff rule fg = %v, want %v", got, purple)
	}
	if got := diffTabActiveStyle.GetBackground(); got != purple {
		t.Errorf("active tab bg = %v, want %v", got, purple)
	}
	if got := diffTabIconStyle.GetForeground(); got != purple {
		t.Errorf("tab icon fg = %v, want %v", got, purple)
	}
	if got := diffTitleStyle.GetForeground(); got != purple {
		t.Errorf("diff title fg = %v, want %v", got, purple)
	}
	if got := diffStatusColors["modified"]; got != purple {
		t.Errorf("modified badge = %v, want %v", got, purple)
	}
}

func TestApplyTheme_claude_diff_chrome_stays_orange(t *testing.T) {
	restoreClaudeTheme(t)
	// Dirty the styles with opencode first, then prove claude restores orange.
	ApplyTheme(ThemeForTool("opencode"))
	ApplyTheme(ThemeForTool("claude"))

	orange := lipgloss.Color("208")
	if got := diffBoxStyle.GetBorderTopForeground(); got != orange {
		t.Errorf("diff box border = %v, want %v", got, orange)
	}
	if got := diffRuleStyle.GetForeground(); got != orange {
		t.Errorf("diff rule fg = %v, want %v", got, orange)
	}
	if got := diffTabActiveStyle.GetBackground(); got != orange {
		t.Errorf("active tab bg = %v, want %v", got, orange)
	}
	if got := diffStatusColors["modified"]; got != orange {
		t.Errorf("modified badge = %v, want %v", got, orange)
	}
}
