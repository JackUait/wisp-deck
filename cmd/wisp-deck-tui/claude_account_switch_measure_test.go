package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// --measure lets bash size the tmux popup to the switcher CARD itself (so the
// switcher floats as a small card inside the agent pane instead of covering the
// whole pane with a dimmed overlay). It prints "cols rows" and exits without
// ever starting the TUI, so it must work with no TTY at all.
func TestClaudeAccountSwitch_measurePrintsCardSize(t *testing.T) {
	flag := claudeAccountSwitchCmd.Flags().Lookup("measure")
	if flag == nil {
		t.Fatal("missing --measure flag")
	}
	if !flag.Hidden {
		t.Fatal("--measure is a bash sizing seam and must stay hidden from help")
	}

	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	if err := os.WriteFile(list, []byte("Work:work\nHome:home\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	origList, origPointer, origDefaultLabel := casList, casPointer, casDefaultLabel
	origTools, origActiveTool := casTools, casActiveTool
	origConfigs, origConfigsDir, origActiveConfig := casConfigs, casConfigsDir, casActiveConfig
	origColors, origBackdrop, origResultFile := casColors, casBackdrop, casResultFile
	t.Cleanup(func() {
		casList, casPointer, casDefaultLabel = origList, origPointer, origDefaultLabel
		casTools, casActiveTool = origTools, origActiveTool
		casConfigs, casConfigsDir, casActiveConfig = origConfigs, origConfigsDir, origActiveConfig
		casColors, casBackdrop, casResultFile = origColors, origBackdrop, origResultFile
		casMeasure = false
	})
	casList = list
	casPointer = filepath.Join(dir, "claude-account")
	casDefaultLabel = ""
	casTools, casActiveTool = "", ""
	casConfigs, casConfigsDir, casActiveConfig = "", "", ""
	casColors, casBackdrop, casResultFile = "", "", ""
	casMeasure = true

	var out strings.Builder
	claudeAccountSwitchCmd.SetOut(&out)
	t.Cleanup(func() { claudeAccountSwitchCmd.SetOut(nil) })
	if err := runClaudeAccountSwitch(claudeAccountSwitchCmd, nil); err != nil {
		t.Fatalf("measure run: %v", err)
	}

	fields := strings.Fields(strings.TrimSpace(out.String()))
	if len(fields) != 2 {
		t.Fatalf("measure output = %q, want \"cols rows\"", out.String())
	}
	w, errW := strconv.Atoi(fields[0])
	h, errH := strconv.Atoi(fields[1])
	if errW != nil || errH != nil {
		t.Fatalf("measure output not numeric: %q", out.String())
	}

	// The printed size must match the card geometry View actually renders.
	rows, cursor := switchRowsForActive(list, "", "")
	model := newAccountSwitchModel(rows, cursor, "")
	wantW := model.contentWidth() + 2*accountSwitchPadX + 2*accountSwitchBorder
	wantH := len(model.innerLines()) + accountSwitchPadY + accountSwitchPadBottom + 2*accountSwitchBorder
	if w != wantW || h != wantH {
		t.Fatalf("measure = %dx%d, want %dx%d", w, h, wantW, wantH)
	}
	// 3 rows (Default, Work, Home) + blank + help = 5 inner lines; +1 pad row
	// and 2 border rows = 8. A regression here means the layout constants moved
	// without --measure following.
	if h != 8 {
		t.Fatalf("card height = %d, want 8 for 3 ungrouped rows", h)
	}
}
