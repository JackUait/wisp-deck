package main

import (
	"encoding/json"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/tui"
	"github.com/jackuait/wisp-deck/internal/util"
	"github.com/spf13/cobra"
)

var (
	configVersion    string
	configAutoSwitch string
)

var configMenuCmd = &cobra.Command{
	Use:   "config-menu",
	Short: "Interactive configuration menu",
	Long:  "Shows configuration options and returns selected action as JSON",
	RunE:  runConfigMenu,
}

func init() {
	configMenuCmd.Flags().StringVar(&configVersion, "version", "", "Current version string")
	configMenuCmd.Flags().StringVar(&configAutoSwitch, "auto-switch", "off", "Current auto-switch setting (on/off)")
	rootCmd.AddCommand(configMenuCmd)
}

func runConfigMenu(cmd *cobra.Command, args []string) error {
	tui.ApplyTheme(effectiveTheme(aiToolFlag))

	model := tui.NewConfigMenu(tui.ConfigMenuOptions{
		Version:    configVersion,
		AutoSwitch: configAutoSwitch,
	})

	ttyOpts, cleanup, err := util.TUITeaOptions()
	if err != nil {
		return fmt.Errorf("failed to run TUI: %w", err)
	}
	defer cleanup()

	opts := append([]tea.ProgramOption{tea.WithAltScreen(), tea.WithMouseAllMotion()}, ttyOpts...)
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
