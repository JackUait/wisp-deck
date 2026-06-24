package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestLogo_TickAdvancesFrame(t *testing.T) {
	m := NewLogo("claude")
	// Send a logoTickMsg
	updated, cmd := m.Update(logoTickMsg(time.Now()))
	result := updated.(LogoModel)
	if result.frame != 1 {
		t.Errorf("After tick: expected frame 1, got %d", result.frame)
	}
	// Should return a new tick command since not quitting
	if cmd == nil {
		t.Error("Tick should return a new tick command when not quitting")
	}
}

func TestLogo_TickWhileQuitting(t *testing.T) {
	m := NewLogo("claude")
	m.quitting = true
	updated, cmd := m.Update(logoTickMsg(time.Now()))
	result := updated.(LogoModel)
	_ = result
	if cmd != nil {
		t.Error("Tick should return nil command when quitting")
	}
}

func TestLogo_QuitMsg(t *testing.T) {
	m := NewLogo("claude")
	updated, cmd := m.Update(quitMsg{})
	result := updated.(LogoModel)
	if !result.quitting {
		t.Error("quitMsg should set quitting to true")
	}
	if cmd == nil {
		t.Error("quitMsg should return tea.Quit")
	}
}

func TestLogo_ViewEmptyWhenQuitting(t *testing.T) {
	m := NewLogo("claude")
	m.quitting = true
	if m.View() != "" {
		t.Error("View should be empty when quitting")
	}
}

func TestLogoModel_Update_stores_window_size(t *testing.T) {
	m := NewLogo("claude")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 50})
	result := updated.(LogoModel)
	if result.width != 120 {
		t.Errorf("expected width 120, got %d", result.width)
	}
	if result.height != 50 {
		t.Errorf("expected height 50, got %d", result.height)
	}
}

func TestLogoModel_View_uncentered_when_dimensions_unknown(t *testing.T) {
	m := NewLogo("")
	got := m.View()
	want := RenderGhost(GhostForTool("", false))
	if got != want {
		t.Errorf("expected uncentered ghost output when dimensions unknown\ngot:  %q\nwant: %q", got, want)
	}
}

func TestLogoModel_View_uses_active_theme(t *testing.T) {
	// The logo splash should follow the active (resolved) theme, so a green
	// preset turns even the claude ghost shape green.
	restoreClaudeTheme(t)
	ApplyTheme(presetThemes["green"])

	m := NewLogo("claude")
	got := m.View()
	if !strings.Contains(got, "\033[38;5;78m") {
		t.Error("logo should use green Primary (78) when the green theme is active")
	}
	if strings.Contains(got, "\033[38;5;209m") {
		t.Error("logo should NOT use claude orange Primary (209) when green theme is active")
	}
}

func TestLogoModel_View_centers_when_dimensions_known(t *testing.T) {
	m := NewLogo("")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	m = updated.(LogoModel)

	got := m.View()
	raw := RenderGhost(GhostForTool("", false))

	// lipgloss.Place pads each line to the full width and pads vertically,
	// so the raw string is not a literal substring. Verify content is preserved
	// by checking that at least one raw line's trimmed content appears in the output.
	rawLines := strings.Split(raw, "\n")
	found := false
	for _, rl := range rawLines {
		trimmed := strings.TrimSpace(rl)
		if trimmed != "" && strings.Contains(got, trimmed) {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected centered output to contain ghost art content from raw output")
	}

	// lipgloss.Place fills the full canvas height — output must have exactly 40 lines
	lines := strings.Split(got, "\n")
	if len(lines) != 40 {
		t.Errorf("expected centered output to have 40 lines (= terminal height), got %d", len(lines))
	}
}
