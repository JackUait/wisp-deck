package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/ghost-tab/internal/tui"
	"github.com/charmbracelet/x/term"
)

func main() {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot open /dev/tty: %v\n", err)
		return
	}
	defer tty.Close()

	// Get actual terminal size
	width, height, err := term.GetSize(tty.Fd())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot get terminal size: %v\n", err)
		return
	}

	// Set renderer to TTY like the real app does
	lipgloss.SetDefaultRenderer(lipgloss.NewRenderer(tty))

	ghostLines := tui.GhostForTool("opencode", false)
	ghost := tui.RenderGhost(ghostLines)

	menuBox := strings.Repeat("X", 48)
	spacer := strings.Repeat(" ", 3)
	content := lipgloss.JoinHorizontal(lipgloss.Top, menuBox, spacer, ghost)
	contentWidth := lipgloss.Width(content)

	// Debug info line
	info := fmt.Sprintf("terminal=%dx%d  contentWidth=%d  leftPad=%d", width, height, contentWidth, (width-contentWidth)/2)

	// Border line at full terminal width
	border := strings.Repeat("-", width)

	// Center the content
	placed := lipgloss.Place(width, 1, lipgloss.Center, lipgloss.Top, content)

	// Print to TTY directly
	fmt.Fprintln(tty, info)
	fmt.Fprintln(tty, border)
	fmt.Fprint(tty, placed)
	fmt.Fprintln(tty)
	fmt.Fprintln(tty, border)
	fmt.Fprintln(tty, "If dashes reach both window edges and content is NOT centered between them,")
	fmt.Fprintln(tty, "there's a width measurement issue. Press Enter to exit.")

	// Wait for keypress
	buf := make([]byte, 1)
	tty.Read(buf)
}
