package ledger

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSessionFixture(t *testing.T, directory, name, content string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSessionContextParsesRelaunchValuesVerbatim(t *testing.T) {
	directory := t.TempDir()
	path := writeSessionFixture(t, directory, "relaunch", strings.Join([]string{
		"tool=claude",
		"filter=prefix=with=equals ",
		"project_dir=/tmp/project with spaces",
		"tools=claude opencode codex",
		"pointer=/config/claude-account",
		"unknown=ignored",
	}, "\n"))

	context, err := ParseSessionContext(path)

	if err != nil {
		t.Fatal(err)
	}
	if context.RelaunchFile != path || context.Tool != "claude" || context.Filter != "prefix=with=equals " || context.ProjectDir != "/tmp/project with spaces" {
		t.Fatalf("parsed context = %#v", context)
	}
	if strings.Join(context.Tools, ",") != "claude,opencode,codex" || context.Pointer != "/config/claude-account" {
		t.Fatalf("parsed tools/pointer = %#v", context)
	}
}

func TestSessionContextHidesPillWhenSwitchingIsIneligible(t *testing.T) {
	directory := t.TempDir()
	list := writeSessionFixture(t, directory, "claude-accounts.list", "# no managed accounts\n")
	relaunch := writeSessionFixture(t, directory, "relaunch", "tool=claude\ntools=claude\nlist="+list+"\n")
	source := NewSessionSource(&recordingProcessRunner{})

	context, err := source.Load(context.Background(), relaunch)

	if err != nil {
		t.Fatal(err)
	}
	if context.Pill != nil {
		t.Fatalf("ineligible context exposed pill %#v", context.Pill)
	}
}

func TestSessionContextShowsRunningClaudeAccountLabelAndColor(t *testing.T) {
	directory := t.TempDir()
	pointer := writeSessionFixture(t, directory, "claude-account", "work\n")
	list := writeSessionFixture(t, directory, "claude-accounts.list", "Work:work\nPersonal:personal\n")
	colors := writeSessionFixture(t, directory, "claude-account-colors", "work:170\npersonal:39\n")
	defaultLabel := writeSessionFixture(t, directory, "default-label", "Keychain\n")
	relaunch := writeSessionFixture(t, directory, "relaunch", strings.Join([]string{
		"tool=claude", "tools=claude opencode", "pointer=" + pointer,
		"list=" + list, "colors=" + colors, "default_label=" + defaultLabel,
	}, "\n"))
	runner := &recordingProcessRunner{run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "tmux" && len(args) > 0 && args[0] == "show-environment" {
			return []byte("WISP_DECK_CLAUDE_ACCOUNT=personal\n"), nil
		}
		return nil, nil
	}}
	source := NewSessionSource(runner)

	got, err := source.Load(context.Background(), relaunch)

	if err != nil {
		t.Fatal(err)
	}
	if got.Pill == nil || got.Pill.Label != "Personal" || got.Pill.Color != 39 {
		t.Fatalf("Claude pill = %#v", got.Pill)
	}
}

func TestSessionContextShowsActiveNonClaudeAgent(t *testing.T) {
	directory := t.TempDir()
	relaunch := writeSessionFixture(t, directory, "relaunch", "tool=opencode\ntools=claude opencode\n")
	source := NewSessionSource(&recordingProcessRunner{})

	got, err := source.Load(context.Background(), relaunch)

	if err != nil {
		t.Fatal(err)
	}
	if got.Pill == nil || got.Pill.Label != "OpenCode" || got.Pill.Color != 141 {
		t.Fatalf("OpenCode pill = %#v", got.Pill)
	}
}

func TestSessionContextSwitcherUsesExistingFlowWithoutInterpolatingPaths(t *testing.T) {
	runner := &recordingProcessRunner{}
	switcher := NewExecAccountSwitcher(runner, "/lib path; false")
	session := SessionContext{RelaunchFile: "/tmp/relaunch '$(false)"}

	if err := switcher.Switch(context.Background(), session); err != nil {
		t.Fatal(err)
	}

	calls := runner.snapshotCalls()
	if len(calls) != 1 || calls[0].name != "bash" {
		t.Fatalf("switcher calls = %#v", calls)
	}
	if len(calls[0].args) != 5 || calls[0].args[0] != "-c" || calls[0].args[2] != "--" || calls[0].args[3] != "/lib path; false" || calls[0].args[4] != session.RelaunchFile {
		t.Fatalf("switcher argv = %#v", calls[0].args)
	}
	program := calls[0].args[1]
	if strings.Contains(program, "/lib path") || strings.Contains(program, session.RelaunchFile) {
		t.Fatalf("paths were interpolated into switcher program %q", program)
	}
	if !strings.Contains(program, "open_account_switcher") || !strings.Contains(program, "account-switch.sh") {
		t.Fatalf("switcher did not reuse existing flow: %q", program)
	}
}
