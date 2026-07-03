package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jackuait/wisp-deck/internal/tui"
)

// Version is set at build time via -ldflags "-X main.Version=X.Y.Z"
var Version = "dev"

var (
	aiToolFlag string
	themeFlag  string
)

var rootCmd = &cobra.Command{
	Use:   "wisp-deck-tui",
	Short: "Interactive TUI components for Wisp Deck",
	Long:  "Provides terminal UI components for Wisp Deck project selector, AI tool picker, and settings menu.",
}

func init() {
	rootCmd.Version = Version
	rootCmd.PersistentFlags().StringVar(&aiToolFlag, "ai-tool", "claude", "AI tool for theming")
	rootCmd.PersistentFlags().StringVar(&themeFlag, "theme", "", "Theme preset (auto or a preset name); empty reads the saved setting")
}

// settingsFilePath returns the path to the user's wisp-deck settings file.
func settingsFilePath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "wisp-deck", "settings")
}

// readThemePref reads the saved "theme=" preference from the settings file, or
// "" if unset/unreadable.
func readThemePref() string {
	f, err := os.Open(settingsFilePath())
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if v, ok := strings.CutPrefix(line, "theme="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// readStatsModePref reads the saved "stats_mode=" preference from the settings
// file, or "" if unset/unreadable. Used to restore the Stats view's Full/Compact
// mode across sessions.
func readStatsModePref() string {
	f, err := os.Open(settingsFilePath())
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if v, ok := strings.CutPrefix(line, "stats_mode="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// effectiveThemePref resolves the theme preference: the explicit --theme flag
// wins; otherwise the saved setting; otherwise "auto".
func effectiveThemePref() string {
	if themeFlag != "" {
		return themeFlag
	}
	if pref := readThemePref(); pref != "" {
		return pref
	}
	return "auto"
}

// effectiveTheme resolves the palette for a command from the active tool and the
// effective theme preference (a chosen preset overrides the per-tool default).
func effectiveTheme(tool string) tui.AIToolTheme {
	return tui.ResolveTheme(tool, effectiveThemePref())
}
