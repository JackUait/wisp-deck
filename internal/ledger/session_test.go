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

func TestSessionContextParsesSubscriptionKeys(t *testing.T) {
	directory := t.TempDir()
	path := writeSessionFixture(t, directory, "relaunch", strings.Join([]string{
		"tool=claude",
		"config_pointer=/config/claude-config",
		"configs_list=/config/claude-configs.list",
	}, "\n"))

	got, err := ParseSessionContext(path)

	if err != nil {
		t.Fatal(err)
	}
	if got.ConfigPointer != "/config/claude-config" || got.ConfigsList != "/config/claude-configs.list" {
		t.Fatalf("parsed subscription keys = %#v", got)
	}
}

func subscriptionSessionFixture(t *testing.T, directory, stampLine string) (string, *recordingProcessRunner) {
	t.Helper()
	pointer := writeSessionFixture(t, directory, "claude-account", "work\n")
	list := writeSessionFixture(t, directory, "claude-accounts.list", "Work:work\nPersonal:personal\n")
	colors := writeSessionFixture(t, directory, "claude-account-colors", "work:170\npersonal:39\n")
	configPointer := writeSessionFixture(t, directory, "claude-config", "openai-chatgpt.json\n")
	configsList := writeSessionFixture(t, directory, "claude-configs.list", "Zhipu GLM:zhipu-glm.json\nOpenAI / ChatGPT:openai-chatgpt.json\n")
	writeSessionFixture(t, directory, "claude-config-colors", "openai-chatgpt.json:205\ndeleted-config.json:220\n")
	relaunch := writeSessionFixture(t, directory, "relaunch", strings.Join([]string{
		"tool=claude", "tools=claude", "pointer=" + pointer, "list=" + list,
		"colors=" + colors, "config_pointer=" + configPointer, "configs_list=" + configsList,
	}, "\n"))
	runner := &recordingProcessRunner{run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "tmux" && len(args) == 2 && args[0] == "show-environment" {
			switch args[1] {
			case "WISP_DECK_CLAUDE_ACCOUNT":
				return []byte("WISP_DECK_CLAUDE_ACCOUNT=work\n"), nil
			case "WISP_DECK_CLAUDE_CONFIG":
				return []byte(stampLine + "\n"), nil
			}
		}
		return nil, nil
	}}
	return relaunch, runner
}

func TestSessionPillShowsStampedSubscriptionName(t *testing.T) {
	relaunch, runner := subscriptionSessionFixture(t, t.TempDir(), "WISP_DECK_CLAUDE_CONFIG=openai-chatgpt.json")
	source := NewSessionSource(runner)

	got, err := source.Load(context.Background(), relaunch)

	if err != nil {
		t.Fatal(err)
	}
	if got.Pill == nil || got.Pill.Label != "OpenAI / ChatGPT" || got.Pill.Color != 205 {
		t.Fatalf("subscription pill = %#v", got.Pill)
	}
}

func TestSessionPillStampedStandardShowsAccount(t *testing.T) {
	// A stamped empty value means THIS pane runs standard Claude even though the
	// global pointer names a subscription; the account label must win.
	relaunch, runner := subscriptionSessionFixture(t, t.TempDir(), "WISP_DECK_CLAUDE_CONFIG=")
	source := NewSessionSource(runner)

	got, err := source.Load(context.Background(), relaunch)

	if err != nil {
		t.Fatal(err)
	}
	if got.Pill == nil || got.Pill.Label != "Work" || got.Pill.Color != 170 {
		t.Fatalf("stamped-standard pill = %#v", got.Pill)
	}
}

func TestSessionPillUnstampedFallsBackToConfigPointer(t *testing.T) {
	// An unstamped session (tmux prints -NAME) reads the global config pointer.
	relaunch, runner := subscriptionSessionFixture(t, t.TempDir(), "-WISP_DECK_CLAUDE_CONFIG")
	source := NewSessionSource(runner)

	got, err := source.Load(context.Background(), relaunch)

	if err != nil {
		t.Fatal(err)
	}
	if got.Pill == nil || got.Pill.Label != "OpenAI / ChatGPT" || got.Pill.Color != 205 {
		t.Fatalf("pointer-fallback pill = %#v", got.Pill)
	}
}

func TestSessionPillUnknownConfigFallsBackToBareFilename(t *testing.T) {
	directory := t.TempDir()
	relaunch, runner := subscriptionSessionFixture(t, directory, "WISP_DECK_CLAUDE_CONFIG=deleted-config.json")
	source := NewSessionSource(runner)

	got, err := source.Load(context.Background(), relaunch)

	if err != nil {
		t.Fatal(err)
	}
	if got.Pill == nil || got.Pill.Label != "deleted-config" || got.Pill.Color != 220 {
		t.Fatalf("stale-config pill = %#v", got.Pill)
	}
}

func TestSessionPillLegacyContextFallsBackToLaunchPlan(t *testing.T) {
	// A legacy relaunch context (no config_pointer) uses the launch-frozen
	// WISP_DECK_PLAN so the pill still states the subscription.
	directory := t.TempDir()
	list := writeSessionFixture(t, directory, "claude-accounts.list", "Work:work\nPersonal:personal\n")
	relaunch := writeSessionFixture(t, directory, "relaunch", "tool=claude\ntools=claude\nlist="+list+"\n")
	source := NewSessionSource(&recordingProcessRunner{}, WithSessionPlan("OpenAI / ChatGPT"))

	got, err := source.Load(context.Background(), relaunch)

	if err != nil {
		t.Fatal(err)
	}
	if got.Pill == nil || got.Pill.Label != "OpenAI / ChatGPT" || got.Pill.Color != 111 {
		t.Fatalf("legacy plan pill = %#v", got.Pill)
	}
}

func TestSessionPillLegacyStandardPlanShowsAccount(t *testing.T) {
	directory := t.TempDir()
	pointer := writeSessionFixture(t, directory, "claude-account", "work\n")
	list := writeSessionFixture(t, directory, "claude-accounts.list", "Work:work\nPersonal:personal\n")
	colors := writeSessionFixture(t, directory, "claude-account-colors", "work:170\n")
	relaunch := writeSessionFixture(t, directory, "relaunch", strings.Join([]string{
		"tool=claude", "tools=claude", "pointer=" + pointer, "list=" + list, "colors=" + colors,
	}, "\n"))
	source := NewSessionSource(&recordingProcessRunner{}, WithSessionPlan("Standard Claude"))

	got, err := source.Load(context.Background(), relaunch)

	if err != nil {
		t.Fatal(err)
	}
	if got.Pill == nil || got.Pill.Label != "Work" || got.Pill.Color != 170 {
		t.Fatalf("standard-plan pill = %#v", got.Pill)
	}
}

func TestSessionPillEligibleWithOnlySubscriptions(t *testing.T) {
	// No managed accounts and a single agent, but configured subscriptions make
	// the switcher useful — the pill must show (mirrors account_pill_enabled).
	directory := t.TempDir()
	configPointer := writeSessionFixture(t, directory, "claude-config", "openai-chatgpt.json\n")
	configsList := writeSessionFixture(t, directory, "claude-configs.list", "OpenAI / ChatGPT:openai-chatgpt.json\n")
	writeSessionFixture(t, directory, "claude-config-colors", "openai-chatgpt.json:43\n")
	relaunch := writeSessionFixture(t, directory, "relaunch", strings.Join([]string{
		"tool=claude", "tools=claude",
		"config_pointer=" + configPointer, "configs_list=" + configsList,
	}, "\n"))
	source := NewSessionSource(&recordingProcessRunner{})

	got, err := source.Load(context.Background(), relaunch)

	if err != nil {
		t.Fatal(err)
	}
	if got.Pill == nil || got.Pill.Label != "OpenAI / ChatGPT" || got.Pill.Color != 43 {
		t.Fatalf("subscription-only pill = %#v", got.Pill)
	}
}
