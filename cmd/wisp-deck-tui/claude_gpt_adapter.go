package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/jackuait/wisp-deck/internal/gptbridge"
)

type claudeGPTAdapterRunner func(
	context.Context,
	gptbridge.AdapterOptions,
) (gptbridge.AdapterResult, error)

func init() {
	rootCmd.AddCommand(newClaudeGPTAdapterCommand(
		runClaudeGPTAdapter,
		os.Exit,
		os.Getwd,
		os.Environ,
	))
}

func newClaudeGPTAdapterCommand(
	run claudeGPTAdapterRunner,
	exit func(int),
	getwd func() (string, error),
	environ func() []string,
) *cobra.Command {
	var codexPath string
	command := &cobra.Command{
		Use:          "claude-gpt-adapter --codex PATH -- COMMAND [ARG...]",
		Short:        "Route one Claude launch through a ChatGPT subscription",
		Hidden:       true,
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(command *cobra.Command, claudeArgv []string) error {
			if codexPath == "" || !filepath.IsAbs(codexPath) {
				return errors.New("--codex must be an absolute path")
			}
			if getwd == nil {
				return errors.New("project cwd resolver is unavailable")
			}
			cwd, err := getwd()
			if err != nil {
				return fmt.Errorf("resolve project cwd: %w", err)
			}
			if !filepath.IsAbs(cwd) {
				return errors.New("project cwd must be absolute")
			}
			physicalCWD, err := filepath.EvalSymlinks(filepath.Clean(cwd))
			if err != nil {
				return fmt.Errorf("resolve physical project cwd: %w", err)
			}
			if run == nil {
				return errors.New("Claude GPT adapter runner is unavailable")
			}
			var environment []string
			if environ != nil {
				environment = environ()
			}
			runContext, stopSignals := signal.NotifyContext(
				command.Context(),
				syscall.SIGTERM,
				syscall.SIGHUP,
				syscall.SIGINT,
				syscall.SIGQUIT,
			)
			defer stopSignals()
			result, err := run(runContext, gptbridge.AdapterOptions{
				CodexPath:     codexPath,
				ClaudeArgv:    append([]string(nil), claudeArgv...),
				Environment:   environment,
				WorkingDir:    physicalCWD,
				ClientVersion: Version,
			})
			if err != nil {
				return err
			}
			if result.ExitCode != 0 {
				if exit == nil {
					return fmt.Errorf("Claude launch exited with status %d", result.ExitCode)
				}
				exit(result.ExitCode)
			}
			return nil
		},
	}
	command.Flags().StringVar(&codexPath, "codex", "", "absolute Codex executable path")
	return command
}

func runClaudeGPTAdapter(
	ctx context.Context,
	options gptbridge.AdapterOptions,
) (gptbridge.AdapterResult, error) {
	return gptbridge.RunAdapter(ctx, options)
}
