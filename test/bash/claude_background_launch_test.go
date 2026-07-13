package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func shellFunctionBody(t *testing.T, source, name, next string) string {
	t.Helper()
	start := strings.Index(source, name+"() {")
	if start < 0 {
		t.Fatalf("%s not found", name)
	}
	end := len(source)
	if next != "" {
		offset := strings.Index(source[start:], "\n"+next+"() {")
		if offset < 0 {
			t.Fatalf("end of %s not found", name)
		}
		end = start + offset
	}
	return source[start:end]
}

func TestClaudeBackgroundLaunchCoversInitialAndSwitchedAccountRoots(t *testing.T) {
	root := projectRoot(t)
	wrapperData, err := os.ReadFile(filepath.Join(root, "wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	wrapper := string(wrapperData)
	resolve := strings.Index(wrapper, `WISP_DECK_CLAUDE_ACCOUNT_DIR=""`)
	start := strings.Index(wrapper, "attention_start_claude_background_candidate")
	build := strings.Index(wrapper, `AI_TOOL_CMD="$(resolve_ai_tool_cmd`)
	if resolve < 0 || start < 0 || build < 0 || !(resolve < start && start < build) {
		t.Fatalf("initial broker order invalid: resolve=%d start=%d build=%d", resolve, start, build)
	}
	for _, required := range []string{
		`${WISP_DECK_CLAUDE_ACCOUNT_DIR:-$HOME/.claude}`,
		`"$CLAUDE_CMD"`, `"$_gt_cfg_root"`, `"$WISP_DECK_ATTENTION_ROOT"`,
		`"$WISP_DECK_CLAUDE_BACKGROUND_MODE"`,
	} {
		if !strings.Contains(wrapper[start:build], required) {
			t.Errorf("initial broker launch missing %q", required)
		}
	}

	accountData, err := os.ReadFile(filepath.Join(root, "lib", "account-switch.sh"))
	if err != nil {
		t.Fatal(err)
	}
	account := string(accountData)
	tests := []struct {
		name string
		next string
	}{
		{name: "relaunch_ai_pane", next: "_relaunch_preserving_draft"},
		{name: "relaunch_switch_tool", next: "auto_switch_relaunch"},
	}
	for _, tt := range tests {
		body := shellFunctionBody(t, account, tt.name, tt.next)
		candidate := strings.Index(body, "attention_start_claude_background_candidate")
		build := strings.Index(body, `cmd="$(build_switch_launch_cmd`)
		if candidate < 0 || build < 0 || candidate > build {
			t.Errorf("%s must retain/start the exact-root candidate before build: candidate=%d build=%d", tt.name, candidate, build)
		}
		if !strings.Contains(body, `${new_dir:-$HOME/.claude}`) {
			t.Errorf("%s does not select the exact managed/default root", tt.name)
		}
		if !strings.Contains(body, `background_mode`) {
			t.Errorf("%s does not pass explicit default/isolated broker mode", tt.name)
		}
	}
	for _, forbidden := range []string{"stop_claude_background", "kill_claude_background"} {
		if strings.Contains(account, forbidden) {
			t.Errorf("switch lifecycle stops prior-root candidate via %q", forbidden)
		}
	}
}

func TestClaudeBackgroundLaunchRegeneratesSettingsAfterEveryClaudeRotation(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "lib", "account-switch.sh"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	for _, tt := range []struct {
		name string
		next string
	}{
		{name: "relaunch_ai_pane", next: "_relaunch_preserving_draft"},
		{name: "relaunch_switch_tool", next: "auto_switch_relaunch"},
	} {
		body := shellFunctionBody(t, source, tt.name, tt.next)
		rotate := strings.Index(body, "prepare_attention_relaunch")
		settings := strings.Index(body, "prepare_claude_relaunch_settings")
		build := strings.Index(body, `cmd="$(build_switch_launch_cmd`)
		if rotate < 0 || settings < 0 || build < 0 || !(rotate < settings && settings < build) {
			t.Errorf("%s settings lifecycle invalid: rotate=%d settings=%d build=%d", tt.name, rotate, settings, build)
		}
	}
}

func TestClaudeBackgroundRelaunchesPassBehavioralDefaultAndIsolatedIdentity(t *testing.T) {
	for _, tt := range []struct {
		name       string
		entrypoint string
		chosen     string
		wantMode   string
	}{
		{name: "account default", entrypoint: "account", chosen: "default", wantMode: "default"},
		{name: "account isolated", entrypoint: "account", chosen: "managed", wantMode: "isolated"},
		{name: "tool default", entrypoint: "tool", chosen: "default", wantMode: "default"},
		{name: "tool isolated", entrypoint: "tool", chosen: "managed", wantMode: "isolated"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			attention := createAttentionFixture(t, dir, "claude")
			cfgRoot := filepath.Join(dir, "wisp")
			accountsDir := filepath.Join(cfgRoot, "claude-accounts")
			if err := os.MkdirAll(filepath.Join(accountsDir, "managed"), 0o700); err != nil {
				t.Fatal(err)
			}
			source := writeTempFile(t, dir, "active.json", `{"model":"opus"}`+"\n")
			currentTool := "claude"
			if tt.entrypoint == "tool" {
				currentTool = "codex"
			}
			ctx := writeTempFile(t, dir, "relaunch", strings.Join([]string{
				"tool=" + currentTool,
				"tool_cmd=/opt/" + currentTool,
				"settings=",
				"settings_source=" + source,
				"filter=",
				"project_dir=/proj",
				"accounts_dir=" + accountsDir,
				"pointer=" + filepath.Join(cfgRoot, "claude-account"),
				"list=" + filepath.Join(cfgRoot, "claude-accounts.list"),
				"colors=" + filepath.Join(cfgRoot, "claude-account-colors"),
				"default_label=" + filepath.Join(cfgRoot, "claude-account-default-label"),
				"tools=claude codex",
				"claude_cmd=/opt/claude",
				"codex_cmd=/opt/codex",
				"attention_root=" + attention["root"],
				"attention_descriptor=" + attention["descriptor"],
				"",
			}, "\n"))
			candidateLog := filepath.Join(dir, "candidate.log")
			tmuxLog := filepath.Join(dir, "tmux.log")
			bin := poolMockTmux(t, dir, tmuxLog)
			call := fmt.Sprintf(`relaunch_ai_pane tmux %q %q`, ctx, tt.chosen)
			if tt.entrypoint == "tool" {
				call = fmt.Sprintf(`relaunch_switch_tool tmux %q claude %q`, ctx, tt.chosen)
			}
			body := fmt.Sprintf(`
attention_start_claude_background_candidate() {
  printf '%%s\t%%s\t%%s\t%%s\t%%s\n' "$1" "$2" "$3" "$4" "$5" >> %q
}
build_switch_launch_cmd() { printf 'claude'; }
%s
`, candidateLog, call)
			env := buildEnv(t, []string{bin}, "HOME="+dir, "WISP_DECK_LIB_DIR="+filepath.Join(projectRoot(t), "lib"))
			_, code := runBashSnippet(t, accountSwitchSnippet(t, body), env)
			assertExitCode(t, code, 0)

			data, err := os.ReadFile(candidateLog)
			if err != nil {
				t.Fatal(err)
			}
			wantRoot := filepath.Join(dir, ".claude")
			if tt.wantMode == "isolated" {
				wantRoot = filepath.Join(accountsDir, "managed")
			}
			want := strings.Join([]string{
				"/opt/claude", wantRoot, cfgRoot, attention["root"], tt.wantMode,
			}, "\t") + "\n"
			if string(data) != want {
				t.Fatalf("candidate identity = %q, want %q", data, want)
			}
		})
	}
}
