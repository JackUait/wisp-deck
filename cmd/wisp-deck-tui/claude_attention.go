package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jackuait/wisp-deck/internal/attention"
)

var claudeAttentionGeneration = regexp.MustCompile(`^generation\.[A-Za-z0-9]+$`)

type claudeAttentionOptions struct {
	StateFile  string
	Generation string
	ConfigDir  string
}

type claudeAttentionRunner func(
	context.Context,
	claudeAttentionOptions,
	[]string,
) (attention.ClaudeExitResult, error)

func init() {
	rootCmd.AddCommand(newClaudeAttentionCommand(runClaudeAttention, os.Exit))
}

func newClaudeAttentionCommand(run claudeAttentionRunner, exit func(int)) *cobra.Command {
	var options claudeAttentionOptions
	cmd := &cobra.Command{
		Use:          "claude-attention --state-file FILE --generation GEN --config-dir DIR -- COMMAND [ARG...]",
		Short:        "Publish semantic attention for one Claude launch",
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, command []string) error {
			if err := validateClaudeAttentionOptions(options); err != nil {
				return err
			}
			if run == nil {
				return fmt.Errorf("Claude attention runner is unavailable")
			}
			result, err := run(cmd.Context(), options, command)
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
	cmd.Flags().StringVar(&options.StateFile, "state-file", "", "generation-fenced attention state file")
	cmd.Flags().StringVar(&options.Generation, "generation", "", "attention generation identity")
	cmd.Flags().StringVar(&options.ConfigDir, "config-dir", "", "exact Claude account config directory")
	return cmd
}

func validateClaudeAttentionOptions(options claudeAttentionOptions) error {
	if options.StateFile == "" {
		return fmt.Errorf("--state-file is required")
	}
	if options.Generation == "" {
		return fmt.Errorf("--generation is required")
	}
	if options.ConfigDir == "" {
		return fmt.Errorf("--config-dir is required")
	}
	if !claudeAttentionGeneration.MatchString(options.Generation) {
		return fmt.Errorf("invalid attention generation %q", options.Generation)
	}
	cleanState := filepath.Clean(options.StateFile)
	if filepath.Base(cleanState) != "state" || filepath.Base(filepath.Dir(cleanState)) != options.Generation {
		return fmt.Errorf("attention state path does not belong to generation %q", options.Generation)
	}
	return nil
}

func runClaudeAttention(
	ctx context.Context,
	options claudeAttentionOptions,
	command []string,
) (attention.ClaudeExitResult, error) {
	reducer, err := attention.NewClaudeReducer(options.Generation)
	if err != nil {
		return attention.ClaudeExitResult{}, err
	}
	writer, writerErr := attention.NewAtomicWriter(options.StateFile, options.Generation)
	monitoring := writerErr == nil
	workdir := attention.NewWorkingDirectoryWriter(options.StateFile)

	publish := func(state attention.State) {
		if !monitoring {
			return
		}
		if err := writer.Publish(state.Phase, state.Reason, state.Identity); err != nil {
			// Attention is an observer. A removed generation or an unavailable
			// state file must never prevent the user's Claude process from running.
			monitoring = false
		}
	}
	// The sidecar is an observer too, and a separate one: a session whose
	// working directory cannot be published still needs its attention state.
	publishWorkdir := func(dir string) {
		_ = workdir.Publish(dir)
	}

	supervisor := attention.ClaudeSupervisor{
		Poll: func(pollContext context.Context, rootPID int, processes []attention.SupervisorProcess) error {
			if !monitoring {
				return nil
			}
			// processes is the table this tick already read for descendant
			// tracking; reusing it halves the tick's process-table work.
			status, found, pollErr := (attention.ClaudeRegistryMapper{
				ConfigDir:     options.ConfigDir,
				LaunchRootPID: rootPID,
				Processes:     processes,
			}).Poll(pollContext)
			if pollErr != nil {
				found = false
			}
			claudeAttentionPublish(status, found, func(observation attention.ClaudeReducerObservation) {
				publish(reducer.Reduce(observation))
			}, publishWorkdir)
			return nil
		},
		OnExit: func(result attention.ClaudeExitResult) error {
			publish(reducer.ReduceExit(attention.ClaudeReducerExit{
				Code:     result.ExitCode,
				Signaled: result.Signaled,
			}))
			return nil
		},
	}
	return supervisor.Run(ctx, command)
}

// claudeAttentionPublish routes one registry poll to both of the generation's
// outputs. Attention has an answer for "the registry could not be read" —
// unknown — but the working directory does not: a session that stops being
// observable has not moved, so an unfound poll publishes no directory at all
// and the wisp session keeps following the last one it saw.
func claudeAttentionPublish(
	status attention.ClaudeRegistryStatus,
	found bool,
	publishObservation func(attention.ClaudeReducerObservation),
	publishWorkdir func(string),
) {
	publishObservation(claudeRegistryObservation(status, found))
	if found {
		publishWorkdir(status.Cwd)
	}
}

func claudeRegistryObservation(
	status attention.ClaudeRegistryStatus,
	found bool,
) attention.ClaudeReducerObservation {
	if !found {
		return attention.ClaudeReducerObservation{Status: attention.ClaudeObservedUnknown}
	}
	observation := attention.ClaudeReducerObservation{
		StatusUpdatedAt: status.StatusIdentity,
		SessionID:       status.SessionID,
	}
	switch status.Status {
	case "idle":
		observation.Status = attention.ClaudeObservedIdle
	case "busy":
		observation.Status = attention.ClaudeObservedBusy
	case "waiting":
		observation.Status = attention.ClaudeObservedWaiting
		observation.WaitingReason = claudeWaitingReason(status.WaitingFor)
		if observation.WaitingReason == "" {
			return attention.ClaudeReducerObservation{Status: attention.ClaudeObservedUnknown}
		}
	default:
		return attention.ClaudeReducerObservation{Status: attention.ClaudeObservedUnknown}
	}
	return observation
}

func claudeWaitingReason(waitingFor string) attention.ClaudeWaitingReason {
	normalized := strings.ToLower(strings.TrimSpace(waitingFor))
	if normalized == "" {
		// Claude versions predating waitingFor used waiting for approval gates.
		return attention.ClaudeWaitingPermission
	}
	for _, marker := range []string{"permission", "approval", "approve", "authorization", "authorize"} {
		if strings.Contains(normalized, marker) {
			return attention.ClaudeWaitingPermission
		}
	}
	for _, marker := range []string{"question", "input", "answer", "reply", "response", "user"} {
		if strings.Contains(normalized, marker) {
			return attention.ClaudeWaitingQuestion
		}
	}
	return ""
}
