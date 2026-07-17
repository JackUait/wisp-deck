package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jackuait/wisp-deck/internal/gptbridge"
)

func TestClaudeGPTAdapterCommandPassesExactClaudeArgvAndPhysicalCWD(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "project link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	physical, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	environment := []string{"PATH=/bin", "HOME=/home/test"}
	var got gptbridge.AdapterOptions
	command := newClaudeGPTAdapterCommand(
		func(_ context.Context, options gptbridge.AdapterOptions) (gptbridge.AdapterResult, error) {
			got = options
			return gptbridge.AdapterResult{}, nil
		},
		func(int) {},
		func() (string, error) { return link, nil },
		func() []string { return environment },
	)
	command.SetArgs([]string{
		"--codex", "/opt/Codex App/codex",
		"--", "/bin/bash", "-c", `claude --resume 'hostile; $(no)'`,
	})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got.CodexPath != "/opt/Codex App/codex" {
		t.Fatalf("CodexPath = %q", got.CodexPath)
	}
	if !reflect.DeepEqual(got.ClaudeArgv, []string{
		"/bin/bash", "-c", `claude --resume 'hostile; $(no)'`,
	}) {
		t.Fatalf("ClaudeArgv = %#v", got.ClaudeArgv)
	}
	if got.WorkingDir != physical {
		t.Fatalf("WorkingDir = %q, want %q", got.WorkingDir, physical)
	}
	if got.ClientVersion != Version {
		t.Fatalf("ClientVersion = %q, want %q", got.ClientVersion, Version)
	}
	if !reflect.DeepEqual(got.Environment, environment) {
		t.Fatalf("Environment = %#v", got.Environment)
	}
}

func TestClaudeGPTAdapterCommandRejectsInvalidInvocation(t *testing.T) {
	valid := []string{"--codex", "/opt/codex", "--", "claude"}
	tests := []struct {
		name string
		args []string
		cwd  func() (string, error)
	}{
		{"missing codex", []string{"--", "claude"}, nil},
		{"relative codex", []string{"--codex", "codex", "--", "claude"}, nil},
		{"missing Claude command", []string{"--codex", "/opt/codex", "--"}, nil},
		{"cwd error", valid, func() (string, error) { return "", errors.New("cwd unavailable") }},
		{"relative cwd", valid, func() (string, error) { return "repo", nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			getwd := test.cwd
			if getwd == nil {
				getwd = func() (string, error) { return "/repo", nil }
			}
			command := newClaudeGPTAdapterCommand(
				func(context.Context, gptbridge.AdapterOptions) (gptbridge.AdapterResult, error) {
					called = true
					return gptbridge.AdapterResult{}, nil
				},
				func(int) {},
				getwd,
				func() []string { return nil },
			)
			command.SetArgs(test.args)
			if err := command.ExecuteContext(context.Background()); err == nil {
				t.Fatal("invalid adapter invocation accepted")
			}
			if called {
				t.Fatal("runner called for invalid invocation")
			}
		})
	}
}

func TestClaudeGPTAdapterCommandReturnsRunnerErrorAndPreservesExit(t *testing.T) {
	projectCWD := t.TempDir()
	runnerErr := errors.New("bridge failed")
	command := newClaudeGPTAdapterCommand(
		func(context.Context, gptbridge.AdapterOptions) (gptbridge.AdapterResult, error) {
			return gptbridge.AdapterResult{}, runnerErr
		},
		func(int) { t.Fatal("exit called for runner error") },
		func() (string, error) { return projectCWD, nil },
		func() []string { return nil },
	)
	command.SetArgs([]string{"--codex", "/opt/codex", "--", "claude"})
	if err := command.ExecuteContext(context.Background()); !errors.Is(err, runnerErr) {
		t.Fatalf("error = %v, want runner error", err)
	}

	var exits []int
	command = newClaudeGPTAdapterCommand(
		func(context.Context, gptbridge.AdapterOptions) (gptbridge.AdapterResult, error) {
			return gptbridge.AdapterResult{ExitCode: 29}, nil
		},
		func(code int) { exits = append(exits, code) },
		func() (string, error) { return projectCWD, nil },
		func() []string { return nil },
	)
	command.SetArgs([]string{"--codex", "/opt/codex", "--", "claude"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(exits, []int{29}) {
		t.Fatalf("exit calls = %v, want [29]", exits)
	}
}

func TestClaudeGPTAdapterCommandIsRegisteredAndHidden(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"claude-gpt-adapter"})
	if err != nil {
		t.Fatal(err)
	}
	if command.Name() != "claude-gpt-adapter" {
		t.Fatalf("found command %q", command.Name())
	}
	if !command.Hidden {
		t.Fatal("bridge plumbing must stay hidden from the normal command list")
	}
}
