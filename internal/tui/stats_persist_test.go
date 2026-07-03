package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readSetting returns the value of key=... from a wisp-deck settings file, or "".
func readSetting(t *testing.T, path, key string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), key+"="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// TestSetStatsCompact_persistsMode verifies that switching the Stats view mode
// writes the choice to the settings file so it survives across sessions. Both the
// keyboard toggle and the mouse click funnel through setStatsCompact, so persisting
// here covers every way a user can target the toggle.
func TestSetStatsCompact_persistsMode(t *testing.T) {
	dir := t.TempDir()
	sf := filepath.Join(dir, "settings")
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetSettingsFile(sf)

	m.setStatsCompact(true)
	if got := readSetting(t, sf, "stats_mode"); got != "compact" {
		t.Errorf("after compact: stats_mode=%q, want %q", got, "compact")
	}

	m.setStatsCompact(false)
	if got := readSetting(t, sf, "stats_mode"); got != "full" {
		t.Errorf("after full: stats_mode=%q, want %q", got, "full")
	}
}

// TestStatsMode_clickPersists verifies that targeting the toggle with the mouse
// (clicking the Compact label) persists the choice, not just an in-memory flip.
func TestStatsMode_clickPersists(t *testing.T) {
	dir := t.TempDir()
	sf := filepath.Join(dir, "settings")
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetSettingsFile(sf)
	m.SetActiveTab(TabStats)
	m.SetSize(120, 40)
	updated, _ := m.Update(statsLoadedMsg{months: statsMonthWithModels()})
	mm := updated.(*MainMenuModel)
	_ = mm.renderStatsBox()

	row := mm.statsModeRowIndex()
	ranges := statsModeHitRanges()
	mid := (ranges[1][0] + ranges[1][1]) / 2
	mm.clickTarget(mm.HitTest(mid, row))

	if got := readSetting(t, sf, "stats_mode"); got != "compact" {
		t.Errorf("clicking Compact persisted stats_mode=%q, want %q", got, "compact")
	}
}

// TestSetStatsMode_restores verifies the saved preference seeds the initial view
// mode on launch, so a returning user lands on the mode they last chose.
func TestSetStatsMode_restores(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetStatsMode("compact")
	if !m.statsCompact {
		t.Errorf("SetStatsMode(compact) should start in compact mode")
	}
	m.SetStatsMode("full")
	if m.statsCompact {
		t.Errorf("SetStatsMode(full) should start in full mode")
	}
	// Unknown/empty values leave the default (full) untouched.
	m.SetStatsMode("")
	if m.statsCompact {
		t.Errorf("SetStatsMode(\"\") should not change the default full mode")
	}
}

// TestSetStatsMode_doesNotResaveOnLaunch guards against a launch-time restore
// re-writing the settings file (which would be a pointless write and could race
// with other readers). Seeding the mode must not touch the file.
func TestSetStatsMode_doesNotResaveOnLaunch(t *testing.T) {
	dir := t.TempDir()
	sf := filepath.Join(dir, "settings")
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetSettingsFile(sf)
	m.SetStatsMode("compact")
	if _, err := os.Stat(sf); err == nil {
		t.Errorf("SetStatsMode should not write the settings file on launch")
	}
}
