package bash_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Codex needs its own arm in build_ai_launch_cmd rather than the `default` one.
// The default arm is claude's: it prepends `env -u CLAUDE_CONFIG_DIR` (or a
// CLAUDE_CONFIG_DIR= assignment) and appends `--settings <path>` — neither of
// which `codex` accepts. Panes are created with `-c "$PROJECT_DIR"`, so codex
// also takes no positional path the way opencode does.

func codexLaunchCmd(t *testing.T, env []string, extra string) string {
	t.Helper()
	if env == nil {
		// buildEnv strips inherited WISP_DECK* vars; a bare nil would let the
		// surrounding wisp-deck pane leak WISP_DECK_RESUME etc. into the test.
		env = buildEnv(t, nil)
	}
	args := []string{"codex", "/usr/bin/codex"}
	if extra != "" {
		args = append(args, extra)
	}
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd", args, env)
	assertExitCode(t, code, 0)
	return strings.TrimSpace(out)
}

func TestBuildAiLaunchCmd_codex_is_the_bare_command(t *testing.T) {
	if got := codexLaunchCmd(t, nil, ""); got != "/usr/bin/codex" {
		t.Errorf("got %q, want %q", got, "/usr/bin/codex")
	}
}

// A claude settings file is active for the session, but codex must not receive
// --settings: it would fail to parse the flag and drop the pane to a shell.
func TestBuildAiLaunchCmd_codex_ignores_claude_settings(t *testing.T) {
	env := buildEnv(t, nil, "WISP_DECK_CLAUDE_SETTINGS=/tmp/settings.json")
	got := codexLaunchCmd(t, env, "")
	if strings.Contains(got, "--settings") {
		t.Errorf("codex launch %q must not carry --settings", got)
	}
	if got != "/usr/bin/codex" {
		t.Errorf("got %q, want %q", got, "/usr/bin/codex")
	}
}

// Likewise the CLAUDE_CONFIG_DIR env plumbing (both the assignment form and the
// `env -u` shedding form) is claude-only.
func TestBuildAiLaunchCmd_codex_carries_no_claude_config_dir_plumbing(t *testing.T) {
	for _, env := range [][]string{
		buildEnv(t, nil), // -> claude's default arm would emit `env -u CLAUDE_CONFIG_DIR `
		buildEnv(t, nil, "WISP_DECK_CLAUDE_ACCOUNT_DIR=/tmp/acct"),
	} {
		got := codexLaunchCmd(t, env, "")
		if strings.Contains(got, "CLAUDE_CONFIG_DIR") {
			t.Errorf("codex launch %q must not mention CLAUDE_CONFIG_DIR", got)
		}
	}
}

// The screenshot-drag filter wraps claude only.
func TestBuildAiLaunchCmd_codex_carries_no_screenshot_filter(t *testing.T) {
	env := buildEnv(t, nil, "WISP_DECK_CLAUDE_FILTER=wisp-deck-tui screenshot-filter -- ")
	if got := codexLaunchCmd(t, env, ""); got != "/usr/bin/codex" {
		t.Errorf("got %q, want %q", got, "/usr/bin/codex")
	}
}

// opencode gets the project dir as a positional arg; codex must not, because the
// pane's cwd is already the project dir.
func TestBuildAiLaunchCmd_codex_takes_no_positional_project_dir(t *testing.T) {
	if got := codexLaunchCmd(t, nil, "/p/app"); got != "/usr/bin/codex" {
		t.Errorf("got %q, want %q", got, "/usr/bin/codex")
	}
}

// Resume mode with a captured session id: codex resumes ITS exact session via
// a guarded `codex resume <id>` that falls back to the resume selector when
// the exact id fails at startup. It must never open a silent fresh session.
func TestBuildAiLaunchCmd_codex_resume_with_sid_uses_guarded_resume_chain(t *testing.T) {
	env := buildEnv(t, nil, "WISP_DECK_RESUME=1", "WISP_DECK_RESUME_SESSION="+codexSessionA)
	got := codexLaunchCmd(t, env, "")
	assertContains(t, got, "/usr/bin/codex resume "+codexSessionA)
	assertContains(t, got, "_wd_rc")
	assertContains(t, got, "; /usr/bin/codex resume;")
	assertNotContains(t, got, "; /usr/bin/codex;")
}

// Resume mode WITHOUT a captured id opens Codex's selector. A legacy snapshot
// is recoverable by user choice; a plain launch would silently erase context.
func TestBuildAiLaunchCmd_codex_resume_without_sid_uses_picker(t *testing.T) {
	env := buildEnv(t, nil, "WISP_DECK_RESUME=1", "WISP_DECK_RESUME_SESSION=")
	got := codexLaunchCmd(t, env, "")
	if got != "/usr/bin/codex resume" {
		t.Errorf("got %q, want resume picker %q", got, "/usr/bin/codex resume")
	}
	for _, flag := range []string{"--continue", "-c"} {
		if strings.Contains(got, flag) {
			t.Errorf("codex resume launch %q must not carry %q", got, flag)
		}
	}
}

func TestBuildAiLaunchCmd_codex_attention_uses_argv_safe_adapter(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(dir, "args")
	writeExecutable(t, filepath.Join(binDir, "wisp-deck-tui"), `#!/bin/bash
: > "$CAPTURE"
for arg in "$@"; do printf '%s\n' "$arg" >> "$CAPTURE"; done
`)
	state := filepath.Join(dir, "private root", "generation.Abc123", "state")
	env := buildEnv(t, []string{binDir},
		"WISP_DECK_ATTENTION_FILE="+state,
		"WISP_DECK_ATTENTION_GENERATION=generation.Abc123",
		"WISP_DECK_CODEX_SESSION_FILE="+filepath.Join(dir, "session-identities", "dev-app.codex"),
		"WISP_DECK_RESUME_FALLBACK_WINDOW=9",
		"CAPTURE="+capture,
	)
	marker := filepath.Join(dir, "must-not-run")
	prompt := `--hostile prompt; $(touch ` + marker + `)`
	// Pass the hostile value as a real positional parameter. runBashFunc's Go
	// %q helper is not shell-safe for $(), which would test the helper instead
	// of build_ai_launch_cmd.
	module := filepath.Join(projectRoot(t), "lib", "tmux-session.sh")
	build := exec.Command("bash", "-c", `source "$1"; shift; build_ai_launch_cmd "$@"`,
		"bash", module, "codex", "/usr/bin/codex", prompt)
	build.Env = env
	builtBytes, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("build adapter: %v\n%s", err, builtBytes)
	}
	built := strings.TrimSpace(string(builtBytes))
	cmd := exec.Command("bash", "-c", built)
	cmd.Env = env
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("execute built adapter: %v\n%s\ncommand: %s", err, output, built)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("hostile prompt executed shell substitution; marker err=%v", err)
	}
	got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	want := []string{
		"codex-adapter",
		"--codex", "/usr/bin/codex",
		"--state-file", state,
		"--generation", "generation.Abc123",
		"--session-file", filepath.Join(dir, "session-identities", "dev-app.codex"),
		"--fallback-window", "9s",
		"--", prompt,
	}
	if len(got) != len(want) {
		t.Fatalf("adapter args = %#v, want %#v\ncommand: %s", got, want, built)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("adapter arg %d = %q, want %q\nall=%#v\ncommand: %s", i, got[i], want[i], got, built)
		}
	}
}

func TestBuildAiLaunchCmd_codex_attention_resume_is_one_adapter_not_shell_chain(t *testing.T) {
	env := buildEnv(t, nil,
		"WISP_DECK_ATTENTION_FILE=/tmp/generation.Abc/state",
		"WISP_DECK_ATTENTION_GENERATION=generation.Abc",
		"WISP_DECK_CODEX_SESSION_FILE=/tmp/session-identities/dev.codex",
		"WISP_DECK_RESUME=1",
		"WISP_DECK_RESUME_SESSION=11111111-1111-4111-8111-111111111111",
		"WISP_DECK_RESUME_FALLBACK_WINDOW=2.5s",
	)
	got := codexLaunchCmd(t, env, "/repo/must-not-become-prompt")
	for _, want := range []string{
		"wisp-deck-tui codex-adapter",
		"--codex /usr/bin/codex",
		"--state-file /tmp/generation.Abc/state",
		"--generation generation.Abc",
		"--session-file /tmp/session-identities/dev.codex",
		"--resume-session 11111111-1111-4111-8111-111111111111",
		"--fallback-window 2.5s",
	} {
		assertContains(t, got, want)
	}
	for _, forbidden := range []string{"_wd_t0", "_wd_rc", "/repo/must-not-become-prompt"} {
		assertNotContains(t, got, forbidden)
	}
	if !strings.HasSuffix(got, " --") {
		t.Fatalf("adapter command must end in -- for a safely appended handoff: %q", got)
	}
}

func TestBuildAiLaunchCmd_codex_attention_missing_id_uses_resume_picker(t *testing.T) {
	env := buildEnv(t, nil,
		"WISP_DECK_ATTENTION_FILE=/tmp/generation.Abc/state",
		"WISP_DECK_ATTENTION_GENERATION=generation.Abc",
		"WISP_DECK_CODEX_SESSION_FILE=/tmp/session-identities/dev.codex",
		"WISP_DECK_RESUME=1",
	)
	got := codexLaunchCmd(t, env, "")
	for _, want := range []string{
		"wisp-deck-tui codex-adapter",
		"--session-file /tmp/session-identities/dev.codex",
		"--resume-picker",
	} {
		assertContains(t, got, want)
	}
	assertNotContains(t, got, "--resume-session")
}

func TestBuildAiLaunchCmd_codex_attention_requires_session_file(t *testing.T) {
	env := buildEnv(t, nil,
		"WISP_DECK_ATTENTION_FILE=/tmp/generation.Abc/state",
		"WISP_DECK_ATTENTION_GENERATION=generation.Abc",
	)
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"codex", "/usr/bin/codex"}, env)
	if code == 0 {
		t.Fatalf("semantic Codex launch without a sidecar succeeded: %q", out)
	}
}

func TestBuildAiLaunchCmd_codex_without_complete_attention_runtime_keeps_legacyBehavior(t *testing.T) {
	tests := [][]string{
		{"WISP_DECK_ATTENTION_FILE=/tmp/generation.Abc/state"},
		{"WISP_DECK_ATTENTION_GENERATION=generation.Abc"},
	}
	for _, vars := range tests {
		if got := codexLaunchCmd(t, buildEnv(t, nil, vars...), ""); got != "/usr/bin/codex" {
			t.Fatalf("partial attention env produced %q, want legacy bare command", got)
		}
	}
}

// The collapsed signature: opencode still receives the project dir positionally,
// now from the single <tool_cmd> slot.
func TestBuildAiLaunchCmd_opencode_keeps_positional_dir_under_single_slot(t *testing.T) {
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"opencode", "npx --prefer-offline opencode-ai@latest", "/p/app"}, buildEnv(t, nil))
	assertExitCode(t, code, 0)
	want := `npx --prefer-offline opencode-ai@latest "/p/app"`
	if got := strings.TrimSpace(out); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGetToolAccent_codex_is_teal(t *testing.T) {
	out, code := runBashFunc(t, "lib/tmux-session.sh", "get_tool_accent", []string{"codex"}, buildEnv(t, nil))
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "36" {
		t.Errorf("get_tool_accent codex = %q, want 36", got)
	}
}
