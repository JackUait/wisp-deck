package main

import (
	"encoding/json"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/models"
	"github.com/jackuait/wisp-deck/internal/tui"
	"github.com/jackuait/wisp-deck/internal/util"
	"github.com/spf13/cobra"
)

var selectAIToolCmd = &cobra.Command{
	Use:   "select-ai-tool",
	Short: "Interactive AI tool selector",
	Long:  "Shows available AI tools and returns selected tool as JSON",
	RunE:  runSelectAITool,
}

func init() {
	rootCmd.AddCommand(selectAIToolCmd)
}

func runSelectAITool(cmd *cobra.Command, args []string) error {
	tui.ApplyTheme(effectiveTheme(aiToolFlag))

	tools := models.DetectAITools()

	model := tui.NewAIToolSelector(tools)

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

	m := finalModel.(tui.AIToolSelectorModel)
	selected := m.Selected()

	var result map[string]interface{}
	if selected != nil {
		result = map[string]interface{}{
			"tool":     selected.Name,
			"command":  selected.Command,
			"selected": true,
		}
	} else {
		result = map[string]interface{}{"selected": false}
	}

	jsonOutput, _ := json.Marshal(result)
	fmt.Println(string(jsonOutput))

	return nil
}
