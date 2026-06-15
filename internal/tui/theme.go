package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// AIToolTheme defines the color palette for an AI tool's TUI appearance.
type AIToolTheme struct {
	Name          string
	Primary       lipgloss.Color
	Dim           lipgloss.Color
	Bright        lipgloss.Color
	Accent        lipgloss.Color
	Cap           lipgloss.Color
	DarkFeet      lipgloss.Color
	EyeWhite      lipgloss.Color
	EyePupil      lipgloss.Color
	SleepPrimary  lipgloss.Color
	SleepAccent   lipgloss.Color
	SleepDim      lipgloss.Color
	SleepDarkFeet lipgloss.Color
	SleepCap      lipgloss.Color
	Text          lipgloss.Color
}

var themes = map[string]AIToolTheme{
	"claude": {
		Name:          "claude",
		Primary:       lipgloss.Color("209"),
		Dim:           lipgloss.Color("166"),
		Bright:        lipgloss.Color("208"),
		Accent:        lipgloss.Color("220"),
		Cap:           lipgloss.Color("223"),
		DarkFeet:      lipgloss.Color("166"),
		EyeWhite:      lipgloss.Color("255"),
		EyePupil:      lipgloss.Color("232"),
		SleepPrimary:  lipgloss.Color("166"),
		SleepAccent:   lipgloss.Color("178"),
		SleepDim:      lipgloss.Color("166"),
		SleepDarkFeet: lipgloss.Color("94"),
		SleepCap:      lipgloss.Color("180"),
		Text:          lipgloss.Color("223"),
	},
	"codex": {
		Name:          "codex",
		Primary:       lipgloss.Color("114"),
		Dim:           lipgloss.Color("71"),
		Bright:        lipgloss.Color("113"),
		Accent:        lipgloss.Color("78"),
		Cap:           lipgloss.Color("157"),
		DarkFeet:      lipgloss.Color("71"),
		EyeWhite:      lipgloss.Color("255"),
		EyePupil:      lipgloss.Color("232"),
		SleepPrimary:  lipgloss.Color("71"),
		SleepAccent:   lipgloss.Color("65"),
		SleepDim:      lipgloss.Color("71"),
		SleepDarkFeet: lipgloss.Color("58"),
		SleepCap:      lipgloss.Color("114"),
		Text:          lipgloss.Color("157"),
	},
	"copilot": {
		Name:          "copilot",
		Primary:       lipgloss.Color("141"),
		Dim:           lipgloss.Color("98"),
		Bright:        lipgloss.Color("140"),
		Accent:        lipgloss.Color("134"),
		Cap:           lipgloss.Color("183"),
		DarkFeet:      lipgloss.Color("98"),
		EyeWhite:      lipgloss.Color("255"),
		EyePupil:      lipgloss.Color("232"),
		SleepPrimary:  lipgloss.Color("98"),
		SleepAccent:   lipgloss.Color("96"),
		SleepDim:      lipgloss.Color("98"),
		SleepDarkFeet: lipgloss.Color("60"),
		SleepCap:      lipgloss.Color("140"),
		Text:          lipgloss.Color("183"),
	},
	"opencode": {
		Name:          "opencode",
		Primary:       lipgloss.Color("250"),
		Dim:           lipgloss.Color("244"),
		Bright:        lipgloss.Color("255"),
		Accent:        lipgloss.Color("240"),
		Cap:           lipgloss.Color("252"),
		DarkFeet:      lipgloss.Color("240"),
		EyeWhite:      lipgloss.Color("255"),
		EyePupil:      lipgloss.Color("238"),
		SleepPrimary:  lipgloss.Color("244"),
		SleepAccent:   lipgloss.Color("234"),
		SleepDim:      lipgloss.Color("236"),
		SleepDarkFeet: lipgloss.Color("232"),
		SleepCap:      lipgloss.Color("242"),
		Text:          lipgloss.Color("252"),
	},
}

// currentTheme is the palette last applied via ApplyTheme. Components that need
// the full palette (not just the package-level title/selected styles) read this.
// Defaults to claude so rendering works even before ApplyTheme is called (tests).
var currentTheme = themes["claude"]

// ThemeForTool returns the color theme for the given AI tool.
// Unknown tools fall back to the claude theme.
func ThemeForTool(tool string) AIToolTheme {
	if theme, ok := themes[tool]; ok {
		return theme
	}
	return themes["claude"]
}

// AnsiFromThemeColor converts a lipgloss.Color (ANSI 256 string) to an
// ANSI escape sequence. This bridges lipgloss theme colors with raw
// escape-code rendering used by ghost ASCII art.
func AnsiFromThemeColor(c lipgloss.Color) string {
	return fmt.Sprintf("\033[38;5;%sm", string(c))
}

// ApplyTheme updates the package-level styles (titleStyle, selectedItemStyle,
// questionStyle) to use the given theme's Primary color. Call this before
// creating any TUI model so that all components reflect the AI tool's colors.
func ApplyTheme(theme AIToolTheme) {
	currentTheme = theme
	titleStyle = lipgloss.NewStyle().Foreground(theme.Primary).Bold(true)
	selectedItemStyle = lipgloss.NewStyle().Foreground(theme.Primary)
	questionStyle = lipgloss.NewStyle().Foreground(theme.Primary).Bold(true)
}
