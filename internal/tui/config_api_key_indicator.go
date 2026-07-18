package tui

import (
	"fmt"

	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

// configAPIKeyIndicator returns a display string showing mapping status for a
// config. Mappings are counted against the config's own provider models, so a
// value belonging to a different provider isn't mis-counted.
func configAPIKeyIndicator(configsDir, file, name string) string {
	if file == "" {
		return ""
	}
	provider := claudeconfig.ProviderForConfig(configsDir, claudeconfig.Config{Name: name, File: file})
	mappings := claudeconfig.ReadModelMappings(configsDir, file, claudeconfig.ProviderModels[provider.Key])
	mapped := 0
	for _, v := range mappings {
		if v >= 0 {
			mapped++
		}
	}
	if mapped > 0 {
		return fmt.Sprintf("%d mapped", mapped)
	}
	return "unmapped"
}
