package main

import (
	"io"

	"github.com/jackuait/wisp-deck/internal/statusline"
	"github.com/spf13/cobra"
)

// statusline-context corrects the context_window figures in Claude Code's
// statusline JSON (stdin → stdout) when the active model is a subscription
// catalog model whose real window differs from Claude Code's hardcoded guess.
// It never fails the caller: on any problem the input echoes through verbatim
// so the wrapper keeps rendering — a statusline is no place for error output.
var statuslineContextCmd = &cobra.Command{
	Use:           "statusline-context",
	Short:         "Rewrite statusline JSON with the subscription model's real context window",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		raw, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return nil
		}
		out, _ := statusline.RewriteContextWindow(raw)
		_, _ = cmd.OutOrStdout().Write(out)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statuslineContextCmd)
}
