package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/tui"
)

type unavailableLedgerProcessRunner struct{}

func (unavailableLedgerProcessRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return nil, errors.New("unavailable in test")
}

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
	originalProcessRunner := ledgerProcessRunner
	originalSnapshotFile := ledgerSnapshotFile
	originalRefresh := ledgerRefreshInterval
	t.Cleanup(func() {
		ledgerTUIOptions = originalTUIOptions
		ledgerProgramRun = originalProgramRun
		ledgerApplyTheme = originalApplyTheme
		ledgerProcessRunner = originalProcessRunner
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
	data := `{"generation":7,"rows":[{"kind":0,"group":2,"label":"modified","count":1},{"kind":1,"id":{"group":2,"path":"fixture.txt"},"path":"fixture.txt","added":4}],"metadata":{"branch":"fixture","total_files":1,"added":4}}`
	if err := os.WriteFile(snapshotPath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := readLedgerSnapshot(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Rows[0].Group; got != 2 {
		t.Fatalf("snapshot seam dropped group identity: got %d, want 2", got)
	}

	originalTUIOptions := ledgerTUIOptions
	originalProgramRun := ledgerProgramRun
	originalApplyTheme := ledgerApplyTheme
	originalProcessRunner := ledgerProcessRunner
	originalSnapshotFile := ledgerSnapshotFile
	t.Cleanup(func() {
		ledgerTUIOptions = originalTUIOptions
		ledgerProgramRun = originalProgramRun
		ledgerApplyTheme = originalApplyTheme
		ledgerProcessRunner = originalProcessRunner
		ledgerSnapshotFile = originalSnapshotFile
	})
	ledgerApplyTheme = func() {}
	ledgerProcessRunner = unavailableLedgerProcessRunner{}
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

func TestLedgerAccountCommandWiresSessionAndPopupAdapters(t *testing.T) {
	// Exercise the fixture account, not the launch plan inherited from this Wisp pane.
	t.Setenv("WISP_DECK_PLAN", "Standard Claude")

	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	fixtureDir := t.TempDir()
	snapshotPath := filepath.Join(fixtureDir, "snapshot.json")
	snapshot := `{"generation":1,"rows":[{"kind":0,"group":2,"label":"modified","count":1},{"kind":1,"id":{"group":2,"path":"fixture.go"},"path":"fixture.go","added":1}],"metadata":{"branch":"main","total_files":1,"added":1}}`
	if err := os.WriteFile(snapshotPath, []byte(snapshot), 0o644); err != nil {
		t.Fatal(err)
	}
	list := filepath.Join(fixtureDir, "claude-accounts.list")
	pointer := filepath.Join(fixtureDir, "claude-account")
	colors := filepath.Join(fixtureDir, "claude-account-colors")
	for path, content := range map[string]string{list: "Work:work\n", pointer: "work\n", colors: "work:170\n"} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	relaunch := filepath.Join(fixtureDir, "relaunch")
	context := strings.Join([]string{
		"tool=claude", "tools=claude opencode", "list=" + list,
		"pointer=" + pointer, "colors=" + colors,
	}, "\n")
	if err := os.WriteFile(relaunch, []byte(context), 0o644); err != nil {
		t.Fatal(err)
	}

	originalTUIOptions := ledgerTUIOptions
	originalProgramRun := ledgerProgramRun
	originalApplyTheme := ledgerApplyTheme
	originalProcessRunner := ledgerProcessRunner
	originalSnapshotFile := ledgerSnapshotFile
	originalRelaunchFile := ledgerRelaunchFile
	originalLibDir := ledgerLibDir
	t.Cleanup(func() {
		ledgerTUIOptions = originalTUIOptions
		ledgerProgramRun = originalProgramRun
		ledgerApplyTheme = originalApplyTheme
		ledgerProcessRunner = originalProcessRunner
		ledgerSnapshotFile = originalSnapshotFile
		ledgerRelaunchFile = originalRelaunchFile
		ledgerLibDir = originalLibDir
	})
	ledgerApplyTheme = func() {}
	ledgerProcessRunner = unavailableLedgerProcessRunner{}
	ledgerTUIOptions = func() ([]tea.ProgramOption, func(), error) { return nil, func() {}, nil }
	ledgerSnapshotFile = snapshotPath
	ledgerRelaunchFile = relaunch
	ledgerLibDir = filepath.Join(repo, "lib")
	ledgerProgramRun = func(model tea.Model, _ ...tea.ProgramOption) (tea.Model, error) {
		ledgerModel := model.(*tui.LedgerModel)
		initCommand := ledgerModel.Init()
		batch, ok := initCommand().(tea.BatchMsg)
		if !ok {
			t.Fatalf("ledger Init = %T, want snapshot/session batch", initCommand())
		}
		for _, command := range batch {
			if command != nil {
				ledgerModel.Update(command())
			}
		}
		ledgerModel.Update(tea.WindowSizeMsg{Width: 80, Height: 14})
		if view := ledgerModel.View(); !strings.Contains(view, "Work") {
			t.Fatalf("session pill not wired:\n%s", view)
		}
		_, openCommand := ledgerModel.Update(tea.MouseMsg{
			X: 1, Y: 13, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
		})
		if openCommand != nil {
			t.Fatal("account pill started a process before the in-process selection")
		}
		if view := ledgerModel.View(); !strings.Contains(view, "Switch agent") || !strings.Contains(view, "OpenCode") {
			t.Fatalf("account pill did not paint the in-process chooser:\n%s", view)
		}
		ledgerModel.Update(tea.KeyMsg{Type: tea.KeyDown})
		_, switchCommand := ledgerModel.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if switchCommand == nil {
			t.Fatal("confirmed account choice has no asynchronous apply command")
		}
		return model, nil
	}

	if err := runLedger(ledgerCmd, []string{repo}); err != nil {
		t.Fatal(err)
	}
}
