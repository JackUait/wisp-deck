package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/ledger"
	"github.com/jackuait/wisp-deck/internal/tui"
	"github.com/jackuait/wisp-deck/internal/util"
	"github.com/spf13/cobra"
)

var (
	ledgerRefreshInterval = 2 * time.Second
	ledgerRelaunchFile    string
	ledgerLibDir          string
	ledgerSnapshotFile    string
)

var ledgerCmd = &cobra.Command{
	Use:   "ledger PROJECT_DIR",
	Short: "Native changeset ledger",
	Args:  cobra.ExactArgs(1),
	RunE:  runLedger,
}

var (
	ledgerTUIOptions                         = util.TUITeaOptions
	ledgerApplyTheme                         = func() { tui.ApplyTheme(effectiveTheme(aiToolFlag)) }
	ledgerProcessRunner ledger.ProcessRunner = ledger.ExecProcessRunner{}
	ledgerProgramRun                         = func(model tea.Model, options ...tea.ProgramOption) (tea.Model, error) {
		return tea.NewProgram(model, options...).Run()
	}
)

func init() {
	ledgerCmd.Flags().DurationVar(&ledgerRefreshInterval, "refresh-interval", 2*time.Second, "interval between repository refreshes")
	ledgerCmd.Flags().StringVar(&ledgerRelaunchFile, "relaunch-file", "", "session relaunch context for account switching")
	ledgerCmd.Flags().StringVar(&ledgerLibDir, "lib-dir", "", "path to the wisp-deck library directory")
	ledgerCmd.Flags().StringVar(&ledgerSnapshotFile, "snapshot-file", "", "deterministic ledger snapshot fixture")
	if err := ledgerCmd.Flags().MarkHidden("snapshot-file"); err != nil {
		panic(err)
	}
	rootCmd.AddCommand(ledgerCmd)
}

func runLedger(_ *cobra.Command, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("ledger requires exactly one project directory")
	}
	projectDir := args[0]
	runner := ledger.ExecRunner{}
	out, err := runner.Run(context.Background(), projectDir, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		if err != nil {
			return fmt.Errorf("ledger project is not a Git repository: %w", err)
		}
		return fmt.Errorf("ledger project is not a Git repository")
	}

	processRunner := ledgerProcessRunner
	backdropCache := ledger.NewFileBackdropCache(processRunner,
		filepath.Join(os.TempDir(), fmt.Sprintf("wisp-ledger-backdrop-%d", os.Getpid())))
	defer backdropCache.Close()
	popup := ledger.NewExecPopup(processRunner, os.TempDir())
	var sessionSource LedgerSessionSource
	var accountSwitcher ledger.AccountSwitcher
	if ledgerRelaunchFile != "" {
		sessionSource = ledger.NewSessionSource(processRunner)
		if ledgerLibDir != "" {
			accountSwitcher = ledger.NewExecAccountSwitcher(processRunner, ledgerLibDir)
		}
	}

	var source LedgerSnapshotSource = ledger.NewSource(runner, ledger.WithPlan(os.Getenv("WISP_DECK_PLAN")))
	snapshot := ledger.NewSnapshot(0, nil, ledger.Metadata{})
	loading := true
	if ledgerSnapshotFile != "" {
		snapshot, err = readLedgerSnapshot(ledgerSnapshotFile)
		if err != nil {
			return fmt.Errorf("load ledger snapshot: %w", err)
		}
		source = staticLedgerSource{snapshot: snapshot}
		loading = false
	}

	ledgerApplyTheme()
	model := tui.NewLedgerModel(source, snapshot, tui.LedgerOptions{
		ProjectDir:      projectDir,
		Loading:         loading,
		RefreshInterval: ledgerRefreshInterval,
		Mutator:         ledger.NewGitMutator(runner),
		Popup:           popup,
		BackdropCache:   backdropCache,
		Tool:            aiToolFlag,
		SessionSource:   sessionSource,
		SessionPath:     ledgerRelaunchFile,
		AccountSwitcher: accountSwitcher,
	})
	ttyOptions, cleanup, err := ledgerTUIOptions()
	if err != nil {
		return fmt.Errorf("failed to run ledger TUI: %w", err)
	}
	defer cleanup()
	options := append(ttyOptions, tea.WithAltScreen(), tea.WithMouseAllMotion())
	if _, err := ledgerProgramRun(model, options...); err != nil {
		return fmt.Errorf("failed to run ledger TUI: %w", err)
	}
	return nil
}

// LedgerSnapshotSource keeps the command seam limited to the public ledger
// loading contract while allowing deterministic PTY fixtures.
type LedgerSnapshotSource interface {
	Load(context.Context, string, uint64) (ledger.Snapshot, error)
}

// LedgerSessionSource is the command's session-context loading seam.
type LedgerSessionSource interface {
	Load(context.Context, string) (ledger.SessionContext, error)
}

type staticLedgerSource struct {
	snapshot ledger.Snapshot
}

func (s staticLedgerSource) Load(_ context.Context, _ string, generation uint64) (ledger.Snapshot, error) {
	snapshot := s.snapshot
	snapshot.Generation = generation
	return snapshot, nil
}

type ledgerSnapshotDocument struct {
	Generation uint64              `json:"generation"`
	Rows       []ledgerSnapshotRow `json:"rows"`
	Metadata   struct {
		Branch     string `json:"branch"`
		Ahead      int    `json:"ahead"`
		Behind     int    `json:"behind"`
		Plan       string `json:"plan"`
		TotalFiles int    `json:"total_files"`
		Added      int    `json:"added"`
		Deleted    int    `json:"deleted"`
	} `json:"metadata"`
}

type ledgerSnapshotRow struct {
	Kind  ledger.RowKind `json:"kind"`
	Group ledger.Group   `json:"group"`
	ID    struct {
		Group ledger.Group `json:"group"`
		Path  string       `json:"path"`
	} `json:"id"`
	Path     string `json:"path"`
	Label    string `json:"label"`
	Count    int    `json:"count"`
	Added    int    `json:"added"`
	Deleted  int    `json:"deleted"`
	Binary   bool   `json:"binary"`
	OldBytes int64  `json:"old_bytes"`
	NewBytes int64  `json:"new_bytes"`
}

func readLedgerSnapshot(path string) (ledger.Snapshot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ledger.Snapshot{}, err
	}
	var document ledgerSnapshotDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return ledger.Snapshot{}, err
	}
	rows := make([]ledger.Row, len(document.Rows))
	for index, row := range document.Rows {
		rows[index] = ledger.Row{
			Kind: row.Kind, Group: row.Group, ID: ledger.RowID{Group: row.ID.Group, Path: row.ID.Path},
			Path: row.Path, Label: row.Label, Count: row.Count,
			Added: row.Added, Deleted: row.Deleted, Binary: row.Binary,
			OldBytes: row.OldBytes, NewBytes: row.NewBytes,
		}
	}
	metadata := ledger.Metadata{
		Branch: document.Metadata.Branch, Ahead: document.Metadata.Ahead,
		Behind: document.Metadata.Behind, Plan: document.Metadata.Plan,
		TotalFiles: document.Metadata.TotalFiles, Added: document.Metadata.Added,
		Deleted: document.Metadata.Deleted,
	}
	return ledger.NewSnapshot(document.Generation, rows, metadata), nil
}
