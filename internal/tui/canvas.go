package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// pitchBlack is the background color every full-screen view is painted with, so
// the UI reads the same regardless of the terminal theme's own background.
const pitchBlack = "#000000"

const ansiReset = "\x1b[0m"

// blackSequence is the SGR sequence that sets the background to pitch black —
// empty when the terminal has no color support, in which case nothing is painted.
func blackSequence() string {
	c := lipgloss.ColorProfile().Color(pitchBlack)
	if c == nil {
		return ""
	}
	return termenv.CSI + c.Sequence(true) + "m"
}

// FillBackground paints a rendered frame onto a pitch-black screen of the given
// size: short lines are padded out to width, missing lines are added up to
// height, and black is re-applied after every reset a nested style emits — a
// reset would otherwise hand the rest of the line back to the terminal's own
// background color.
func FillBackground(frame string, width, height int) string {
	if frame == "" {
		return "" // a quitting model draws nothing; painting would flash a black screen
	}
	black := blackSequence()
	if black == "" {
		return frame
	}

	lines := strings.Split(frame, "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i, line := range lines {
		if pad := width - lipgloss.Width(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		lines[i] = black + strings.ReplaceAll(line, ansiReset, ansiReset+black) + ansiReset
	}
	return strings.Join(lines, "\n")
}

// BlackCanvasModel wraps a screen-owning model and renders it on a pitch-black
// canvas. It is a pass-through in every other respect: the wrapped model still
// sees every message, including the WindowSizeMsg the canvas sizes itself from.
type BlackCanvasModel struct {
	inner  tea.Model
	width  int
	height int
}

// WithBlackBackground wraps a model so its view is painted pitch black.
func WithBlackBackground(m tea.Model) *BlackCanvasModel {
	return &BlackCanvasModel{inner: m}
}

// Inner returns the wrapped model — call sites that read a result off the final
// model returned by Program.Run() unwrap it through this.
func (b *BlackCanvasModel) Inner() tea.Model { return b.inner }

// Unwrap peels the black canvas off a model, so a call site can type-assert the
// model returned by Program.Run() to its own concrete type. A model that was
// never wrapped passes through untouched.
func Unwrap(m tea.Model) tea.Model {
	if b, ok := m.(*BlackCanvasModel); ok {
		return b.Inner()
	}
	return m
}

func (b *BlackCanvasModel) Init() tea.Cmd { return b.inner.Init() }

func (b *BlackCanvasModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		b.width, b.height = ws.Width, ws.Height
	}
	inner, cmd := b.inner.Update(msg)
	b.inner = inner
	return b, cmd
}

func (b *BlackCanvasModel) View() string {
	return FillBackground(b.inner.View(), b.width, b.height)
}
