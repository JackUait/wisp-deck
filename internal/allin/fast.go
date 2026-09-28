package allin

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

// fastClaudeModel is the model Claude Code picks for background calls when
// it talks to Anthropic directly.
const fastClaudeModel = "claude-haiku-4-5-20251001"

// fastTarget keeps a background call on row's source. It must never pick
// another source: that would bill a subscription the session did not choose.
func fastTarget(row Target, configFast func(source string) string) Target {
	switch row.Kind {
	case KindAccount:
		return Target{Kind: KindAccount, Source: row.Source, Model: fastClaudeModel}
	case KindConfig:
		model := ""
		if configFast != nil {
			model = configFast(row.Source)
		}
		if model == "" {
			model = row.Model
		}
		return Target{Kind: KindConfig, Source: row.Source, Model: model}
	}
	return Target{Kind: KindSession, Model: fastClaudeModel}
}

// FastModelFor returns a profile's own fast model, or "" when it has none.
// The source comes off the wire, so it is checked before it names a file.
func (e Env) FastModelFor(source string) string {
	if validSource(source) != nil {
		return ""
	}
	return claudeconfig.ReadFastModel(e.ConfigsDir, source+".json")
}

// StartingRow is the row a session opens on. Claude Code asks for the title
// before the first turn, so the router has no row of its own yet. Anything
// unreadable gives the zero Target: the session's own login.
func StartingRow(settingsPath string) Target {
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return Target{}
	}
	var settings struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return Target{}
	}
	row := Route(settings.Model)
	if row.Kind == KindFast {
		return Target{}
	}
	return row
}

// UserSettingsPath is the settings file the session reads its model from.
// Every account dir links its settings.json to ~/.claude/settings.json.
func UserSettingsPath() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "settings.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}
