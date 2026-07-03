package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadStatsModePref reads the saved stats_mode preference from the settings
// file so the Stats view restores the user's last-chosen mode across sessions.
func TestReadStatsModePref(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	sf := filepath.Join(dir, "wisp-deck", "settings")
	if err := os.MkdirAll(filepath.Dir(sf), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sf, []byte("theme=auto\nstats_mode=compact\npanel_mode=compact\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := readStatsModePref(); got != "compact" {
		t.Errorf("readStatsModePref() = %q, want %q", got, "compact")
	}
}

// TestReadStatsModePref_missing returns "" when the setting is absent, leaving the
// model's default (full) in place.
func TestReadStatsModePref_missing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if got := readStatsModePref(); got != "" {
		t.Errorf("readStatsModePref() with no file = %q, want empty", got)
	}
}
