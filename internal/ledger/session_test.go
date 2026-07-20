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

func TestSessionContextPrecomputesSwitchOptionsWithActiveSubscription(t *testing.T) {
	directory := t.TempDir()
	accountsDir := filepath.Join(directory, "claude-accounts")
	if err := os.MkdirAll(filepath.Join(accountsDir, "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(accountsDir, "personal"), 0o700); err != nil {
		t.Fatal(err)
	}
	list := writeSessionFixture(t, directory, "claude-accounts.list", "Work:work\nPersonal:personal\n")
	colors := writeSessionFixture(t, directory, "claude-account-colors", "default:208\nwork:170\npersonal:39\n")
	defaultLabel := writeSessionFixture(t, directory, "default-label", "Keychain\n")
	configsDir := filepath.Join(directory, "claude-configs")
	writeSessionFixture(t, configsDir, "glm.json",
		`{"env":{"WISP_DECK_SUBSCRIPTION_PROVIDER":"zhipu","ANTHROPIC_AUTH_TOKEN":"sk-x"}}`)
	writeSessionFixture(t, configsDir, "chatgpt.json",
		`{"env":{"WISP_DECK_SUBSCRIPTION_PROVIDER":"openai-chatgpt"}}`)
	configsList := writeSessionFixture(t, directory, "claude-configs.list",
		"GLM:glm.json\nChatGPT:chatgpt.json\n")
	writeSessionFixture(t, directory, "claude-config-colors", "glm.json:75\nchatgpt.json:205\n")
	codexCommand := writeSessionFixture(t, directory, "codex", "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(codexCommand, 0o755); err != nil {
		t.Fatal(err)
	}
	relaunch := writeSessionFixture(t, directory, "relaunch", strings.Join([]string{
		"tool=claude", "tools=claude codex", "codex_cmd=" + codexCommand, "accounts_dir=" + accountsDir,
		"list=" + list, "colors=" + colors, "default_label=" + defaultLabel,
		"config_pointer=" + filepath.Join(directory, "claude-config"),
		"configs_dir=" + configsDir, "configs_list=" + configsList,
	}, "\n"))
	runner := &recordingProcessRunner{run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "tmux" && len(args) == 1 && args[0] == "show-environment" {
			return []byte("WISP_DECK_CLAUDE_ACCOUNT=personal\nWISP_DECK_CLAUDE_CONFIG=chatgpt.json\n"), nil
		}
		return nil, nil
	}}

	got, err := NewSessionSource(runner).Load(context.Background(), relaunch)

	if err != nil {
		t.Fatal(err)
	}
	if len(got.SwitchOptions) != 6 {
		t.Fatalf("switch options = %#v", got.SwitchOptions)
	}
	want := []SwitchOption{
		{Choice: SwitchChoice{Kind: SwitchAccount}, Label: "Keychain", Color: 208, Ready: true},
		{Choice: SwitchChoice{Kind: SwitchAccount, Value: "work"}, Label: "Work", Color: 170, Ready: true},
		{Choice: SwitchChoice{Kind: SwitchAccount, Value: "personal"}, Label: "Personal", Color: 39, Ready: true},
		{Choice: SwitchChoice{Kind: SwitchSubscription, Value: "glm.json"}, Label: "GLM", Color: 75, Glyph: "✦", Ready: true},
		{Choice: SwitchChoice{Kind: SwitchSubscription, Value: "chatgpt.json"}, Label: "ChatGPT", Color: 205, Glyph: "✦", Ready: true, Active: true},
		{Choice: SwitchChoice{Kind: SwitchTool, Value: "codex"}, Label: "Codex", Color: 36, Glyph: "󰛄", Ready: true},
	}
	for i := range want {
		if got.SwitchOptions[i] != want[i] {
			t.Fatalf("option %d = %#v, want %#v", i, got.SwitchOptions[i], want[i])
		}
	}
}

func TestSessionContextMarksChatGPTReadyWithHiddenCodexBridge(t *testing.T) {
	directory := t.TempDir()
	configsDir := filepath.Join(directory, "claude-configs")
	writeSessionFixture(t, configsDir, "chatgpt.json",
		`{"env":{"WISP_DECK_SUBSCRIPTION_PROVIDER":"openai-chatgpt"}}`)
	configsList := writeSessionFixture(t, directory, "claude-configs.list", "ChatGPT:chatgpt.json\n")
	claudeCommand := writeSessionFixture(t, directory, "claude", "#!/bin/sh\nexit 0\n")
	codexCommand := writeSessionFixture(t, directory, "codex", "#!/bin/sh\nexit 0\n")
	for _, command := range []string{claudeCommand, codexCommand} {
		if err := os.Chmod(command, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	relaunch := writeSessionFixture(t, directory, "relaunch", strings.Join([]string{
		"tool=claude", "tool_cmd=" + claudeCommand, "tools=claude",
		"claude_cmd=" + claudeCommand, "codex_cmd=" + codexCommand,
		"configs_dir=" + configsDir, "configs_list=" + configsList,
	}, "\n"))

	got, err := NewSessionSource(&recordingProcessRunner{}).Load(context.Background(), relaunch)
	if err != nil {
		t.Fatal(err)
	}
	for _, option := range got.SwitchOptions {
		if option.Choice == (SwitchChoice{Kind: SwitchSubscription, Value: "chatgpt.json"}) {
			if !option.Ready {
				t.Fatalf("ChatGPT choice was unready with configured Codex bridge: %#v", option)
			}
			return
		}
	}
	t.Fatal("ChatGPT choice missing")
}

func TestSessionContextMarksChatGPTUnreadyWithoutCodexExecutable(t *testing.T) {
	directory := t.TempDir()
	configsDir := filepath.Join(directory, "claude-configs")
	writeSessionFixture(t, configsDir, "chatgpt.json",
		`{"env":{"WISP_DECK_SUBSCRIPTION_PROVIDER":"openai-chatgpt"}}`)
	configsList := writeSessionFixture(t, directory, "claude-configs.list", "ChatGPT:chatgpt.json\n")
	claudeCommand := writeSessionFixture(t, directory, "claude", "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(claudeCommand, 0o755); err != nil {
		t.Fatal(err)
	}
	relaunch := writeSessionFixture(t, directory, "relaunch", strings.Join([]string{
		"tool=claude", "tool_cmd=" + claudeCommand, "tools=claude codex",
		"claude_cmd=" + claudeCommand, "codex_cmd=/missing/codex",
		"configs_dir=" + configsDir, "configs_list=" + configsList,
	}, "\n"))

	got, err := NewSessionSource(&recordingProcessRunner{}).Load(context.Background(), relaunch)
	if err != nil {
		t.Fatal(err)
	}
	for _, option := range got.SwitchOptions {
		if option.Choice == (SwitchChoice{Kind: SwitchSubscription, Value: "chatgpt.json"}) {
			if option.Ready {
				t.Fatalf("ChatGPT choice remained ready without Codex: %#v", option)
			}
			return
		}
	}
	t.Fatal("ChatGPT choice missing")
}

func TestSessionContextLoadsSessionIdentitiesWithOneTmuxQuery(t *testing.T) {
	directory := t.TempDir()
	pointer := writeSessionFixture(t, directory, "claude-account", "work\n")
	list := writeSessionFixture(t, directory, "claude-accounts.list", "Work:work\n")
	configPointer := writeSessionFixture(t, directory, "claude-config", "chatgpt.json\n")
	configsList := writeSessionFixture(t, directory, "claude-configs.list", "ChatGPT:chatgpt.json\n")
	relaunch := writeSessionFixture(t, directory, "relaunch", strings.Join([]string{
		"tool=claude", "tools=claude codex", "pointer=" + pointer, "list=" + list,
		"config_pointer=" + configPointer, "configs_list=" + configsList,
	}, "\n"))
	runner := &recordingProcessRunner{run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "tmux" || len(args) == 0 || args[0] != "show-environment" {
			return nil, nil
		}
		if len(args) == 1 {
			return []byte("WISP_DECK_CLAUDE_ACCOUNT=personal\nWISP_DECK_CLAUDE_CONFIG=chatgpt.json\n"), nil
		}
		switch args[1] {
		case "WISP_DECK_CLAUDE_ACCOUNT":
			return []byte("WISP_DECK_CLAUDE_ACCOUNT=personal\n"), nil
		case "WISP_DECK_CLAUDE_CONFIG":
			return []byte("WISP_DECK_CLAUDE_CONFIG=chatgpt.json\n"), nil
		}
		return nil, nil
	}}

	if _, err := NewSessionSource(runner).Load(context.Background(), relaunch); err != nil {
		t.Fatal(err)
	}

	calls := runner.snapshotCalls()
	if len(calls) != 1 || calls[0].name != "tmux" || len(calls[0].args) != 1 || calls[0].args[0] != "show-environment" {
		t.Fatalf("session identity queries = %#v, want one tmux show-environment", calls)
	}
}

func TestSessionContextDisablesClaudeChoicesWhenClaudeUnavailable(t *testing.T) {
	directory := t.TempDir()
	accountsDir := filepath.Join(directory, "claude-accounts")
	if err := os.MkdirAll(filepath.Join(accountsDir, "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	list := writeSessionFixture(t, directory, "claude-accounts.list", "Work:work\n")
	configsDir := filepath.Join(directory, "claude-configs")
	writeSessionFixture(t, configsDir, "chatgpt.json",
		`{"env":{"WISP_DECK_SUBSCRIPTION_PROVIDER":"openai-chatgpt"}}`)
	configsList := writeSessionFixture(t, directory, "claude-configs.list", "ChatGPT:chatgpt.json\n")
	relaunch := writeSessionFixture(t, directory, "relaunch", strings.Join([]string{
		"tool=codex", "tools=codex", "accounts_dir=" + accountsDir, "list=" + list,
		"configs_dir=" + configsDir, "configs_list=" + configsList,
	}, "\n"))

	got, err := NewSessionSource(&recordingProcessRunner{}).Load(context.Background(), relaunch)
	if err != nil {
		t.Fatal(err)
	}

	var checked int
	for _, option := range got.SwitchOptions {
		if option.Choice.Kind != SwitchAccount && option.Choice.Kind != SwitchSubscription {
			continue
		}
		checked++
		if option.Ready {
			t.Fatalf("Claude choice remained ready without Claude: %#v", option)
		}
	}
	if checked != 3 {
		t.Fatalf("checked %d Claude choices, want default, work, and ChatGPT", checked)
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

func TestExplicitAccountSwitcherAppliesChoiceWithoutOpeningPopupOrProbingCapabilities(t *testing.T) {
	directory := t.TempDir()
	configsDir := filepath.Join(directory, "claude-configs")
	writeSessionFixture(t, configsDir, "chatgpt.json",
		`{"env":{"WISP_DECK_SUBSCRIPTION_PROVIDER":"openai-chatgpt"}}`)
	configsList := writeSessionFixture(t, directory, "claude-configs.list", "ChatGPT:chatgpt.json\n")
	runner := &recordingProcessRunner{}
	switcher := NewExecAccountSwitcher(runner, "/lib path; false")
	session := SessionContext{
		RelaunchFile: "/tmp/relaunch '$(false)",
		ConfigsDir:   configsDir,
		ConfigsList:  configsList,
	}
	choice := SwitchChoice{Kind: SwitchSubscription, Value: "chatgpt.json"}

	if err := switcher.Switch(context.Background(), session, choice); err != nil {
		t.Fatal(err)
	}

	calls := runner.snapshotCalls()
	if len(calls) != 1 || calls[0].name != "bash" {
		t.Fatalf("switcher calls = %#v", calls)
	}
	if len(calls[0].args) != 7 || calls[0].args[0] != "-c" || calls[0].args[2] != "--" ||
		calls[0].args[3] != "/lib path; false" || calls[0].args[4] != session.RelaunchFile ||
		calls[0].args[5] != string(choice.Kind) || calls[0].args[6] != choice.Value {
		t.Fatalf("switcher argv = %#v", calls[0].args)
	}
	program := calls[0].args[1]
	if strings.Contains(program, "/lib path") || strings.Contains(program, session.RelaunchFile) ||
		strings.Contains(program, choice.Value) {
		t.Fatalf("arguments were interpolated into switcher program %q", program)
	}
	if !strings.Contains(program, "apply_account_switch_choice") || !strings.Contains(program, "account-switch.sh") {
		t.Fatalf("switcher did not call the explicit apply flow: %q", program)
	}
	if strings.Contains(program, "open_account_switcher") || strings.Contains(program, "--help") {
		t.Fatalf("explicit apply must not open or capability-probe the popup: %q", program)
	}
}

func TestExplicitAccountSwitcherRejectsSubscriptionThatIsNoLongerReady(t *testing.T) {
	directory := t.TempDir()
	configsDir := filepath.Join(directory, "claude-configs")
	writeSessionFixture(t, configsDir, "glm.json",
		`{"env":{"WISP_DECK_SUBSCRIPTION_PROVIDER":"zhipu"}}`)
	configsList := writeSessionFixture(t, directory, "claude-configs.list", "GLM:glm.json\n")
	runner := &recordingProcessRunner{}
	switcher := NewExecAccountSwitcher(runner, "/lib")

	err := switcher.Switch(context.Background(), SessionContext{
		RelaunchFile: "/tmp/relaunch",
		ConfigsDir:   configsDir,
		ConfigsList:  configsList,
	}, SwitchChoice{Kind: SwitchSubscription, Value: "glm.json"})

	if err == nil {
		t.Fatal("switcher accepted a subscription that lost its API key")
	}
	if calls := runner.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("invalid subscription launched process calls %#v", calls)
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
		if name == "tmux" && len(args) == 1 && args[0] == "show-environment" {
			return []byte("WISP_DECK_CLAUDE_ACCOUNT=work\n" + stampLine + "\n"), nil
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

func TestSessionPillGlyphDistinguishesSubscriptionFromAccount(t *testing.T) {
	directory := t.TempDir()
	relaunch, runner := subscriptionSessionFixture(t, directory, "WISP_DECK_CLAUDE_CONFIG=openai-chatgpt.json")
	source := NewSessionSource(runner)

	got, err := source.Load(context.Background(), relaunch)

	if err != nil {
		t.Fatal(err)
	}
	if got.Pill == nil || got.Pill.Glyph != "✦" {
		t.Fatalf("subscription pill glyph = %#v, want the subscription spark", got.Pill)
	}

	standard, runner2 := subscriptionSessionFixture(t, t.TempDir(), "WISP_DECK_CLAUDE_CONFIG=")
	got2, err := NewSessionSource(runner2).Load(context.Background(), standard)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Pill == nil || got2.Pill.Glyph != "" {
		t.Fatalf("account pill glyph = %#v, want empty (renderer default 󰀄)", got2.Pill)
	}
}

func TestSessionPillLegacyPlanUsesSubscriptionGlyph(t *testing.T) {
	directory := t.TempDir()
	list := writeSessionFixture(t, directory, "claude-accounts.list", "Work:work\n")
	relaunch := writeSessionFixture(t, directory, "relaunch", "tool=claude\ntools=claude\nlist="+list+"\n")
	source := NewSessionSource(&recordingProcessRunner{}, WithSessionPlan("OpenAI / ChatGPT"))

	got, err := source.Load(context.Background(), relaunch)

	if err != nil {
		t.Fatal(err)
	}
	if got.Pill == nil || got.Pill.Glyph != "✦" {
		t.Fatalf("legacy plan pill glyph = %#v, want the subscription spark", got.Pill)
	}
}
