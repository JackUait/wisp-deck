package main

import (
	"encoding/json"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/jackuait/ghost-tab/internal/tui"
	"github.com/jackuait/ghost-tab/internal/util"
)

var configVersion string

var configMenuCmd = &cobra.Command{
	Use:   "config-menu",
	Short: "Interactive configuration menu",
	Long:  "Shows configuration options and returns selected action as JSON",
	RunE:  runConfigMenu,
}

func init() {
	configMenuCmd.Flags().StringVar(&configVersion, "version", "", "Current version string")
	rootCmd.AddCommand(configMenuCmd)
}

func runConfigMenu(cmd *cobra.Command, args []string) error {
	tui.ApplyTheme(tui.ThemeForTool(aiToolFlag))

	model := tui.NewConfigMenu(tui.ConfigMenuOptions{
		Version: configVersion,
	})

	ttyOpts, cleanup, err := util.TUITeaOptions()
	if err != nil {
		return fmt.Errorf("failed to run TUI: %w", err)
	}
	defer cleanup()

	opts := append([]tea.ProgramOption{tea.WithAltScreen()}, ttyOpts...)
	p := tea.NewProgram(model, opts...)

	finalModel, err := p.Run()
	if err != nil {
		return fmt.Errorf("failed to run TUI: %w", err)
	}

	m := finalModel.(tui.ConfigMenuModel)
	selected := m.Selected()

	var result map[string]interface{}
	if selected != nil {
		result = map[string]interface{}{
			"action": selected.Action,
		}
	} else {
		result = map[string]interface{}{
			"action": "quit",
		}
	}

	jsonOutput, _ := json.Marshal(result)
	fmt.Println(string(jsonOutput))

	return nil
}
