package tui

import (
	"strings"
)

// r is the ANSI reset escape sequence.
const r = "\033[0m"

// ghostClaude returns the awake Claude ghost ASCII art (15 lines, 28 visible chars wide).
func ghostClaude(theme AIToolTheme) []string {
	O := AnsiFromThemeColor(theme.Primary)  // orange
	D := AnsiFromThemeColor(theme.Bright)   // deeper orange
	B := AnsiFromThemeColor(theme.DarkFeet) // dark orange
	L := AnsiFromThemeColor(theme.Cap)      // peach
	W := AnsiFromThemeColor(theme.EyeWhite) // white
	K := AnsiFromThemeColor(theme.EyePupil) // black
	Y := AnsiFromThemeColor(theme.Accent)   // gold

	return []string{
		r + "       " + L + "\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584" + r + "       ",
		r + "     " + L + "\u2584" + O + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + L + "\u2584" + r + "     ",
		r + "    " + L + "\u2584" + O + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + L + "\u2584" + r + "    ",
		r + "   " + O + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "   ",
		r + "  " + O + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + O + "\u2588\u2588\u2588\u2588" + W + "\u2588\u2588\u2588" + K + "\u2588\u2588" + O + "\u2588\u2588\u2588\u2588\u2588\u2588" + W + "\u2588\u2588\u2588" + K + "\u2588\u2588" + O + "\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + O + "\u2588\u2588\u2588\u2588" + W + "\u2588\u2588\u2588" + K + "\u2588\u2588" + O + "\u2588\u2588\u2588\u2588\u2588\u2588" + W + "\u2588\u2588\u2588" + K + "\u2588\u2588" + O + "\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + Y + "\u2588\u2588" + D + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + Y + "\u2588\u2580\u2580\u2588" + D + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + Y + "\u2588\u2584\u2584\u2588" + D + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + Y + "\u2588\u2588" + D + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + B + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + B + "\u2588\u2588 \u2588\u2588\u2588\u2588\u2588 \u2588\u2588\u2588\u2588\u2588\u2588 \u2588\u2588\u2588\u2588\u2588 \u2588\u2588" + r + "  ",
		r + "  " + B + "\u2588" + r + "  " + B + "\u2580\u2588\u2588\u2588\u2588\u2580" + r + " " + B + "\u2588\u2588\u2588\u2588" + r + " " + B + "\u2580\u2588\u2588\u2588\u2588\u2580" + r + "  " + B + "\u2588" + r + "  ",
	}
}

// ghostClaudeSleeping returns the sleeping Claude ghost (dimmed colors, closed eyes, rosy cheeks).
func ghostClaudeSleeping(theme AIToolTheme) []string {
	O := AnsiFromThemeColor(theme.SleepPrimary)  // dimmed orange
	D := AnsiFromThemeColor(theme.SleepDim)      // darker lower body
	B := AnsiFromThemeColor(theme.SleepDarkFeet) // dimmed dark
	L := AnsiFromThemeColor(theme.SleepCap)      // dimmed peach
	K := AnsiFromThemeColor(theme.EyePupil)      // black
	P := AnsiFromThemeColor(theme.SleepBlush)    // rosy cheeks

	return []string{
		r + "       " + L + "\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584" + r + "       ",
		r + "     " + L + "\u2584" + O + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + L + "\u2584" + r + "     ",
		r + "    " + L + "\u2584" + O + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + L + "\u2584" + r + "    ",
		r + "   " + O + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "   ",
		r + "  " + O + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588\u2588\u2588\u2588" + K + "\u25ac\u25ac\u25ac\u25ac\u25ac" + D + "\u2588\u2588\u2588\u2588\u2588\u2588" + K + "\u25ac\u25ac\u25ac\u25ac\u25ac" + D + "\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588\u2588\u2588\u2588" + P + "\u2588\u2588" + D + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + P + "\u2588\u2588" + D + "\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + B + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + B + "\u2588\u2588 \u2588\u2588\u2588\u2588\u2588 \u2588\u2588\u2588\u2588\u2588\u2588 \u2588\u2588\u2588\u2588\u2588 \u2588\u2588" + r + "  ",
		r + "  " + B + "\u2588" + r + "  " + B + "\u2580\u2588\u2588\u2588\u2588\u2580" + r + " " + B + "\u2588\u2588\u2588\u2588" + r + " " + B + "\u2580\u2588\u2588\u2588\u2588\u2580" + r + "  " + B + "\u2588" + r + "  ",
	}
}

// paintRows converts pixel-map rows ('.' = transparent, letters = color slots)
// into ANSI art lines: each cell becomes a full block in its slot's color.
func paintRows(rows []string, colors map[byte]string) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		var b strings.Builder
		b.WriteString(r)
		var cur byte
		for j := 0; j < len(row); j++ {
			ch := row[j]
			if ch == '.' {
				b.WriteByte(' ')
				continue
			}
			if ch != cur {
				b.WriteString(colors[ch])
				cur = ch
			}
			b.WriteString("\u2588")
		}
		b.WriteString(r)
		out[i] = b.String()
	}
	return out
}

// ghostOpencode returns Brace the moth, OpenCode's mascot: curly-brace wings
// around a stout block-cursor body (28 cols, 15 rows). Slot letters:
// P wings/eye band, B head/body, C antenna dots, D stripes, A lower body,
// F feet+smile, L blush, W eye white, K pupils.
func ghostOpencode(theme AIToolTheme) []string {
	return paintRows([]string{
		".......C............C.......",
		"........P..........P........",
		".........BBBBBBBBBB.........",
		"....PPP.BBBBBBBBBBBB.PPP....",
		"...PPP..PWWKKPPWWKKP..PPP...",
		"...PPP..PWWKKPPWWKKP..PPP...",
		"..PPP...LBBBBFFBBBBL...PPP..",
		".PPP.....BBBBBBBBBB.....PPP.",
		"..PPP....DDDDDDDDDD....PPP..",
		"...PPP...BBBBBBBBBB...PPP...",
		"...PPP....DDDDDDDD....PPP...",
		"....PPP...AAAAAAAA...PPP....",
		"..........AAAAAAAA..........",
		"............AAAA............",
		"...........FF..FF...........",
	}, map[byte]string{
		'P': AnsiFromThemeColor(theme.Primary),
		'B': AnsiFromThemeColor(theme.Bright),
		'C': AnsiFromThemeColor(theme.Cap),
		'D': AnsiFromThemeColor(theme.Dim),
		'A': AnsiFromThemeColor(theme.Accent),
		'F': AnsiFromThemeColor(theme.DarkFeet),
		'L': AnsiFromThemeColor(theme.SleepBlush),
		'W': AnsiFromThemeColor(theme.EyeWhite),
		'K': AnsiFromThemeColor(theme.EyePupil),
	})
}

// ghostOpencodeSleeping returns the sleeping Brace (dimmed, closed eyes,
// blush, 16 rows \u2014 the extra row is the blush line, matching the other tools).
func ghostOpencodeSleeping(theme AIToolTheme) []string {
	return paintRows([]string{
		".......C............C.......",
		"........P..........P........",
		".........BBBBBBBBBB.........",
		"....PPP.BBBBBBBBBBBB.PPP....",
		"...PPP..PKKKPPPPKKKP..PPP...",
		"...PPP..LLBBBBBBBBLL..PPP...",
		"..PPP...BBBBBFFBBBBB...PPP..",
		".PPP.....BBBBBBBBBB.....PPP.",
		"..PPP....DDDDDDDDDD....PPP..",
		"...PPP...BBBBBBBBBB...PPP...",
		"...PPP...BBBBBBBBBB...PPP...",
		"...PPP....DDDDDDDD....PPP...",
		"....PPP...AAAAAAAA...PPP....",
		"..........AAAAAAAA..........",
		"............AAAA............",
		"...........FF..FF...........",
	}, map[byte]string{
		'P': AnsiFromThemeColor(theme.SleepPrimary),
		'B': AnsiFromThemeColor(theme.SleepPrimary),
		'C': AnsiFromThemeColor(theme.SleepCap),
		'D': AnsiFromThemeColor(theme.SleepDim),
		'A': AnsiFromThemeColor(theme.SleepAccent),
		'F': AnsiFromThemeColor(theme.SleepDarkFeet),
		'L': AnsiFromThemeColor(theme.SleepBlush),
		'K': AnsiFromThemeColor(theme.EyePupil),
	})
}

// blk returns n full-block characters.
func blk(n int) string { return strings.Repeat("█", n) }

// ghostCodex returns the awake Codex ghost (15 lines, 28 visible chars wide).
// A flat-topped mascot with a `>_` prompt emblem on its belly, distinguishing it
// from Claude's rounded cap and gold diamond. Uses the CLAUDE color-slot
// semantics (Accent = bright emblem, Bright = darker lower body) so the shared
// preset table recolors it correctly.
func ghostCodex(theme AIToolTheme) []string {
	T := AnsiFromThemeColor(theme.Primary)  // teal
	D := AnsiFromThemeColor(theme.Bright)   // deeper teal (lower body)
	B := AnsiFromThemeColor(theme.DarkFeet) // dark teal (feet)
	L := AnsiFromThemeColor(theme.Cap)      // pale mint crown
	W := AnsiFromThemeColor(theme.EyeWhite) // white
	K := AnsiFromThemeColor(theme.EyePupil) // black
	Y := AnsiFromThemeColor(theme.Accent)   // bright cyan emblem

	eyes := r + " " + T + blk(5) + W + blk(3) + K + blk(2) + T + blk(6) +
		W + blk(3) + K + blk(2) + T + blk(5) + r + " "

	return []string{
		r + "  " + L + strings.Repeat("▄", 24) + r + "  ",
		r + " " + L + "▄" + T + blk(24) + L + "▄" + r + " ",
		r + " " + T + blk(26) + r + " ",
		r + " " + T + blk(26) + r + " ",
		r + " " + T + blk(26) + r + " ",
		eyes,
		eyes,
		r + " " + D + blk(26) + r + " ",
		r + " " + D + blk(12) + Y + "▀▄" + D + blk(12) + r + " ",
		r + " " + D + blk(12) + Y + "▄▀" + D + blk(12) + r + " ",
		r + " " + D + blk(11) + Y + strings.Repeat("▄", 4) + D + blk(11) + r + " ",
		r + " " + D + blk(26) + r + " ",
		r + " " + B + blk(26) + r + " ",
		r + " " + B + blk(2) + r + " " + B + blk(6) + r + " " + B + blk(6) +
			r + " " + B + blk(6) + r + " " + B + blk(2) + r + " ",
		r + " " + B + "█" + r + "  " + B + "▀" + blk(4) + "▀" + r + " " + B + blk(6) +
			r + " " + B + "▀" + blk(4) + "▀" + r + "  " + B + "█" + r + " ",
	}
}

// ghostCodexSleeping returns the sleeping Codex ghost (dimmed, closed eyes,
// cheeks, and no prompt emblem — the terminal is idle).
func ghostCodexSleeping(theme AIToolTheme) []string {
	T := AnsiFromThemeColor(theme.SleepPrimary)  // dimmed teal
	D := AnsiFromThemeColor(theme.SleepDim)      // darker lower body
	B := AnsiFromThemeColor(theme.SleepDarkFeet) // dimmed dark
	L := AnsiFromThemeColor(theme.SleepCap)      // dimmed mint
	K := AnsiFromThemeColor(theme.EyePupil)      // black
	P := AnsiFromThemeColor(theme.SleepBlush)    // cheeks

	return []string{
		r + "  " + L + strings.Repeat("▄", 24) + r + "  ",
		r + " " + L + "▄" + T + blk(24) + L + "▄" + r + " ",
		r + " " + T + blk(26) + r + " ",
		r + " " + T + blk(26) + r + " ",
		r + " " + T + blk(26) + r + " ",
		r + " " + D + blk(4) + K + strings.Repeat("▬", 5) + D + blk(8) +
			K + strings.Repeat("▬", 5) + D + blk(4) + r + " ",
		r + " " + D + blk(4) + P + blk(2) + D + blk(14) + P + blk(2) + D + blk(4) + r + " ",
		r + " " + D + blk(26) + r + " ",
		r + " " + D + blk(26) + r + " ",
		r + " " + D + blk(26) + r + " ",
		r + " " + D + blk(26) + r + " ",
		r + " " + D + blk(26) + r + " ",
		r + " " + B + blk(26) + r + " ",
		r + " " + B + blk(26) + r + " ",
		r + " " + B + blk(2) + r + " " + B + blk(6) + r + " " + B + blk(6) +
			r + " " + B + blk(6) + r + " " + B + blk(2) + r + " ",
		r + " " + B + "█" + r + "  " + B + "▀" + blk(4) + "▀" + r + " " + B + blk(6) +
			r + " " + B + "▀" + blk(4) + "▀" + r + "  " + B + "█" + r + " ",
	}
}

// GhostForTheme returns the ghost ASCII art lines for the given tool, painted
// with the supplied palette. The tool selects the mascot SHAPE (claude vs
// opencode vs codex); the theme supplies the COLORS — so a user-selected preset
// recolors any mascot. Unknown tools fall back to the claude shape.
func GhostForTheme(tool string, sleeping bool, theme AIToolTheme) []string {
	switch tool {
	case "opencode":
		if sleeping {
			return ghostOpencodeSleeping(theme)
		}
		return ghostOpencode(theme)
	case "codex":
		if sleeping {
			return ghostCodexSleeping(theme)
		}
		return ghostCodex(theme)
	default:
		// claude and unknown tools
		if sleeping {
			return ghostClaudeSleeping(theme)
		}
		return ghostClaude(theme)
	}
}

// GhostForTool returns the ghost ASCII art lines for the given tool using that
// tool's own default palette (the "auto" theme). For a user-selected preset,
// use GhostForTheme with the resolved theme instead.
func GhostForTool(tool string, sleeping bool) []string {
	return GhostForTheme(tool, sleeping, ThemeForTool(tool))
}

// RenderGhost joins ghost lines with newlines into a single string.
func RenderGhost(lines []string) string {
	return strings.Join(lines, "\n")
}

// RenderZzz returns a "z Z Z" sleeping indicator.
// For animated rendering, use ZzzAnimation directly.
func RenderZzz() string {
	z := NewZzzAnimation()
	return z.View()
}
