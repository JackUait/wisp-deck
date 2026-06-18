package tui

import (
	"strings"
)

// r is the ANSI reset escape sequence.
const r = "\033[0m"

// ghostClaude returns the awake Claude ghost ASCII art (15 lines, 28 visible chars wide).
func ghostClaude(theme AIToolTheme) []string {
	O := AnsiFromThemeColor(theme.Primary)   // orange
	D := AnsiFromThemeColor(theme.Bright)    // deeper orange
	B := AnsiFromThemeColor(theme.DarkFeet)  // dark orange
	L := AnsiFromThemeColor(theme.Cap)       // peach
	W := AnsiFromThemeColor(theme.EyeWhite)  // white
	K := AnsiFromThemeColor(theme.EyePupil)  // black
	Y := AnsiFromThemeColor(theme.Accent)    // gold

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

// ghostClaudeSleeping returns the sleeping Claude ghost (dimmed colors, closed eyes).
func ghostClaudeSleeping(theme AIToolTheme) []string {
	O := AnsiFromThemeColor(theme.SleepPrimary)  // dimmed orange
	D := AnsiFromThemeColor(theme.SleepDim)      // dimmed deeper
	B := AnsiFromThemeColor(theme.SleepDarkFeet) // dimmed dark
	L := AnsiFromThemeColor(theme.SleepCap)      // dimmed peach
	K := AnsiFromThemeColor(theme.EyePupil)      // black

	return []string{
		r + "       " + L + "\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584" + r + "       ",
		r + "     " + L + "\u2584" + O + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + L + "\u2584" + r + "     ",
		r + "    " + L + "\u2584" + O + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + L + "\u2584" + r + "    ",
		r + "   " + O + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "   ",
		r + "  " + O + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588\u2588\u2588\u2588" + K + "\u25ac\u25ac\u25ac\u25ac\u25ac" + D + "\u2588\u2588\u2588\u2588\u2588\u2588" + K + "\u25ac\u25ac\u25ac\u25ac\u25ac" + D + "\u2588\u2588\u2588\u2588" + r + "  ",
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

// ghostCodex returns the awake Codex ghost ASCII art.
func ghostCodex(theme AIToolTheme) []string {
	G := AnsiFromThemeColor(theme.Primary)   // green
	Y := AnsiFromThemeColor(theme.Bright)    // yellow-green
	D := AnsiFromThemeColor(theme.DarkFeet)  // dark green
	L := AnsiFromThemeColor(theme.Cap)       // light green
	W := AnsiFromThemeColor(theme.EyeWhite)  // white
	K := AnsiFromThemeColor(theme.EyePupil)  // black
	H := AnsiFromThemeColor(theme.Accent)    // teal

	return []string{
		r + "       " + L + "\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584" + r + "       ",
		r + "     " + L + "\u2584" + G + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + L + "\u2584" + r + "     ",
		r + "    " + L + "\u2584" + G + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + L + "\u2584" + r + "    ",
		r + "   " + G + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "   ",
		r + "  " + G + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + G + "\u2588\u2588\u2588\u2588" + W + "\u2588\u2588\u2588" + K + "\u2588\u2588" + G + "\u2588\u2588\u2588\u2588\u2588\u2588" + W + "\u2588\u2588\u2588" + K + "\u2588\u2588" + G + "\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + G + "\u2588\u2588\u2588\u2588" + W + "\u2588\u2588\u2588" + K + "\u2588\u2588" + G + "\u2588\u2588\u2588\u2588\u2588\u2588" + W + "\u2588\u2588\u2588" + K + "\u2588\u2588" + G + "\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + Y + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + Y + "\u2588\u2588\u2588\u2588" + H + "\u2584\u2580\u2580\u2580\u2584" + Y + "\u2588\u2588\u2588\u2588\u2588\u2588" + H + "\u2584\u2580\u2580\u2580\u2584" + Y + "\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + Y + "\u2588\u2588\u2588\u2588" + H + "\u2588" + Y + "\u2588\u2588\u2588\u2588" + H + "\u2588" + Y + "\u2588\u2588\u2588\u2588" + H + "\u2588" + Y + "\u2588\u2588\u2588\u2588" + H + "\u2588" + Y + "\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + Y + "\u2588\u2588\u2588\u2588" + H + "\u2580\u2584\u2584\u2584\u2580" + Y + "\u2588\u2588\u2588\u2588\u2588\u2588" + H + "\u2580\u2584\u2584\u2584\u2580" + Y + "\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + Y + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588\u2588 \u2588\u2588\u2588\u2588\u2588 \u2588\u2588\u2588\u2588\u2588\u2588 \u2588\u2588\u2588\u2588\u2588 \u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588" + r + "  " + D + "\u2580\u2588\u2588\u2588\u2588\u2580" + r + " " + D + "\u2588\u2588\u2588\u2588" + r + " " + D + "\u2580\u2588\u2588\u2588\u2588\u2580" + r + "  " + D + "\u2588" + r + "  ",
	}
}

// ghostCodexSleeping returns the sleeping Codex ghost (dimmed, closed eyes).
func ghostCodexSleeping(theme AIToolTheme) []string {
	G := AnsiFromThemeColor(theme.SleepPrimary)  // dimmed green
	Y := AnsiFromThemeColor(theme.SleepDim)      // dimmed yellow-green
	D := AnsiFromThemeColor(theme.SleepDarkFeet) // dimmed dark
	L := AnsiFromThemeColor(theme.SleepCap)      // dimmed light
	K := AnsiFromThemeColor(theme.EyePupil)      // black

	return []string{
		r + "       " + L + "\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584" + r + "       ",
		r + "     " + L + "\u2584" + G + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + L + "\u2584" + r + "     ",
		r + "    " + L + "\u2584" + G + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + L + "\u2584" + r + "    ",
		r + "   " + G + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "   ",
		r + "  " + G + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + Y + "\u2588\u2588\u2588\u2588" + K + "\u25ac\u25ac\u25ac\u25ac\u25ac" + Y + "\u2588\u2588\u2588\u2588\u2588\u2588" + K + "\u25ac\u25ac\u25ac\u25ac\u25ac" + Y + "\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + Y + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + Y + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + Y + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + Y + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + Y + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + Y + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588\u2588 \u2588\u2588\u2588\u2588\u2588 \u2588\u2588\u2588\u2588\u2588\u2588 \u2588\u2588\u2588\u2588\u2588 \u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588" + r + "  " + D + "\u2580\u2588\u2588\u2588\u2588\u2580" + r + " " + D + "\u2588\u2588\u2588\u2588" + r + " " + D + "\u2580\u2588\u2588\u2588\u2588\u2580" + r + "  " + D + "\u2588" + r + "  ",
	}
}

// ghostOpencode returns the awake OpenCode ghost ASCII art.
func ghostOpencode(theme AIToolTheme) []string {
	W := AnsiFromThemeColor(theme.Bright)    // white (upper body)
	VL := AnsiFromThemeColor(theme.Cap)      // very light gray
	ML := AnsiFromThemeColor(theme.Primary)  // medium light gray
	M := AnsiFromThemeColor(theme.Dim)       // medium gray
	MD := AnsiFromThemeColor(theme.Accent)   // medium dark gray
	D := AnsiFromThemeColor(theme.DarkFeet)  // dark gray
	K := AnsiFromThemeColor(theme.EyePupil)  // near-black
	SM := AnsiFromThemeColor(theme.DarkFeet) // smile color

	return []string{
		r + "       " + VL + "\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584" + r + "       ",
		r + "     " + VL + "\u2584" + W + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + VL + "\u2584" + r + "     ",
		r + "    " + VL + "\u2584" + W + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + VL + "\u2584" + r + "    ",
		r + "   " + W + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "   ",
		r + "  " + W + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + ML + "\u2588\u2588\u2588\u2588" + W + "\u2588\u2588\u2588" + K + "\u2588\u2588" + ML + "\u2588\u2588\u2588\u2588\u2588\u2588" + W + "\u2588\u2588\u2588" + K + "\u2588\u2588" + ML + "\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + ML + "\u2588\u2588\u2588\u2588" + W + "\u2588\u2588\u2588" + K + "\u2588\u2588" + ML + "\u2588\u2588\u2588\u2588\u2588\u2588" + W + "\u2588\u2588\u2588" + K + "\u2588\u2588" + ML + "\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + M + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + M + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + M + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + SM + "\u2588\u2580\u2580\u2588" + M + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + M + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + MD + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + MD + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588\u2588 \u2588\u2588\u2588\u2588\u2588 \u2588\u2588\u2588\u2588\u2588\u2588 \u2588\u2588\u2588\u2588\u2588 \u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588" + r + "  " + D + "\u2580\u2588\u2588\u2588\u2588\u2580" + r + " " + D + "\u2588\u2588\u2588\u2588" + r + " " + D + "\u2580\u2588\u2588\u2588\u2588\u2580" + r + "  " + D + "\u2588" + r + "  ",
	}
}

// ghostOpencodeSleeping returns the sleeping OpenCode ghost (dimmed, closed eyes).
func ghostOpencodeSleeping(theme AIToolTheme) []string {
	W := AnsiFromThemeColor(theme.SleepPrimary)  // dimmed white
	VL := AnsiFromThemeColor(theme.SleepCap)     // dimmed very light
	ML := AnsiFromThemeColor(theme.SleepDim)     // dimmed medium light
	M := AnsiFromThemeColor(theme.SleepDim)      // dimmed medium
	MD := AnsiFromThemeColor(theme.SleepAccent)  // dimmed medium dark
	D := AnsiFromThemeColor(theme.SleepDarkFeet) // dimmed dark
	K := AnsiFromThemeColor(theme.EyePupil)      // black

	return []string{
		r + "       " + VL + "\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584\u2584" + r + "       ",
		r + "     " + VL + "\u2584" + W + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + VL + "\u2584" + r + "     ",
		r + "    " + VL + "\u2584" + W + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + VL + "\u2584" + r + "    ",
		r + "   " + W + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "   ",
		r + "  " + W + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + ML + "\u2588\u2588\u2588\u2588" + K + "\u25ac\u25ac\u25ac\u25ac\u25ac" + ML + "\u2588\u2588\u2588\u2588\u2588\u2588" + K + "\u25ac\u25ac\u25ac\u25ac\u25ac" + ML + "\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + ML + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + M + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + M + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + M + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + M + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + MD + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + MD + "\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588\u2588 \u2588\u2588\u2588\u2588\u2588 \u2588\u2588\u2588\u2588\u2588\u2588 \u2588\u2588\u2588\u2588\u2588 \u2588\u2588" + r + "  ",
		r + "  " + D + "\u2588" + r + "  " + D + "\u2580\u2588\u2588\u2588\u2588\u2580" + r + " " + D + "\u2588\u2588\u2588\u2588" + r + " " + D + "\u2580\u2588\u2588\u2588\u2588\u2580" + r + "  " + D + "\u2588" + r + "  ",
	}
}

// GhostForTool returns the ghost ASCII art lines for the given tool.
// Unknown tools fall back to the claude ghost.
func GhostForTool(tool string, sleeping bool) []string {
	theme := ThemeForTool(tool)
	switch tool {
	case "codex":
		if sleeping {
			return ghostCodexSleeping(theme)
		}
		return ghostCodex(theme)
	case "opencode":
		if sleeping {
			return ghostOpencodeSleeping(theme)
		}
		return ghostOpencode(theme)
	default:
		// claude and unknown tools
		if sleeping {
			return ghostClaudeSleeping(theme)
		}
		return ghostClaude(theme)
	}
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
