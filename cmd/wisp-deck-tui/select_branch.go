package main

import (
	"encoding/json"
	"fmt"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/models"
	"github.com/jackuait/wisp-deck/internal/tui"
	"github.com/jackuait/wisp-deck/internal/util"
	"github.com/spf13/cobra"
)

var selectBranchCmd = &cobra.Command{
	Use:   "select-branch",
	Short: "Interactive branch selector for worktree creation",
	Long:  "Shows a filterable list of branches and returns the selected branch as JSON",
	RunE:  runSelectBranch,
}

var projectPathFlag string

func init() {
	selectBranchCmd.Flags().StringVar(&projectPathFlag, "project-path", "", "Path to the git project")
	selectBranchCmd.MarkFlagRequired("project-path")
	rootCmd.AddCommand(selectBranchCmd)
}

func runSelectBranch(cmd *cobra.Command, args []string) error {
	theme := effectiveTheme(aiToolFlag)
	tui.ApplyTheme(theme)

	// Get main branch name from worktree porcelain output
	wtCmd := exec.Command("git", "-C", projectPathFlag, "worktree", "list", "--porcelain")
	wtOut, _ := wtCmd.Output()
	mainBranch := models.ParseMainBranch(string(wtOut))

	// List all branches
	branches := models.ListBranches(projectPathFlag)
	if len(branches) == 0 {
		result := map[string]interface{}{"selected": false}
		jsonOutput, _ := json.Marshal(result)
		fmt.Println(string(jsonOutput))
		return nil
	}

	// Get existing worktrees to filter out
	worktrees := models.DetectWorktrees(projectPathFlag)

	// Filter out taken branches
	available := models.FilterAvailableBranches(branches, worktrees, mainBranch)
	if len(available) == 0 {
		result := map[string]interface{}{"selected": false}
		jsonOutput, _ := json.Marshal(result)
		fmt.Println(string(jsonOutput))
		return nil
	}

	model := tui.NewBranchPicker(available, theme, projectPathFlag)

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

	m := finalModel.(tui.BranchPickerModel)
	selected := m.Selected()

	var result map[string]interface{}
	if selected != nil {
		result = map[string]interface{}{
			"branch":   *selected,
			"selected": true,
		}
	} else {
		result = map[string]interface{}{"selected": false}
	}

	jsonOutput, _ := json.Marshal(result)
	fmt.Println(string(jsonOutput))
	return nil
}
