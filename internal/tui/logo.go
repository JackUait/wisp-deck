package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type logoTickMsg time.Time

type LogoModel struct {
	tool     string
	frame    int
	quitting bool
	width    int
	height   int
}

// NewLogo creates a LogoModel that displays the ghost art for the given AI tool.
func NewLogo(tool string) LogoModel {
	return LogoModel{tool: tool}
}

func (m LogoModel) Init() tea.Cmd {
	return tea.Batch(
		tickCmd(),
		tea.Tick(time.Second*2, func(t time.Time) tea.Msg {
			return quitMsg{}
		}),
	)
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Millisecond*200, func(t time.Time) tea.Msg {
		return logoTickMsg(t)
	})
}

type quitMsg struct{}

func (m LogoModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case logoTickMsg:
		m.frame = (m.frame + 1) % 2
		if !m.quitting {
			return m, tickCmd()
		}
		return m, nil

	case quitMsg:
		m.quitting = true
		return m, tea.Quit

	case tea.KeyMsg:
		m.quitting = true
		return m, tea.Quit
	}

	return m, nil
}

func (m LogoModel) View() string {
	if m.quitting {
		return ""
	}

	lines := GhostForTheme(m.tool, false, currentTheme)
	if m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, RenderGhost(lines))
	}
	return RenderGhost(lines)
}
