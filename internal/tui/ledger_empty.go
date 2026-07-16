package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ledgerEmptyCaption labels the clean-tree placeholder.
const ledgerEmptyCaption = "working tree clean"

// ledgerWispPixels is the clean-tree mascot as a 14x12 pixel map: '.' is
// transparent, every letter is a colour slot. Rendered with half-block glyphs
// (two pixels per cell) it occupies 14 columns and 6 rows — an eighth of the
// area of the full-size splash ghost (GhostForTheme), which filled half the
// pane and shouted over the changesets the ledger exists to show.
//
// The splash mascot's eye whites and belly emblem are deliberately dropped: at
// this scale they are 2-3 pixels of pure noise. The rows are laid out so each
// feature lands on a cell boundary — the eyes fill one cell row exactly, and
// the body/lower-body seam falls mid-cell, where a half block renders it as a
// crisp shading step rather than a stack of identical slabs.
// Slots: P body, D lower body, F feet, K pupils.
var ledgerWispPixels = []string{
	"....PPPPPP....",
	"..PPPPPPPPPP..",
	".PPPPPPPPPPPP.",
	"PPPPPPPPPPPPPP",
	"PPKKPPPPPPKKPP",
	"PPKKPPPPPPKKPP",
	"PPPPPPPPPPPPPP",
	"DDDDDDDDDDDDDD",
	"DDDDDDDDDDDDDD",
	"DDDDDDDDDDDDDD",
	"FFFFFFFFFFFFFF",
	"FF.FFF..FFF.FF",
}

// ledgerWispWidth is the mascot's column count (one cell per pixel column).
const ledgerWispWidth = 14

// renderWispHalfBlocks paints a pixel map with half-block glyphs, packing two
// pixel rows into each character cell: '▀' carries the top pixel as its
// foreground and the bottom pixel as its background. A transparent pixel falls
// back to the terminal's default colour, so the pane's background shows through
// instead of a painted box.
//
// Every cell is emitted with an explicit reset first: a background colour left
// set would bleed across the transparent margin and draw a dark slab around the
// mascot.
func renderWispHalfBlocks(pixels []string, colors map[byte]lipgloss.Color) []string {
	const reset = "\033[0m"
	fg := func(c lipgloss.Color) string { return "\033[38;5;" + string(c) + "m" }
	bg := func(c lipgloss.Color) string { return "\033[48;5;" + string(c) + "m" }

	lines := make([]string, 0, (len(pixels)+1)/2)
	for row := 0; row+1 < len(pixels); row += 2 {
		top, bottom := pixels[row], pixels[row+1]
		var b strings.Builder
		for col := 0; col < len(top); col++ {
			t, d := top[col], bottom[col]
			tSet, dSet := t != '.', d != '.'
			b.WriteString(reset)
			switch {
			case !tSet && !dSet:
				b.WriteByte(' ')
			case tSet && !dSet:
				b.WriteString(fg(colors[t]) + "▀")
			case !tSet && dSet:
				b.WriteString(fg(colors[d]) + "▄")
			case t == d:
				b.WriteString(fg(colors[t]) + "█")
			default:
				b.WriteString(fg(colors[t]) + bg(colors[d]) + "▀")
			}
		}
		b.WriteString(reset)
		lines = append(lines, b.String())
	}
	return lines
}

// ledgerWisp returns the clean-tree mascot in the theme's MUTED (sleep)
// palette. The placeholder marks an idle state, so it recedes rather than
// competing with the file rows; the full-saturation palette belongs to the
// splash, where the mascot is the point.
//
// Each slot is deliberately shifted one stop DARKER than the sleeping splash
// ghost uses it — straight sleep tones still read as a bright blob against the
// pane. The crown highlight is dropped entirely: a lighter rim on a darker body
// is exactly the detail that catches the eye. The dome reads from its
// silhouette instead, leaving a flat two-tone wisp near the caption's weight.
func ledgerWisp(theme AIToolTheme) []string {
	return renderWispHalfBlocks(ledgerWispPixels, map[byte]lipgloss.Color{
		'P': theme.SleepDim,
		'D': theme.SleepDarkFeet,
		'F': theme.SleepDarkFeet,
		'K': theme.EyePupil,
	})
}

// renderLedgerEmptyState draws the clean-tree placeholder: a small, muted wisp
// centered in the body viewport with a caption beneath it. It replaces the bare
// " no changes" label that used to sit alone in the pane's top-left corner.
//
// A pane too narrow or too short for the art degrades to the caption alone —
// wrapping the mascot would smear it across rows, and overflowing the viewport
// would push the footer off the pane.
func renderLedgerEmptyState(theme AIToolTheme, width, height int) []string {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Faint(true)
	captionLine := func() string {
		pad := (width - lipgloss.Width(ledgerEmptyCaption)) / 2
		if pad < 1 {
			pad = 1
		}
		return strings.Repeat(" ", pad) + dim.Render(ledgerEmptyCaption)
	}

	art := ledgerWisp(theme)
	total := len(art) + 2 // art + blank spacer + caption

	if width < ledgerWispWidth+2 || height < total {
		lines := make([]string, 0, height)
		for i := 0; i < (height-1)/2; i++ {
			lines = append(lines, "")
		}
		return append(lines, captionLine())
	}

	pad := strings.Repeat(" ", (width-ledgerWispWidth)/2)
	lines := make([]string, 0, height)
	for i := 0; i < (height-total)/2; i++ {
		lines = append(lines, "")
	}
	for _, line := range art {
		lines = append(lines, pad+line)
	}
	return append(lines, "", captionLine())
}
