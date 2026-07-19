package claudeconfig

import (
	"github.com/jackuait/wisp-deck/internal/claudeaccount"
)

// ColorFor returns the persisted 256-color index for a subscription config
// file, assigning a new one and appending it to colorsFile if the config has
// none yet. It mirrors claudeaccount.ColorFor (same palette, same file format,
// keyed by the config filename) so every identity — login or subscription —
// wears a stable color shared by the ledger pill, the switcher, and the
// statusline usage bars. avoidFiles are additional color files (e.g. the
// account colors) whose assignments the pick steers clear of, so a
// subscription never mimics a login until the palette is exhausted.
func ColorFor(colorsFile, file string, avoidFiles ...string) int {
	return claudeaccount.ColorForKey(colorsFile, file, avoidFiles...)
}
