package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

const trueColorBlack = "\x1b[48;2;0;0;0m"

func trueColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

func TestFillBackground_paints_every_cell_of_the_screen(t *testing.T) {
	trueColor(t)

	out := FillBackground("hi", 6, 3)

	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (frame must be padded to the screen height): %q", len(lines), out)
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, trueColorBlack) {
			t.Errorf("line %d does not open with a pitch-black background: %q", i, line)
		}
		if w := lipgloss.Width(line); w != 6 {
			t.Errorf("line %d is %d columns wide, want 6 (padded to the screen width): %q", i, w, line)
		}
	}
}

func TestFillBackground_reapplies_black_after_an_inner_reset(t *testing.T) {
	trueColor(t)

	frame := lipgloss.NewStyle().Foreground(lipgloss.Color("209")).Render("wisp") + "deck"

	out := FillBackground(frame, 10, 1)

	// Every reset emitted by a nested style would drop the background back to the
	// terminal's own color for the rest of the line, so black must follow it.
	for i := 0; i < len(out); i++ {
		if strings.HasPrefix(out[i:], "\x1b[0m") {
			rest := out[i+len("\x1b[0m"):]
			if rest == "" {
				continue // trailing reset ends the line
			}
			if !strings.HasPrefix(rest, trueColorBlack) {
				t.Fatalf("reset at %d is not followed by black: %q", i, out)
			}
		}
	}
}

func TestFillBackground_leaves_an_empty_frame_empty(t *testing.T) {
	trueColor(t)

	if out := FillBackground("", 20, 5); out != "" {
		t.Errorf("a quitting model renders nothing; got %q", out)
	}
}

type stubModel struct {
	view    string
	updates int
}

func (s stubModel) Init() tea.Cmd { return nil }
func (s stubModel) Update(tea.Msg) (tea.Model, tea.Cmd) {
	s.updates++
	return s, nil
}
func (s stubModel) View() string { return s.view }

func TestBlackCanvas_paints_the_wrapped_view_black(t *testing.T) {
	trueColor(t)

	var m tea.Model = WithBlackBackground(stubModel{view: "menu"})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 8, Height: 2})

	lines := strings.Split(m.View(), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), m.View())
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, trueColorBlack) || lipgloss.Width(line) != 8 {
			t.Errorf("line %d is not a full-width black line: %q", i, line)
		}
	}
}

func TestUnwrap_peels_the_canvas_off_a_final_model(t *testing.T) {
	inner := stubModel{view: "menu"}

	if got := Unwrap(WithBlackBackground(inner)); got != tea.Model(inner) {
		t.Errorf("Unwrap did not peel the canvas, got %#v", got)
	}
	if got := Unwrap(inner); got != tea.Model(inner) {
		t.Errorf("Unwrap must pass an unwrapped model through, got %#v", got)
	}
}

func TestBlackCanvas_delegates_updates_and_exposes_the_inner_model(t *testing.T) {
	canvas := WithBlackBackground(stubModel{view: "menu"})

	m, _ := canvas.Update(tea.KeyMsg{Type: tea.KeyDown})

	inner, ok := m.(*BlackCanvasModel).Inner().(stubModel)
	if !ok {
		t.Fatalf("Inner() did not return the wrapped model, got %T", m.(*BlackCanvasModel).Inner())
	}
	if inner.updates != 1 {
		t.Errorf("the wrapped model got %d updates, want 1", inner.updates)
	}
}
