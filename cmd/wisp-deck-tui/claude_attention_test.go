package main

import (
	"context"
	"reflect"
	"testing"

	"github.com/jackuait/wisp-deck/internal/attention"
)

func TestClaudeAttentionCommand_requires_runtime_flags_and_preserves_command(t *testing.T) {
	var gotOptions claudeAttentionOptions
	var gotCommand []string
	cmd := newClaudeAttentionCommand(
		func(_ context.Context, options claudeAttentionOptions, command []string) (attention.ClaudeExitResult, error) {
			gotOptions = options
			gotCommand = append([]string(nil), command...)
			return attention.ClaudeExitResult{}, nil
		},
		func(int) {},
	)
	cmd.SetArgs([]string{
		"--state-file", "/private/root/generation.Abc123/state",
		"--generation", "generation.Abc123",
		"--config-dir", "/cfg/account",
		"--", "bash", "-c", "claude --resume sid; rc=$?; exit $rc",
	})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}
	wantOptions := claudeAttentionOptions{
		StateFile:  "/private/root/generation.Abc123/state",
		Generation: "generation.Abc123",
		ConfigDir:  "/cfg/account",
	}
	if gotOptions != wantOptions {
		t.Fatalf("options = %+v, want %+v", gotOptions, wantOptions)
	}
	wantCommand := []string{"bash", "-c", "claude --resume sid; rc=$?; exit $rc"}
	if !reflect.DeepEqual(gotCommand, wantCommand) {
		t.Fatalf("command = %#v, want %#v", gotCommand, wantCommand)
	}
}

func TestClaudeAttentionCommand_rejects_missing_required_inputs(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"state file", []string{"--generation", "generation.Abc", "--config-dir", "/cfg", "--", "true"}},
		{"generation", []string{"--state-file", "/tmp/generation.Abc/state", "--config-dir", "/cfg", "--", "true"}},
		{"config dir", []string{"--state-file", "/tmp/generation.Abc/state", "--generation", "generation.Abc", "--", "true"}},
		{"child command", []string{"--state-file", "/tmp/generation.Abc/state", "--generation", "generation.Abc", "--config-dir", "/cfg"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			cmd := newClaudeAttentionCommand(
				func(context.Context, claudeAttentionOptions, []string) (attention.ClaudeExitResult, error) {
					called = true
					return attention.ClaudeExitResult{}, nil
				},
				func(int) {},
			)
			cmd.SetArgs(tt.args)
			if err := cmd.ExecuteContext(context.Background()); err == nil {
				t.Fatal("invalid command accepted")
			}
			if called {
				t.Fatal("runner called for invalid command")
			}
		})
	}
}

func TestClaudeAttentionCommand_rejects_malformed_generation_and_state_path(t *testing.T) {
	tests := []struct {
		name       string
		generation string
		stateFile  string
	}{
		{"traversal", "../generation.Abc", "/tmp/generation.Abc/state"},
		{"empty suffix", "generation.", "/tmp/generation./state"},
		{"non alphanumeric suffix", "generation.bad-name", "/tmp/generation.bad-name/state"},
		{"wrong state basename", "generation.Abc", "/tmp/generation.Abc/other"},
		{"wrong state generation", "generation.Abc", "/tmp/generation.Other/state"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newClaudeAttentionCommand(
				func(context.Context, claudeAttentionOptions, []string) (attention.ClaudeExitResult, error) {
					t.Fatal("runner called")
					return attention.ClaudeExitResult{}, nil
				},
				func(int) {},
			)
			cmd.SetArgs([]string{
				"--state-file", tt.stateFile,
				"--generation", tt.generation,
				"--config-dir", "/cfg",
				"--", "true",
			})
			if err := cmd.ExecuteContext(context.Background()); err == nil {
				t.Fatal("malformed runtime identity accepted")
			}
		})
	}
}

func TestClaudeAttentionCommand_preserves_nonzero_child_exit(t *testing.T) {
	var exitCodes []int
	cmd := newClaudeAttentionCommand(
		func(context.Context, claudeAttentionOptions, []string) (attention.ClaudeExitResult, error) {
			return attention.ClaudeExitResult{ExitCode: 23}, nil
		},
		func(code int) { exitCodes = append(exitCodes, code) },
	)
	cmd.SetArgs([]string{
		"--state-file", "/tmp/generation.Abc/state",
		"--generation", "generation.Abc",
		"--config-dir", "/cfg",
		"--", "false",
	})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}
	if !reflect.DeepEqual(exitCodes, []int{23}) {
		t.Fatalf("exit codes = %v, want [23]", exitCodes)
	}
}

func TestClaudeRegistryObservation_maps_validated_status(t *testing.T) {
	tests := []struct {
		name       string
		status     attention.ClaudeRegistryStatus
		found      bool
		wantStatus attention.ClaudeObservedStatus
		wantReason attention.ClaudeWaitingReason
	}{
		{name: "missing", wantStatus: attention.ClaudeObservedUnknown},
		{name: "idle", found: true, status: attention.ClaudeRegistryStatus{Status: "idle", StatusIdentity: "10"}, wantStatus: attention.ClaudeObservedIdle},
		{name: "busy", found: true, status: attention.ClaudeRegistryStatus{Status: "busy", StatusIdentity: "11"}, wantStatus: attention.ClaudeObservedBusy},
		{name: "question", found: true, status: attention.ClaudeRegistryStatus{Status: "waiting", StatusIdentity: "12", WaitingFor: "user input"}, wantStatus: attention.ClaudeObservedWaiting, wantReason: attention.ClaudeWaitingQuestion},
		{name: "permission", found: true, status: attention.ClaudeRegistryStatus{Status: "waiting", StatusIdentity: "13", WaitingFor: "permission prompt"}, wantStatus: attention.ClaudeObservedWaiting, wantReason: attention.ClaudeWaitingPermission},
		{name: "legacy waiting", found: true, status: attention.ClaudeRegistryStatus{Status: "waiting", StatusIdentity: "14"}, wantStatus: attention.ClaudeObservedWaiting, wantReason: attention.ClaudeWaitingPermission},
		{name: "unknown waiting reason", found: true, status: attention.ClaudeRegistryStatus{Status: "waiting", StatusIdentity: "15", WaitingFor: "remote agent"}, wantStatus: attention.ClaudeObservedUnknown},
		{name: "schema drift", found: true, status: attention.ClaudeRegistryStatus{Status: "paused", StatusIdentity: "16"}, wantStatus: attention.ClaudeObservedUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := claudeRegistryObservation(tt.status, tt.found)
			if got.Status != tt.wantStatus || got.WaitingReason != tt.wantReason {
				t.Fatalf("observation = %+v, want status=%q reason=%q", got, tt.wantStatus, tt.wantReason)
			}
			if tt.found && got.Status != attention.ClaudeObservedUnknown && got.StatusUpdatedAt != tt.status.StatusIdentity {
				t.Fatalf("status identity = %q, want %q", got.StatusUpdatedAt, tt.status.StatusIdentity)
			}
		})
	}
}

func TestClaudeAttentionCommand_is_registered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"claude-attention"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if cmd.Name() != "claude-attention" {
		t.Fatalf("found %q", cmd.Name())
	}
}
