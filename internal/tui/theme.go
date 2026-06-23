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
	// UIAccent is the chrome accent for popup furniture (the diff pager's border,
	// rule, active tab, icons, title). Kept separate from the ghost-shading colors
	// so the window chrome can be tuned without touching the mascot.
	UIAccent lipgloss.Color
	SleepPrimary  lipgloss.Color
	SleepAccent   lipgloss.Color
	SleepBlush    lipgloss.Color
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
		UIAccent:      lipgloss.Color("208"), // orange — the popup chrome color
		SleepPrimary:  lipgloss.Color("166"),
		SleepAccent:   lipgloss.Color("178"),
		SleepBlush:    lipgloss.Color("168"),
		SleepDim:      lipgloss.Color("130"),
		SleepDarkFeet: lipgloss.Color("94"),
		SleepCap:      lipgloss.Color("180"),
		Text:          lipgloss.Color("223"),
	},
	"opencode": {
		Name:          "opencode",
		Primary:       lipgloss.Color("141"), // #af87ff brand purple — gauge fill, title, eye band
		Dim:           lipgloss.Color("99"),  // #875fff — stats border, ghost mid-body
		Bright:        lipgloss.Color("147"), // #afafff — ghost upper body
		Accent:        lipgloss.Color("61"),  // #5f5faf — ghost lower band
		Cap:           lipgloss.Color("183"), // #dfafff — pale crown rim
		DarkFeet:      lipgloss.Color("60"),  // #5f5f87 — feet + smile
		EyeWhite:      lipgloss.Color("147"),
		EyePupil:      lipgloss.Color("235"), // near-black pupils
		UIAccent:      lipgloss.Color("141"), // purple — the popup chrome color
		SleepPrimary:  lipgloss.Color("103"), // #8787af — dim body
		SleepAccent:   lipgloss.Color("61"),  // #5f5faf — dim lower band
		SleepBlush:    lipgloss.Color("139"), // #af87af — mauve cheeks
		SleepDim:      lipgloss.Color("60"),  // #5f5f87
		SleepDarkFeet: lipgloss.Color("236"), // dim feet
		SleepCap:      lipgloss.Color("146"), // #afafd7 — dim rim
		Text:          lipgloss.Color("189"), // #d7d7ff
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
	applyDiffChrome(theme.UIAccent)
}
