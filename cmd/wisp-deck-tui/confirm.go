package main

import (
	"encoding/json"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/tui"
	"github.com/jackuait/wisp-deck/internal/util"
	"github.com/spf13/cobra"
)

var confirmCmd = &cobra.Command{
	Use:   "confirm [message]",
	Short: "Show confirmation dialog",
	Long:  "Shows yes/no confirmation dialog and returns result as JSON",
	Args:  cobra.ExactArgs(1),
	RunE:  runConfirm,
}

func init() {
	rootCmd.AddCommand(confirmCmd)
}

func runConfirm(cmd *cobra.Command, args []string) error {
	tui.ApplyTheme(effectiveTheme(aiToolFlag))

	message := args[0]

	model := tui.NewConfirmDialog(message)

	ttyOpts, cleanup, err := util.TUITeaOptions()
	if err != nil {
		return fmt.Errorf("failed to run TUI: %w", err)
	}
	defer cleanup()

	// Mouse enabled so the Yes/No buttons are clickable (and hoverable).
	opts := append([]tea.ProgramOption{tea.WithMouseAllMotion()}, ttyOpts...)
	p := tea.NewProgram(tui.WithBlackBackground(model), opts...)

	finalModel, err := p.Run()
	if err != nil {
		return fmt.Errorf("failed to run TUI: %w", err)
	}

	m := tui.Unwrap(finalModel).(tui.ConfirmDialogModel)

	result := map[string]interface{}{
		"confirmed": m.Confirmed,
	}

	jsonOutput, _ := json.Marshal(result)
	fmt.Println(string(jsonOutput))

	return nil
}
