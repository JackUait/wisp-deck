package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

// A subscription disabled in the Subscription modal (claude-configs.disabled
// sidecar next to the list file) must not appear as a switcher row: that's the
// whole point of disabling it.
func TestAccountSwitch_buildSwitchRows_skipsDisabledConfigs(t *testing.T) {
	dir := t.TempDir()
	accList := filepath.Join(dir, "claude-accounts.list")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	if err := os.WriteFile(accList, []byte("Work:work\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfgList, cfgDir := writeConfigFixtures(t)
	if _, err := claudeconfig.ToggleDisabled(claudeconfig.DisabledFile(cfgList), "mimo.json"); err != nil {
		t.Fatal(err)
	}

	rows, _ := buildSwitchRows(switchRowsInput{
		listFile:         accList,
		defaultLabelFile: defLabel,
		active:           "work",
		activeTool:       "claude",
		configsList:      cfgList,
		configsDir:       cfgDir,
	})
	// Default, Work, GLM, GPT — MiMo is disabled and hidden.
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4: %+v", len(rows), rows)
	}
	for _, r := range rows {
		if r.Config == "mimo.json" {
			t.Fatalf("disabled subscription must not get a row: %+v", r)
		}
	}
}

// The subscription THIS pane runs stays visible even when disabled — hiding it
// would drop the active marker and misreport what the pane is running.
func TestAccountSwitch_buildSwitchRows_keepsActiveDisabledConfig(t *testing.T) {
	dir := t.TempDir()
	accList := filepath.Join(dir, "claude-accounts.list")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	if err := os.WriteFile(accList, []byte("Work:work\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfgList, cfgDir := writeConfigFixtures(t)
	if _, err := claudeconfig.ToggleDisabled(claudeconfig.DisabledFile(cfgList), "glm.json"); err != nil {
		t.Fatal(err)
	}

	rows, cursor := buildSwitchRows(switchRowsInput{
		listFile:         accList,
		defaultLabelFile: defLabel,
		active:           "work",
		activeTool:       "claude",
		configsList:      cfgList,
		configsDir:       cfgDir,
		activeConfig:     "glm.json",
	})
	if rows[cursor].Config != "glm.json" {
		t.Fatalf("cursor row = %+v, want the active (disabled) GLM subscription kept visible", rows[cursor])
	}
}
