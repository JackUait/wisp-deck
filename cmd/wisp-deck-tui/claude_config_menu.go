package main

import (
	"encoding/json"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/tui"
	"github.com/jackuait/wisp-deck/internal/util"
	"github.com/spf13/cobra"
)

var ccmConfigsList string

var claudeConfigMenuCmd = &cobra.Command{
	Use:   "claude-config-menu",
	Short: "Manage Claude config files (add/rename/delete)",
	RunE:  runClaudeConfigMenu,
}

func init() {
	claudeConfigMenuCmd.Flags().StringVar(&ccmConfigsList, "configs-list", "", "Path to configs list (name:file)")
	rootCmd.AddCommand(claudeConfigMenuCmd)
}

func runClaudeConfigMenu(cmd *cobra.Command, args []string) error {
	configs := tui.LoadClaudeConfigsList(ccmConfigsList)
	model := tui.NewClaudeConfigMenu(configs)

	ttyOpts, cleanup, err := util.TUITeaOptions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open terminal: %v\n", err)
		fmt.Println(`{"action":"quit"}`)
		return nil
	}
	defer cleanup()

	opts := append([]tea.ProgramOption{tea.WithAltScreen(), tea.WithMouseAllMotion()}, ttyOpts...)
	p := tea.NewProgram(model, opts...)
	finalModel, runErr := p.Run()

	m, ok := finalModel.(tui.ClaudeConfigMenuModel)
	if !ok || m.Result() == nil {
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "TUI error: %v\n", runErr)
		}
		fmt.Println(`{"action":"quit"}`)
		return nil
	}
	out, _ := json.Marshal(m.Result())
	fmt.Println(string(out))
	return nil
}
