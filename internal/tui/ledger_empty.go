package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ledgerEmptyCaption labels the clean-tree placeholder.
const ledgerEmptyCaption = "working tree clean"

// renderLedgerEmptyState draws the clean-tree placeholder: the session tool's
// mascot centered in the body viewport with a caption beneath it. It replaces
// the bare " no changes" label that used to sit alone in the pane's top-left
// corner.
//
// A pane too narrow or too short for the art degrades to the caption alone —
// wrapping the mascot would smear it across rows, and overflowing the viewport
// would push the footer off the pane.
func renderLedgerEmptyState(tool string, theme AIToolTheme, width, height int) []string {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Faint(true)
	captionLine := func() string {
		pad := (width - lipgloss.Width(ledgerEmptyCaption)) / 2
		if pad < 1 {
			pad = 1
		}
		return strings.Repeat(" ", pad) + dim.Render(ledgerEmptyCaption)
	}

	art := GhostForTheme(tool, false, theme)
	artWidth := 0
	for _, line := range art {
		if w := lipgloss.Width(line); w > artWidth {
			artWidth = w
		}
	}
	total := len(art) + 2 // art + blank spacer + caption

	if width < artWidth+2 || height < total {
		lines := make([]string, 0, height)
		for i := 0; i < (height-1)/2; i++ {
			lines = append(lines, "")
		}
		return append(lines, captionLine())
	}

	pad := strings.Repeat(" ", (width-artWidth)/2)
	lines := make([]string, 0, height)
	for i := 0; i < (height-total)/2; i++ {
		lines = append(lines, "")
	}
	for _, line := range art {
		lines = append(lines, pad+line)
	}
	return append(lines, "", captionLine())
}
