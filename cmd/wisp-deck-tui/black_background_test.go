package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every full-screen TUI program must run its model inside a black canvas, so the
// view is pitch black rather than the terminal theme's background color. The two
// exceptions are the floating popups: they render over a dimmed snapshot of the
// pane behind them, which IS their background.
var blackCanvasExempt = map[string]string{
	"diff_view.go":             "renders over the dimmed pane backdrop",
	"claude_account_switch.go": "renders over the dimmed pane backdrop",
}

func TestEveryProgramRunsOnABlackCanvas(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}

	newProgram := regexp.MustCompile(`tea\.NewProgram\(\s*([A-Za-z0-9_.]+)`)

	found := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range newProgram.FindAllStringSubmatch(string(src), -1) {
			found++
			if reason, exempt := blackCanvasExempt[file]; exempt {
				t.Logf("%s: exempt (%s)", file, reason)
				continue
			}
			if !strings.Contains(m[1], "tui.WithBlackBackground") {
				t.Errorf("%s: tea.NewProgram(%s) — the model must be wrapped in tui.WithBlackBackground so the view renders pitch black", file, m[1])
			}
		}
	}
	if found == 0 {
		t.Fatal("found no tea.NewProgram call sites — the guard is not checking anything")
	}
}
