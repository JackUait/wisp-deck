package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/spf13/cobra"

	"github.com/jackuait/wisp-deck/internal/codexadapter"
)

var canonicalCodexUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type codexAdapterOptions struct {
	CodexPath      string
	StateFile      string
	Generation     string
	ProjectCWD     string
	ClientVersion  string
	ResumeSession  string
	FallbackWindow time.Duration
	Prompt         string
}

type codexAdapterRunner func(context.Context, codexAdapterOptions) (codexadapter.CodexExitResult, error)

func init() {
	rootCmd.AddCommand(newCodexAdapterCommand(runCodexAdapter, os.Exit, os.Getwd))
}

func newCodexAdapterCommand(
	run codexAdapterRunner,
	exit func(int),
	getwd func() (string, error),
) *cobra.Command {
	var options codexAdapterOptions
	cmd := &cobra.Command{
		Use:          "codex-adapter --codex PATH --state-file FILE --generation GEN [--resume-session UUID] [--fallback-window 10s] [-- PROMPT]",
		Short:        "Publish semantic attention for one Codex TUI launch",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateCodexAdapterOptions(options); err != nil {
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
			physicalCWD, err := filepath.EvalSymlinks(filepath.Clean(cwd))
			if err != nil {
				return fmt.Errorf("resolve physical project cwd: %w", err)
			}
			if !filepath.IsAbs(physicalCWD) {
				return errors.New("physical project cwd must be absolute")
			}
			options.ProjectCWD = filepath.Clean(physicalCWD)
			options.ClientVersion = Version
			if len(args) == 1 {
				options.Prompt = args[0]
			}
			if run == nil {
				return errors.New("Codex adapter runner is unavailable")
			}
			result, err := run(cmd.Context(), options)
			if err != nil {
				return err
			}
			if result.ExitCode != 0 {
				if exit == nil {
					return fmt.Errorf("Codex launch exited with status %d", result.ExitCode)
				}
				exit(result.ExitCode)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&options.CodexPath, "codex", "", "absolute Codex executable path")
	cmd.Flags().StringVar(&options.StateFile, "state-file", "", "generation-fenced attention state file")
	cmd.Flags().StringVar(&options.Generation, "generation", "", "attention generation identity")
	cmd.Flags().StringVar(&options.ResumeSession, "resume-session", "", "exact Codex thread UUID to resume")
	cmd.Flags().DurationVar(&options.FallbackWindow, "fallback-window", 10*time.Second, "strict startup fallback window")
	return cmd
}

func validateCodexAdapterOptions(options codexAdapterOptions) error {
	if options.CodexPath == "" || !filepath.IsAbs(options.CodexPath) {
		return errors.New("--codex must be an absolute path")
	}
	if options.StateFile == "" || !filepath.IsAbs(options.StateFile) {
		return errors.New("--state-file must be absolute")
	}
	if options.Generation == "" || !claudeAttentionGeneration.MatchString(options.Generation) {
		return fmt.Errorf("invalid attention generation %q", options.Generation)
	}
	cleanState := filepath.Clean(options.StateFile)
	if filepath.Base(cleanState) != "state" || filepath.Base(filepath.Dir(cleanState)) != options.Generation {
		return fmt.Errorf("attention state path does not belong to generation %q", options.Generation)
	}
	if options.ResumeSession != "" && !canonicalCodexUUID.MatchString(options.ResumeSession) {
		return fmt.Errorf("--resume-session must be a canonical lowercase UUID")
	}
	if options.FallbackWindow <= 0 {
		return errors.New("--fallback-window must be positive")
	}
	return nil
}

func runCodexAdapter(ctx context.Context, options codexAdapterOptions) (codexadapter.CodexExitResult, error) {
	supervisor := codexadapter.CodexSupervisor{}
	return supervisor.Run(ctx, codexadapter.CodexSupervisorOptions{
		CodexPath:      options.CodexPath,
		StateFile:      options.StateFile,
		Generation:     options.Generation,
		ProjectCWD:     options.ProjectCWD,
		ClientVersion:  options.ClientVersion,
		ResumeSession:  options.ResumeSession,
		FallbackWindow: options.FallbackWindow,
		Prompt:         options.Prompt,
	})
}
