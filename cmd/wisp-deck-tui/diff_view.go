package main

import (
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/tui"
	"github.com/jackuait/wisp-deck/internal/util"
	"github.com/spf13/cobra"
)

var (
	diffViewTitle        string
	diffViewBackdropFile string
	diffViewDiscardFile  string
	diffViewImage        bool
	diffViewStatus       string
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
	diffViewCmd.Flags().StringVar(&diffViewDiscardFile, "discard-file", "",
		"file the pager writes 'discard' to when the user confirms discarding the file")
	diffViewCmd.Flags().BoolVar(&diffViewImage, "image", false,
		"treat stdin as raw image bytes and show a preview instead of a diff")
	diffViewCmd.Flags().StringVar(&diffViewStatus, "status", "modified",
		"file status badge for --image mode (added|modified|deleted)")
	rootCmd.AddCommand(diffViewCmd)
}

// writeDiscardDecision records the user's discard choice for the bash caller:
// the literal "discard" when confirmed, and nothing otherwise. An empty path
// (no --discard-file) is a no-op so the pager works standalone.
func writeDiscardDecision(path string, requested bool) error {
	if path == "" || !requested {
		return nil
	}
	return os.WriteFile(path, []byte("discard"), 0o644)
}

func runDiffView(cmd *cobra.Command, args []string) error {
	// The diff body arrives on stdin (a pipe); keyboard input comes from the TTY
	// via TUITeaOptions, so the two never collide.
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("failed to read diff: %w", err)
	}

	tui.ApplyTheme(effectiveTheme(aiToolFlag))
	// --image: stdin carried raw image bytes, not a diff; the pager shows a
	// half-block preview with the caller-supplied status badge.
	var model tui.DiffViewModel
	if diffViewImage {
		model = tui.NewImageView(diffViewTitle, data, diffViewStatus)
	} else {
		model = tui.NewDiffView(diffViewTitle, string(data))
	}
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
	finalModel, err := p.Run()
	if err != nil {
		return fmt.Errorf("failed to run TUI: %w", err)
	}
	// If the user confirmed discarding, leave a marker for the bash caller, which
	// runs the actual git restore after the popup closes.
	if dv, ok := finalModel.(tui.DiffViewModel); ok {
		if err := writeDiscardDecision(diffViewDiscardFile, dv.DiscardRequested()); err != nil {
			return fmt.Errorf("failed to record discard decision: %w", err)
		}
	}
	return nil
}
