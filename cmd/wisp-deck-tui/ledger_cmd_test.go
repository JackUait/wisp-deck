package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/tui"
)

func TestLedgerCommandRegisteredWithExactProjectArgument(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"ledger"})
	if err != nil {
		t.Fatalf("find ledger command: %v", err)
	}
	if cmd.Name() != "ledger" {
		t.Fatalf("command name = %q, want ledger", cmd.Name())
	}
	if err := cmd.Args(cmd, nil); err == nil {
		t.Fatal("ledger accepted no project directory")
	}
	if err := cmd.Args(cmd, []string{"one"}); err != nil {
		t.Fatalf("ledger rejected one project directory: %v", err)
	}
	if err := cmd.Args(cmd, []string{"one", "two"}); err == nil {
		t.Fatal("ledger accepted multiple project directories")
	}
}

func TestLedgerCommandFlagsExposeNativeContext(t *testing.T) {
	cmd, _, _ := rootCmd.Find([]string{"ledger"})
	for _, want := range []struct {
		name       string
		defaultVal string
	}{
		{name: "refresh-interval", defaultVal: "2s"},
		{name: "relaunch-file", defaultVal: ""},
		{name: "lib-dir", defaultVal: ""},
		{name: "snapshot-file", defaultVal: ""},
	} {
		flag := cmd.Flags().Lookup(want.name)
		if flag == nil {
			t.Errorf("missing --%s", want.name)
			continue
		}
		if flag.DefValue != want.defaultVal {
			t.Errorf("--%s default = %q, want %q", want.name, flag.DefValue, want.defaultVal)
		}
	}
	if flag := cmd.Flags().Lookup("snapshot-file"); flag != nil && !flag.Hidden {
		t.Fatal("deterministic --snapshot-file seam must stay hidden from normal help")
	}
}

func TestLedgerCommandRejectsNonGitDirectory(t *testing.T) {
	err := runLedger(ledgerCmd, []string{t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "Git repository") {
		t.Fatalf("runLedger error = %v, want clear Git repository error", err)
	}
}

func TestLedgerCommandAppliesThemeAndLaunchOptions(t *testing.T) {
	repo := t.TempDir()
	command := exec.Command("git", "-C", repo, "init", "-q")
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	originalTUIOptions := ledgerTUIOptions
	originalProgramRun := ledgerProgramRun
	originalApplyTheme := ledgerApplyTheme
	originalSnapshotFile := ledgerSnapshotFile
	originalRefresh := ledgerRefreshInterval
	t.Cleanup(func() {
		ledgerTUIOptions = originalTUIOptions
		ledgerProgramRun = originalProgramRun
		ledgerApplyTheme = originalApplyTheme
		ledgerSnapshotFile = originalSnapshotFile
		ledgerRefreshInterval = originalRefresh
	})

	var themeApplied, cleaned bool
	var optionCount int
	ledgerApplyTheme = func() { themeApplied = true }
	ledgerTUIOptions = func() ([]tea.ProgramOption, func(), error) {
		return []tea.ProgramOption{tea.WithInput(strings.NewReader(""))}, func() { cleaned = true }, nil
	}
	ledgerProgramRun = func(model tea.Model, options ...tea.ProgramOption) (tea.Model, error) {
		if _, ok := model.(*tui.LedgerModel); !ok {
			t.Fatalf("program model = %T, want *tui.LedgerModel", model)
		}
		optionCount = len(options)
		return model, nil
	}
	ledgerSnapshotFile = ""
	ledgerRefreshInterval = 1500 * time.Millisecond

	if err := runLedger(ledgerCmd, []string{repo}); err != nil {
		t.Fatal(err)
	}

	if !themeApplied || !cleaned {
		t.Fatalf("themeApplied=%v cleaned=%v", themeApplied, cleaned)
	}
	if optionCount != 3 {
		t.Fatalf("program option count = %d, want TTY option + alt screen + all-motion", optionCount)
	}
}

func TestLedgerCommandLoadsDeterministicSnapshotSeam(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	snapshotPath := filepath.Join(t.TempDir(), "snapshot.json")
	data := `{"generation":7,"rows":[{"kind":0,"label":"modified","count":1},{"kind":1,"id":{"group":2,"path":"fixture.txt"},"path":"fixture.txt","added":4}],"metadata":{"branch":"fixture","total_files":1,"added":4}}`
	if err := os.WriteFile(snapshotPath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	originalTUIOptions := ledgerTUIOptions
	originalProgramRun := ledgerProgramRun
	originalApplyTheme := ledgerApplyTheme
	originalSnapshotFile := ledgerSnapshotFile
	t.Cleanup(func() {
		ledgerTUIOptions = originalTUIOptions
		ledgerProgramRun = originalProgramRun
		ledgerApplyTheme = originalApplyTheme
		ledgerSnapshotFile = originalSnapshotFile
	})
	ledgerApplyTheme = func() {}
	ledgerTUIOptions = func() ([]tea.ProgramOption, func(), error) { return nil, func() {}, nil }
	ledgerProgramRun = func(model tea.Model, _ ...tea.ProgramOption) (tea.Model, error) {
		if view := model.View(); !strings.Contains(view, "fixture.txt") {
			t.Fatalf("deterministic snapshot missing from view:\n%s", view)
		}
		return model, nil
	}
	ledgerSnapshotFile = snapshotPath

	if err := runLedger(ledgerCmd, []string{repo}); err != nil {
		t.Fatal(err)
	}
}
