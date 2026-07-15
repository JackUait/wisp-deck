package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jackuait/wisp-deck/internal/opencodeadapter"
)

type openCodeAdapterOptions struct {
	Prefix     []string
	StateFile  string
	Generation string
	ProjectDir string
	Continue   bool
	Session    string
	Prompt     string
}

type openCodeAdapterRunner func(context.Context, openCodeAdapterOptions) (opencodeadapter.ExitResult, error)

func init() {
	rootCmd.AddCommand(newOpenCodeAdapterCommand(runOpenCodeAdapter, os.Exit, os.Getwd))
}

func newOpenCodeAdapterCommand(
	run openCodeAdapterRunner,
	exit func(int),
	getwd func() (string, error),
) *cobra.Command {
	var options openCodeAdapterOptions
	command := &cobra.Command{
		Use:          "opencode-adapter --state-file FILE --generation GEN [--continue|--session ID] [--prompt TEXT] -- COMMAND [ARG...]",
		Short:        "Publish semantic attention for one silent OpenCode TUI launch",
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(command *cobra.Command, args []string) error {
			options.Prefix = append([]string(nil), args...)
			if err := validateOpenCodeAdapterOptions(options); err != nil {
				return err
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
			physical, err := filepath.EvalSymlinks(filepath.Clean(cwd))
			if err != nil {
				return fmt.Errorf("resolve physical project cwd: %w", err)
			}
			if !filepath.IsAbs(physical) {
				return errors.New("physical project cwd must be absolute")
			}
			options.ProjectDir = filepath.Clean(physical)
			if run == nil {
				return errors.New("OpenCode adapter runner is unavailable")
			}
			result, err := run(command.Context(), options)
			if err != nil {
				return err
			}
			if result.ExitCode != 0 {
				if exit == nil {
					return fmt.Errorf("OpenCode launch exited with status %d", result.ExitCode)
				}
				exit(result.ExitCode)
			}
			return nil
		},
	}
	command.Flags().StringVar(&options.StateFile, "state-file", "", "generation-fenced attention state file")
	command.Flags().StringVar(&options.Generation, "generation", "", "attention generation identity")
	command.Flags().BoolVar(&options.Continue, "continue", false, "continue the most recent OpenCode session")
	command.Flags().StringVar(&options.Session, "session", "", "resume an exact OpenCode session")
	command.Flags().StringVar(&options.Prompt, "prompt", "", "prompt to append and submit after attach")
	return command
}

func validateOpenCodeAdapterOptions(options openCodeAdapterOptions) error {
	if _, err := opencodeadapter.BuildServerArgv(options.Prefix, 1); err != nil {
		return err
	}
	if options.StateFile == "" || !filepath.IsAbs(options.StateFile) || filepath.Clean(options.StateFile) != options.StateFile {
		return errors.New("--state-file must be a clean absolute path")
	}
	if !claudeAttentionGeneration.MatchString(options.Generation) ||
		filepath.Base(options.StateFile) != "state" || filepath.Base(filepath.Dir(options.StateFile)) != options.Generation {
		return fmt.Errorf("attention state path does not belong to generation %q", options.Generation)
	}
	if options.Continue && options.Session != "" {
		return errors.New("--continue and --session are mutually exclusive")
	}
	if options.Session != "" && (len(options.Session) > 256 || strings.ContainsAny(options.Session, "\x00\t\r\n")) {
		return errors.New("--session is invalid")
	}
	if len(options.Prompt) > 64*1024 || strings.ContainsRune(options.Prompt, '\x00') {
		return errors.New("--prompt is invalid")
	}
	return nil
}

func runOpenCodeAdapter(ctx context.Context, options openCodeAdapterOptions) (opencodeadapter.ExitResult, error) {
	supervisor := opencodeadapter.Supervisor{}
	return supervisor.Run(ctx, opencodeadapter.SupervisorOptions{
		Prefix: options.Prefix, StateFile: options.StateFile, Generation: options.Generation,
		ProjectDir: options.ProjectDir, Continue: options.Continue, Session: options.Session,
		Prompt: options.Prompt,
	})
}
