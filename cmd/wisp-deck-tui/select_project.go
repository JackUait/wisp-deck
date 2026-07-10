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

var selectProjectCmd = &cobra.Command{
	Use:   "select-project",
	Short: "Interactive project selector",
	Long:  "Shows a list of projects and returns selected project as JSON",
	RunE:  runSelectProject,
}

var projectsFile string

func init() {
	selectProjectCmd.Flags().StringVar(&projectsFile, "projects-file", "", "Path to projects file")
	selectProjectCmd.MarkFlagRequired("projects-file")
	rootCmd.AddCommand(selectProjectCmd)
}

func runSelectProject(cmd *cobra.Command, args []string) error {
	tui.ApplyTheme(effectiveTheme(aiToolFlag))

	projects, err := models.LoadProjects(projectsFile)
	if err != nil {
		return fmt.Errorf("failed to load projects: %w", err)
	}

	if len(projects) == 0 {
		result := map[string]interface{}{"selected": false}
		jsonOutput, _ := json.Marshal(result)
		fmt.Println(string(jsonOutput))
		return nil
	}

	model := tui.NewProjectSelector(projects)

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

	m := finalModel.(tui.ProjectSelectorModel)
	selected := m.Selected()

	var result map[string]interface{}
	if selected != nil {
		result = map[string]interface{}{
			"name":     selected.Name,
			"path":     selected.Path,
			"selected": true,
		}
	} else {
		result = map[string]interface{}{"selected": false}
	}

	jsonOutput, _ := json.Marshal(result)
	fmt.Println(string(jsonOutput))

	return nil
}
