package claudeconfig

import (
	"math/rand"
	"os"
	"strconv"

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
	colors := claudeaccount.LoadColors(colorsFile)
	if c, ok := colors[file]; ok {
		return c
	}

	used := map[int]bool{}
	for _, c := range colors {
		used[c] = true
	}
	for _, avoid := range avoidFiles {
		for _, c := range claudeaccount.LoadColors(avoid) {
			used[c] = true
		}
	}
	var avail []int
	for _, c := range claudeaccount.Palette {
		if !used[c] {
			avail = append(avail, c)
		}
	}
	pool := avail
	if len(pool) == 0 {
		pool = claudeaccount.Palette
	}
	pick := pool[rand.Intn(len(pool))]

	f, err := os.OpenFile(colorsFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		_, _ = f.WriteString(file + ":" + strconv.Itoa(pick) + "\n")
		_ = f.Close()
	}
	return pick
}
