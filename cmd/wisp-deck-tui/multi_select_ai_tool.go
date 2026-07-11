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

var multiSelectAIToolCmd = &cobra.Command{
	Use:   "multi-select-ai-tool",
	Short: "Checkbox-style multi-select for AI tools during installation",
	Long:  "Shows checkboxes for all AI tools and returns selected tools as JSON",
	RunE:  runMultiSelectAITool,
}

func init() {
	rootCmd.AddCommand(multiSelectAIToolCmd)
}

func runMultiSelectAITool(cmd *cobra.Command, args []string) error {
	tui.ApplyTheme(effectiveTheme(aiToolFlag))

	tools := models.DetectAITools()

	model := tui.NewMultiSelect(tools)

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

	m := finalModel.(tui.MultiSelectModel)
	result := m.Result()

	var output interface{}
	if result != nil && result.Confirmed {
		output = result
	} else {
		output = map[string]interface{}{"confirmed": false}
	}

	jsonOutput, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}
	fmt.Println(string(jsonOutput))

	return nil
}
