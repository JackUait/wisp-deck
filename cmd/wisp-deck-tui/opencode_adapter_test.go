package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jackuait/wisp-deck/internal/opencodeadapter"
)

func TestOpenCodeAdapterCommandPreservesValidatedPrefixAndHandoff(t *testing.T) {
	project := t.TempDir()
	physical, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	var got openCodeAdapterOptions
	cmd := newOpenCodeAdapterCommand(
		func(_ context.Context, options openCodeAdapterOptions) (opencodeadapter.ExitResult, error) {
			got = options
			return opencodeadapter.ExitResult{}, nil
		},
		func(int) {}, func() (string, error) { return project, nil },
	)
	cmd.SetArgs([]string{
		"--state-file", "/tmp/generation.OpenCode/state",
		"--generation", "generation.OpenCode",
		"--continue", "--prompt", "--hostile; $(must-not-run)",
		"--", "/usr/local/bin/npx", "--no-install", "opencode-ai",
	})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := openCodeAdapterOptions{
		Prefix:    []string{"/usr/local/bin/npx", "--no-install", "opencode-ai"},
		StateFile: "/tmp/generation.OpenCode/state", Generation: "generation.OpenCode",
		ProjectDir: physical, Continue: true, Prompt: "--hostile; $(must-not-run)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("options = %#v, want %#v", got, want)
	}
}

func TestOpenCodeAdapterCommandRejectsUnsafeInvocations(t *testing.T) {
	valid := []string{
		"--state-file", "/tmp/generation.OpenCode/state",
		"--generation", "generation.OpenCode",
		"--", "/opt/opencode",
	}
	tests := []struct {
		name string
		args []string
		cwd  func() (string, error)
	}{
		{name: "missing prefix", args: valid[:4]},
		{name: "relative prefix", args: []string{"--state-file", "/tmp/generation.OpenCode/state", "--generation", "generation.OpenCode", "--", "opencode"}},
		{name: "unsupported prefix args", args: append(append([]string(nil), valid...), "unexpected")},
		{name: "missing state", args: removeCLIArg(valid, "--state-file")},
		{name: "wrong state owner", args: replaceCLIArg(valid, "--state-file", "/tmp/generation.Other/state")},
		{name: "bad generation", args: replaceCLIArg(valid, "--generation", "generation.bad-name")},
		{name: "continue and session", args: append([]string{"--continue", "--session", "session-1"}, valid...)},
		{name: "cwd failure", args: valid, cwd: func() (string, error) { return "", errors.New("cwd failed") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			cwd := test.cwd
			if cwd == nil {
				cwd = func() (string, error) { return "/workspace/project", nil }
			}
			cmd := newOpenCodeAdapterCommand(
				func(context.Context, openCodeAdapterOptions) (opencodeadapter.ExitResult, error) {
					called = true
					return opencodeadapter.ExitResult{}, nil
				},
				func(int) {}, cwd,
			)
			cmd.SetArgs(test.args)
			if err := cmd.ExecuteContext(context.Background()); err == nil {
				t.Fatal("invalid OpenCode adapter invocation accepted")
			}
			if called {
				t.Fatal("runner called for invalid invocation")
			}
		})
	}
}

func TestOpenCodeAdapterCommandPreservesExitStatusAndIsRegistered(t *testing.T) {
	var exits []int
	project := t.TempDir()
	cmd := newOpenCodeAdapterCommand(
		func(context.Context, openCodeAdapterOptions) (opencodeadapter.ExitResult, error) {
			return opencodeadapter.ExitResult{ExitCode: 29}, nil
		},
		func(code int) { exits = append(exits, code) },
		func() (string, error) { return project, nil },
	)
	cmd.SetArgs([]string{"--state-file", "/tmp/generation.OpenCode/state", "--generation", "generation.OpenCode", "--", "/opt/opencode"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(exits, []int{29}) {
		t.Fatalf("exit calls = %#v", exits)
	}
	registered, _, err := rootCmd.Find([]string{"opencode-adapter"})
	if err != nil || registered.Name() != "opencode-adapter" {
		t.Fatalf("registered command = %v, %v", registered, err)
	}
}
