package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jackuait/wisp-deck/internal/codexadapter"
)

func TestCodexAdapterCommandValidatesFlagsAndPreservesOneRawPrompt(t *testing.T) {
	projectCWD := t.TempDir()
	physicalCWD, err := filepath.EvalSymlinks(projectCWD)
	if err != nil {
		t.Fatal(err)
	}
	var got codexAdapterOptions
	cmd := newCodexAdapterCommand(
		func(_ context.Context, options codexAdapterOptions) (codexadapter.CodexExitResult, error) {
			got = options
			return codexadapter.CodexExitResult{}, nil
		},
		func(int) {},
		func() (string, error) { return projectCWD, nil },
	)
	cmd.SetArgs([]string{
		"--codex", "/opt/codex bin/codex",
		"--state-file", "/private/root/generation.Abc123/state",
		"--generation", "generation.Abc123",
		"--session-file", "/private/root/session-identities/dev-app-1.codex",
		"--resume-session", "11111111-1111-4111-8111-111111111111",
		"--fallback-window", "7.5s",
		"--", "--hostile prompt; $(must-not-run)",
	})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := codexAdapterOptions{
		CodexPath:      "/opt/codex bin/codex",
		StateFile:      "/private/root/generation.Abc123/state",
		Generation:     "generation.Abc123",
		ProjectCWD:     physicalCWD,
		ClientVersion:  Version,
		ResumeSession:  "11111111-1111-4111-8111-111111111111",
		SessionFile:    "/private/root/session-identities/dev-app-1.codex",
		FallbackWindow: 7500 * time.Millisecond,
		Prompt:         "--hostile prompt; $(must-not-run)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("options = %+v, want %+v", got, want)
	}
}

func TestCodexAdapterCommandResolvesProjectCWDPhysically(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	var got string
	cmd := newCodexAdapterCommand(
		func(_ context.Context, options codexAdapterOptions) (codexadapter.CodexExitResult, error) {
			got = options.ProjectCWD
			return codexadapter.CodexExitResult{}, nil
		},
		func(int) {},
		func() (string, error) { return link, nil },
	)
	cmd.SetArgs(validCodexAdapterArgs())
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("project cwd = %q, want physical path %q", got, want)
	}
}

func TestCodexAdapterCommandRejectsInvalidRuntimeIdentityAndArguments(t *testing.T) {
	validCWD := t.TempDir()
	valid := []string{
		"--codex", "/opt/codex",
		"--state-file", "/tmp/generation.Abc/state",
		"--generation", "generation.Abc",
		"--session-file", "/tmp/session-identities/dev.codex",
		"--fallback-window", "10s",
	}
	tests := []struct {
		name string
		args []string
		cwd  func() (string, error)
	}{
		{"missing codex", valid[2:], nil},
		{"relative codex", replaceCLIArg(valid, "--codex", "codex"), nil},
		{"missing state", removeCLIArg(valid, "--state-file"), nil},
		{"relative state", replaceCLIArg(valid, "--state-file", "generation.Abc/state"), nil},
		{"wrong state basename", replaceCLIArg(valid, "--state-file", "/tmp/generation.Abc/other"), nil},
		{"wrong state generation", replaceCLIArg(valid, "--state-file", "/tmp/generation.Other/state"), nil},
		{"malformed generation", replaceCLIArg(valid, "--generation", "generation.bad-name"), nil},
		{"missing session file", removeCLIArg(valid, "--session-file"), nil},
		{"relative session file", replaceCLIArg(valid, "--session-file", "session-identities/dev.codex"), nil},
		{"session file outside identity dir", replaceCLIArg(valid, "--session-file", "/tmp/dev.codex"), nil},
		{"session file wrong suffix", replaceCLIArg(valid, "--session-file", "/tmp/session-identities/dev.txt"), nil},
		{"unclean session file", replaceCLIArg(valid, "--session-file", "/tmp/session-identities/../dev.codex"), nil},
		{"zero fallback", replaceCLIArg(valid, "--fallback-window", "0s"), nil},
		{"negative fallback", replaceCLIArg(valid, "--fallback-window", "-1s"), nil},
		{"malformed resume UUID", append(append([]string(nil), valid...), "--resume-session", "ABC"), nil},
		{"uppercase resume UUID", append(append([]string(nil), valid...), "--resume-session", "11111111-1111-4111-8111-AAAAAAAAAAAA"), nil},
		{"resume UUID and picker", append(append([]string(nil), valid...), "--resume-session", "11111111-1111-4111-8111-111111111111", "--resume-picker"), nil},
		{"two prompts", append(append([]string(nil), valid...), "--", "one", "two"), nil},
		{"cwd error", valid, func() (string, error) { return "", errors.New("cwd unavailable") }},
		{"relative cwd", valid, func() (string, error) { return "repo", nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			cwd := test.cwd
			if cwd == nil {
				cwd = func() (string, error) { return validCWD, nil }
			}
			cmd := newCodexAdapterCommand(
				func(context.Context, codexAdapterOptions) (codexadapter.CodexExitResult, error) {
					called = true
					return codexadapter.CodexExitResult{}, nil
				},
				func(int) {}, cwd,
			)
			cmd.SetArgs(test.args)
			if err := cmd.ExecuteContext(context.Background()); err == nil {
				t.Fatal("invalid adapter invocation accepted")
			}
			if called {
				t.Fatal("runner called for invalid invocation")
			}
		})
	}
}

func TestCodexAdapterCommandRejectsSymlinkedIdentityDirectory(t *testing.T) {
	target := t.TempDir()
	container := t.TempDir()
	identityDir := filepath.Join(container, "session-identities")
	if err := os.Symlink(target, identityDir); err != nil {
		t.Fatal(err)
	}
	args := replaceCLIArg(
		validCodexAdapterArgs(),
		"--session-file",
		filepath.Join(identityDir, "dev.codex"),
	)
	called := false
	cmd := newCodexAdapterCommand(
		func(context.Context, codexAdapterOptions) (codexadapter.CodexExitResult, error) {
			called = true
			return codexadapter.CodexExitResult{}, nil
		},
		func(int) {},
		func() (string, error) { return t.TempDir(), nil },
	)
	cmd.SetArgs(args)
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("symlinked identity directory accepted")
	}
	if called {
		t.Fatal("runner called with symlinked identity directory")
	}
}

func TestCodexAdapterCommandReturnsRunnerErrorAndPreservesExit(t *testing.T) {
	projectCWD := t.TempDir()
	runnerErr := errors.New("adapter failed")
	cmd := newCodexAdapterCommand(
		func(context.Context, codexAdapterOptions) (codexadapter.CodexExitResult, error) {
			return codexadapter.CodexExitResult{}, runnerErr
		},
		func(int) { t.Fatal("exit called for runner error") },
		func() (string, error) { return projectCWD, nil },
	)
	cmd.SetArgs(validCodexAdapterArgs())
	if err := cmd.ExecuteContext(context.Background()); !errors.Is(err, runnerErr) {
		t.Fatalf("error = %v, want runner error", err)
	}

	var exits []int
	cmd = newCodexAdapterCommand(
		func(context.Context, codexAdapterOptions) (codexadapter.CodexExitResult, error) {
			return codexadapter.CodexExitResult{ExitCode: 23}, nil
		},
		func(code int) { exits = append(exits, code) },
		func() (string, error) { return projectCWD, nil },
	)
	cmd.SetArgs(validCodexAdapterArgs())
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(exits, []int{23}) {
		t.Fatalf("exit calls = %v, want [23]", exits)
	}
}

func TestCodexAdapterCommandIsRegistered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"codex-adapter"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Name() != "codex-adapter" {
		t.Fatalf("found command %q", cmd.Name())
	}
}

func validCodexAdapterArgs() []string {
	return []string{
		"--codex", "/opt/codex",
		"--state-file", filepath.Join("/tmp", "generation.Abc", "state"),
		"--generation", "generation.Abc",
		"--session-file", filepath.Join("/tmp", "session-identities", "dev.codex"),
		"--fallback-window", "10s",
	}
}

func TestCodexAdapterCommandPassesResumePicker(t *testing.T) {
	var got codexAdapterOptions
	cmd := newCodexAdapterCommand(
		func(_ context.Context, options codexAdapterOptions) (codexadapter.CodexExitResult, error) {
			got = options
			return codexadapter.CodexExitResult{}, nil
		},
		func(int) {},
		func() (string, error) { return t.TempDir(), nil },
	)
	args := append(validCodexAdapterArgs(), "--resume-picker")
	cmd.SetArgs(args)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !got.ResumePicker {
		t.Fatal("--resume-picker did not reach adapter options")
	}
}

func replaceCLIArg(args []string, name, value string) []string {
	result := append([]string(nil), args...)
	for i := 0; i+1 < len(result); i++ {
		if result[i] == name {
			result[i+1] = value
			return result
		}
	}
	return result
}

func removeCLIArg(args []string, name string) []string {
	result := make([]string, 0, len(args)-2)
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			i++
			continue
		}
		result = append(result, args[i])
	}
	return result
}
