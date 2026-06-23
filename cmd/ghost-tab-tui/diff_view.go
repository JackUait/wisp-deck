package main

import (
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/jackuait/ghost-tab/internal/tui"
	"github.com/jackuait/ghost-tab/internal/util"
)

var (
	diffViewTitle        string
	diffViewBackdropFile string
)

var diffViewCmd = &cobra.Command{
	Use:   "diff-view",
	Short: "Scrollable diff pager",
	Long:  "Reads a (colored) diff from stdin and shows it in a scrollable popup pager that closes on Esc, q, ctrl+c, or a click outside the box.",
	RunE:  runDiffView,
}

func init() {
	diffViewCmd.Flags().StringVar(&diffViewTitle, "title", "", "title shown in the header")
	diffViewCmd.Flags().StringVar(&diffViewBackdropFile, "backdrop-file", "",
		"file with a serialized screen capture shown dimmed behind the popup")
	rootCmd.AddCommand(diffViewCmd)
}

func runDiffView(cmd *cobra.Command, args []string) error {
	// The diff body arrives on stdin (a pipe); keyboard input comes from the TTY
	// via TUITeaOptions, so the two never collide.
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("failed to read diff: %w", err)
	}

	tui.ApplyTheme(tui.ThemeForTool(aiToolFlag))
	model := tui.NewDiffView(diffViewTitle, string(data))
	// Show the screen behind the (full-screen) popup dimmed in the margin. Best
	// effort: an unreadable/missing backdrop file just leaves the margin blank.
	if diffViewBackdropFile != "" {
		if raw, err := os.ReadFile(diffViewBackdropFile); err == nil {
			model = model.WithBackdrop(tui.ParseBackdrop(string(raw)))
		}
	}

	ttyOpts, cleanup, err := util.TUITeaOptions()
	if err != nil {
		return fmt.Errorf("failed to run TUI: %w", err)
	}
	defer cleanup()

	// All-motion so the view-switch tabs highlight on hover, not just on click.
	opts := append(ttyOpts, tea.WithAltScreen(), tea.WithMouseAllMotion())
	p := tea.NewProgram(model, opts...)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("failed to run TUI: %w", err)
	}
	return nil
}
