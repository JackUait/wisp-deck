package claudeaccount

import (
	"math/rand"
	"os"
	"strconv"
	"strings"
)

// Palette is the set of 256-color indices a Claude account's identity can wear.
// They are used verbatim in both Go (lipgloss.Color) and bash (\033[38;5;Nm), so
// the statusline and the TUI menu agree on an account's color. The set is a
// spread of distinct, readable-on-dark hues; the exact members must match
// GT_ACCOUNT_PALETTE in lib/statusline.sh.
var Palette = []int{39, 208, 170, 78, 203, 141, 43, 220, 205, 75, 156, 214}

// normalizeDir maps the implicit Default login (empty dir) onto the shared
// "default" key so bash and Go persist it in the same slot.
func normalizeDir(dir string) string {
	if dir == "" {
		return "default"
	}
	return dir
}

// LoadColors parses a dir:index color file into a map, skipping blank lines,
// comment lines (leading '#'), lines without a colon, and lines whose index is
// not an integer. Returns an empty map if the file cannot be read.
func LoadColors(file string) map[string]int {
	out := map[string]int{}
	data, err := os.ReadFile(file)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(line[i+1:]))
		if err != nil {
			continue
		}
		out[line[:i]] = n
	}
	return out
}

// ColorFor returns the persisted 256-color index for an account dir, assigning a
// new one and appending it to file if the dir has none yet. The new color is
// picked at random from the palette entries not already used by another account
// — nor by any identity persisted in the avoidFiles (e.g. the subscription
// colors), so logins and subscriptions never collide — until the palette is
// exhausted, after which it falls back to a random palette member. The empty dir
// (implicit Default) is keyed under "default". The file is the source of truth:
// once assigned, the color is stable across calls and shared with the statusline.
func ColorFor(file, dir string, avoidFiles ...string) int {
	return ColorForKey(file, normalizeDir(dir), avoidFiles...)
}

// ColorForKey is ColorFor without the Default-dir normalization: the shared
// assignment primitive for any identity keyed in a dir:index colors file.
// claudeconfig.ColorFor delegates here so accounts and subscriptions draw from
// one palette with mutual avoidance.
func ColorForKey(file, key string, avoidFiles ...string) int {
	colors := LoadColors(file)
	if c, ok := colors[key]; ok {
		return c
	}

	used := map[int]bool{}
	for _, c := range colors {
		used[c] = true
	}
	for _, avoid := range avoidFiles {
		for _, c := range LoadColors(avoid) {
			used[c] = true
		}
	}
	var avail []int
	for _, c := range Palette {
		if !used[c] {
			avail = append(avail, c)
		}
	}
	pool := avail
	if len(pool) == 0 {
		pool = Palette
	}
	pick := pool[rand.Intn(len(pool))]

	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		_, _ = f.WriteString(key + ":" + strconv.Itoa(pick) + "\n")
		_ = f.Close()
	}
	return pick
}
