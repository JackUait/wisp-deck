package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// poolSwitchSnippet sources the agent-switch stack INCLUDING session-pool.sh —
// the cross-agent pool helpers relaunch_switch_tool integrates with.
func poolSwitchSnippet(t *testing.T, body string) string {
	t.Helper()
	lib := filepath.Join(projectRoot(t), "lib")
	return fmt.Sprintf(
		"source %q && source %q && source %q && source %q && source %q && %s",
		filepath.Join(lib, "statusline.sh"),
		filepath.Join(lib, "claude-accounts.sh"),
		filepath.Join(lib, "tmux-session.sh"),
		filepath.Join(lib, "session-pool.sh"),
		filepath.Join(lib, "account-switch.sh"),
		body,
	)
}

// poolMockTmux mocks tmux with a STATEFUL session env backed by files in
// dir/tmuxenv (one file per variable), so set-environment stamps written by
// the switch are observable and later show-environment reads see them.
// Everything else (respawn-pane, set-option) is logged to rec.
func poolMockTmux(t *testing.T, dir, rec string) string {
	t.Helper()
	envdir := filepath.Join(dir, "tmuxenv")
	if err := os.MkdirAll(envdir, 0o755); err != nil {
		t.Fatalf("mkdir tmuxenv: %v", err)
	}
	return mockCommand(t, dir, "tmux", fmt.Sprintf(`
envdir=%q
case "$1" in
  show-environment)
    if [ -f "$envdir/$2" ]; then printf '%%s=%%s\n' "$2" "$(cat "$envdir/$2")"; else printf -- '-%%s\n' "$2"; fi
    exit 0 ;;
  set-environment)
    printf '%%s' "$3" > "$envdir/$2"
    printf '%%s\n' "$*" >> %q
    exit 0 ;;
  list-panes) printf '%%%%1 1\n'; exit 0 ;;
  capture-pane) printf '❯\n'; exit 0 ;;
  display-message) printf '0\n'; exit 0 ;;
esac
printf '%%s\n' "$*" >> %q`, envdir, rec, rec))
}

// stampTmuxEnv pre-seeds a variable in poolMockTmux's stateful env.
func stampTmuxEnv(t *testing.T, dir, name, value string) {
	t.Helper()
	writeTempFile(t, filepath.Join(dir, "tmuxenv"), name, value)
}

func readTmuxEnv(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "tmuxenv", name))
	if err != nil {
		return ""
	}
	return string(b)
}

// poolCtx writes a relaunch context whose project dir is /proj and whose
// cfg root is dir (accounts_dir = dir/claude-accounts).
func poolCtx(t *testing.T, dir, tool string) string {
	t.Helper()
	return switcherToolCtx(t, dir, tool, "claude opencode codex")
}

func TestRelaunchSwitchToolSameToolRotatesAttentionBeforeBuild(t *testing.T) {
	dir := t.TempDir()
	attention := createAttentionFixture(t, dir, "opencode")
	oldGeneration := attention["generation"]
	oldState := attention["state"]
	ctx := poolCtx(t, dir, "opencode")
	contextData, err := os.ReadFile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	contextData = []byte(strings.Replace(string(contextData), "tool_cmd=opencode", "tool_cmd=/opt/opencode", 1))
	if err := os.WriteFile(ctx, contextData, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(ctx, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(f, "attention_root=%s\nattention_descriptor=%s\n",
		attention["root"], attention["descriptor"]); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	rec := filepath.Join(dir, "tmux.log")
	bin := poolMockTmux(t, dir, rec)
	envdir := filepath.Join(dir, "tmuxenv")
	body := fmt.Sprintf(`
build_switch_launch_cmd() {
  if [ -n "${WISP_DECK_ATTENTION_GENERATION:-}" ] \
     && [ -f "${WISP_DECK_ATTENTION_FILE:-}" ] \
     && [ "$(cat %q/WISP_DECK_TOOL 2>/dev/null)" = "opencode" ] \
     && [ "$(cat %q/WISP_DECK_ATTENTION_ROOT 2>/dev/null)" = "$WISP_DECK_ATTENTION_ROOT" ] \
     && [ "$(cat %q/WISP_DECK_ATTENTION_DESCRIPTOR 2>/dev/null)" = "$WISP_DECK_ATTENTION_DESCRIPTOR" ] \
     && [ "$(cat %q/WISP_DECK_ATTENTION_GENERATION 2>/dev/null)" = "$WISP_DECK_ATTENTION_GENERATION" ] \
     && [ "$(cat %q/WISP_DECK_ATTENTION_FILE 2>/dev/null)" = "$WISP_DECK_ATTENTION_FILE" ]; then
    printf 'build-ok %%s\n' "$WISP_DECK_ATTENTION_GENERATION" >> %q
  else
    printf 'build-before-attention\n' >> %q
  fi
  printf 'opencode'
}
relaunch_switch_tool tmux %q opencode`,
		envdir, envdir, envdir, envdir, envdir, rec, rec, ctx)
	env := buildEnv(t, []string{bin}, "HOME="+dir, "WISP_DECK_LIB_DIR="+filepath.Join(projectRoot(t), "lib"))
	out, code := runBashSnippet(t, poolSwitchSnippet(t, body), env)
	assertExitCode(t, code, 0)
	if strings.Contains(out, "command not found") {
		t.Fatalf("attention runtime was not available in tool-switch shell: %s", out)
	}

	descriptorRecord, err := os.ReadFile(attention["descriptor"])
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Split(strings.TrimSuffix(string(descriptorRecord), "\n"), "\t")
	if len(fields) != 4 {
		t.Fatalf("rotated descriptor malformed: %q", descriptorRecord)
	}
	newGeneration, newState := fields[1], fields[3]
	if fields[2] != "opencode" || newGeneration == oldGeneration {
		t.Fatalf("same-tool switch did not rotate generation: %q", descriptorRecord)
	}
	if _, err := os.Stat(filepath.Dir(oldState)); !os.IsNotExist(err) {
		t.Fatalf("old tool generation was not fenced: %v", err)
	}
	if _, err := os.Stat(newState); err != nil {
		t.Fatalf("new tool generation state missing: %v", err)
	}

	for name, want := range map[string]string{
		"WISP_DECK_TOOL":                 "opencode",
		"WISP_DECK_ATTENTION_ROOT":       attention["root"],
		"WISP_DECK_ATTENTION_DESCRIPTOR": attention["descriptor"],
		"WISP_DECK_ATTENTION_GENERATION": newGeneration,
		"WISP_DECK_ATTENTION_FILE":       newState,
	} {
		if got := readTmuxEnv(t, dir, name); got != want {
			t.Errorf("tmux %s = %q, want %q", name, got, want)
		}
	}
	logData, err := os.ReadFile(rec)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	logText := string(logData)
	assertNotContains(t, logText, "build-before-attention")
	assertSubstringsInOrder(t, logText,
		"set-environment WISP_DECK_TOOL opencode",
		"set-environment WISP_DECK_ATTENTION_ROOT ",
		"set-environment WISP_DECK_ATTENTION_DESCRIPTOR ",
		"set-environment WISP_DECK_ATTENTION_GENERATION ",
		"set-environment WISP_DECK_ATTENTION_FILE ",
		"build-ok ",
		"respawn-pane",
	)
	contextData, err = os.ReadFile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(contextData), "attention_root="+attention["root"])
	assertContains(t, string(contextData), "attention_descriptor="+attention["descriptor"])
}

func TestRelaunchSwitchTool_unsafe_opencode_prefix_preserves_running_generation(t *testing.T) {
	dir := t.TempDir()
	attention := createAttentionFixture(t, dir, "claude")
	ctx := poolCtx(t, dir, "claude")
	f, err := os.OpenFile(ctx, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(f, "attention_root=%s\nattention_descriptor=%s\n",
		attention["root"], attention["descriptor"]); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	beforeDescriptor, err := os.ReadFile(attention["descriptor"])
	if err != nil {
		t.Fatal(err)
	}
	rec := filepath.Join(dir, "tmux.log")
	bin := poolMockTmux(t, dir, rec)
	data, err := os.ReadFile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "opencode_cmd=/opt/opencode", "opencode_cmd=env EVIL=1 opencode", 1))
	if err := os.WriteFile(ctx, data, 0o600); err != nil {
		t.Fatal(err)
	}
	env := buildEnv(t, []string{bin}, "HOME="+dir,
		"WISP_DECK_LIB_DIR="+filepath.Join(projectRoot(t), "lib"))

	_, code := runBashSnippet(t, poolSwitchSnippet(t,
		fmt.Sprintf("relaunch_switch_tool tmux %q opencode", ctx)), env)
	assertExitCode(t, code, 0)
	afterDescriptor, err := os.ReadFile(attention["descriptor"])
	if err != nil {
		t.Fatal(err)
	}
	if string(afterDescriptor) != string(beforeDescriptor) {
		t.Fatalf("unsafe prefix rotated descriptor\nbefore: %q\nafter:  %q", beforeDescriptor, afterDescriptor)
	}
	if _, err := os.Stat(attention["state"]); err != nil {
		t.Fatalf("unsafe prefix fenced the running generation: %v", err)
	}
	logData, err := os.ReadFile(rec)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	logText := string(logData)
	assertNotContains(t, logText, "respawn-pane")
}

// Leaving codex must capture the pane's codex conversation: stamp its id into
// the session env, record it in the pool meta, and export the handoff.
func TestRelaunchSwitchTool_leaving_codex_captures_session_and_handoff(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	bin := poolMockTmux(t, dir, rec)
	croot := filepath.Join(dir, "codex-sessions")
	uuid := "019c4ee5-2e51-7400-ba62-0000000000aa"
	writeRollout(t, croot, uuid, "/proj", time.Now(),
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"refactor the parser"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Parser refactored"}]}}`,
	)
	ctx := poolCtx(t, dir, "codex")
	env := buildEnv(t, []string{bin}, "HOME="+dir, "WISP_DECK_CODEX_SESSIONS_DIR="+croot)
	_, code := runBashSnippet(t, poolSwitchSnippet(t,
		fmt.Sprintf("relaunch_switch_tool tmux %q claude", ctx)), env)
	assertExitCode(t, code, 0)

	if got := readTmuxEnv(t, dir, "WISP_DECK_CODEX_SESSION"); got != uuid {
		t.Fatalf("expected stamped codex session %q, got %q", uuid, got)
	}
	pool := filepath.Join(dir, "session-pool", "relaunch")
	meta, _ := os.ReadFile(filepath.Join(pool, "meta"))
	assertContains(t, string(meta), "codex="+uuid)
	assertContains(t, string(meta), "last_export_tool=codex")
	handoff, err := os.ReadFile(filepath.Join(pool, "handoff.md"))
	if err != nil {
		t.Fatalf("handoff not exported: %v", err)
	}
	assertContains(t, string(handoff), "refactor the parser")
}

// Switching back to codex with a stamped codex session resumes THAT session
// natively and re-stamps the stint start for the next capture.
func TestRelaunchSwitchTool_returning_to_codex_resumes_stamped_session(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	bin := poolMockTmux(t, dir, rec)
	uuid := "019c4ee5-2e51-7400-ba62-0000000000bb"
	stampTmuxEnv(t, dir, "WISP_DECK_CODEX_SESSION", uuid)
	ctx := poolCtx(t, dir, "claude")
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, poolSwitchSnippet(t,
		fmt.Sprintf("relaunch_switch_tool tmux %q codex", ctx)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "/opt/codex resume "+uuid)
	assertNotContains(t, normalizeShellEscapedSpaces(logOut), "taking over")
	if readTmuxEnv(t, dir, "WISP_DECK_CODEX_STARTED_AT") == "" {
		t.Fatalf("expected WISP_DECK_CODEX_STARTED_AT stamped on switch to codex")
	}
}

// A mid-session tool switch must NOT rewrite the launcher's saved ai-tool
// preference. That file is what wrapper.sh reads to choose the tool for the
// NEXT launch and for OTHER sessions (wrapper.sh:44,122); steering it from an
// in-session switch would leak this session's momentary choice into every
// future session. The session's own tool identity is carried by WISP_DECK_TOOL,
// the pool meta, and the relaunch context — none of which need the global file.
func TestRelaunchSwitchTool_does_not_touch_global_ai_tool_pref(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	bin := poolMockTmux(t, dir, rec)
	// The user's saved launcher preference: claude.
	writeTempFile(t, dir, "ai-tool", "claude\n")
	ctx := poolCtx(t, dir, "claude")
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, poolSwitchSnippet(t,
		fmt.Sprintf("relaunch_switch_tool tmux %q codex", ctx)), env)
	assertExitCode(t, code, 0)

	// The in-session switch stamps WISP_DECK_TOOL — that is where THIS session's
	// tool now lives.
	if got := readTmuxEnv(t, dir, "WISP_DECK_TOOL"); got != "codex" {
		t.Fatalf("expected WISP_DECK_TOOL=codex for this session, got %q", got)
	}
	// But the launcher preference other sessions read must be untouched.
	pref, err := os.ReadFile(filepath.Join(dir, "ai-tool"))
	if err != nil {
		t.Fatalf("ai-tool pref: %v", err)
	}
	if strings.TrimSpace(string(pref)) != "claude" {
		t.Fatalf("mid-session switch leaked into launcher ai-tool pref: got %q, want claude", string(pref))
	}
}

// claude → codex with NO codex session of its own: codex is seeded with the
// exported claude conversation via an initial handoff prompt.
func TestRelaunchSwitchTool_claude_to_codex_seeds_handoff(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	bin := poolMockTmux(t, dir, rec)
	stampTmuxEnv(t, dir, "WISP_DECK_CLAUDE_SESSION", "sid-1")
	writeTempFile(t, filepath.Join(dir, ".claude", "projects", "-proj"), "sid-1.jsonl", strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"design the cache layer"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Designed with LRU"}]}}`,
	}, "\n")+"\n")
	ctx := poolCtx(t, dir, "claude")
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, poolSwitchSnippet(t,
		fmt.Sprintf("relaunch_switch_tool tmux %q codex", ctx)), env)
	assertExitCode(t, code, 0)

	pool := filepath.Join(dir, "session-pool", "relaunch")
	handoff, err := os.ReadFile(filepath.Join(pool, "handoff.md"))
	if err != nil {
		t.Fatalf("handoff not exported: %v", err)
	}
	assertContains(t, string(handoff), "design the cache layer")
	meta, _ := os.ReadFile(filepath.Join(pool, "meta"))
	assertContains(t, string(meta), "claude=sid-1")
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "respawn-pane")
	assertContains(t, normalizeShellEscapedSpaces(logOut), "taking over")
	assertContains(t, logOut, filepath.Join(pool, "handoff.md"))
}

// codex → claude with no claude session: claude is seeded with the codex
// conversation the same way.
func TestRelaunchSwitchTool_codex_to_claude_seeds_handoff(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	bin := poolMockTmux(t, dir, rec)
	croot := filepath.Join(dir, "codex-sessions")
	uuid := "019c4ee5-2e51-7400-ba62-0000000000cc"
	writeRollout(t, croot, uuid, "/proj", time.Now(),
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"port this to rust"}]}}`,
	)
	ctx := poolCtx(t, dir, "codex")
	env := buildEnv(t, []string{bin}, "HOME="+dir, "WISP_DECK_CODEX_SESSIONS_DIR="+croot)
	_, code := runBashSnippet(t, poolSwitchSnippet(t,
		fmt.Sprintf("relaunch_switch_tool tmux %q claude", ctx)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "/opt/claude")
	assertContains(t, normalizeShellEscapedSpaces(logOut), "taking over")
	assertContains(t, logOut, "codex")
}

// opencode round-trip: leaving opencode stamps the flag; returning launches
// `--continue` so the project's opencode session carries on.
func TestRelaunchSwitchTool_opencode_round_trip_uses_continue(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	bin := poolMockTmux(t, dir, rec)
	ctx := poolCtx(t, dir, "opencode")
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, poolSwitchSnippet(t,
		fmt.Sprintf("relaunch_switch_tool tmux %q claude", ctx)), env)
	assertExitCode(t, code, 0)
	if readTmuxEnv(t, dir, "WISP_DECK_OPENCODE_ACTIVE") != "1" {
		t.Fatalf("expected WISP_DECK_OPENCODE_ACTIVE stamped when leaving opencode")
	}
	// The context was rewritten to tool=claude by the first switch; go back.
	_, code = runBashSnippet(t, poolSwitchSnippet(t,
		fmt.Sprintf("relaunch_switch_tool tmux %q opencode", ctx)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "/opt/opencode --continue")
}

// claude → opencode with no opencode stint: opencode takes no positional
// prompt, but its TUI has a --prompt flag — the handoff must ride in on it or
// the conversation silently dies at the opencode border (the observed real-use
// failure).
func TestRelaunchSwitchTool_claude_to_opencode_seeds_handoff_via_prompt_flag(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	bin := poolMockTmux(t, dir, rec)
	stampTmuxEnv(t, dir, "WISP_DECK_CLAUDE_SESSION", "sid-2")
	writeTempFile(t, filepath.Join(dir, ".claude", "projects", "-proj"), "sid-2.jsonl",
		`{"type":"user","message":{"role":"user","content":"ship the release"}}`+"\n")
	ctx := poolCtx(t, dir, "claude")
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, poolSwitchSnippet(t,
		fmt.Sprintf("relaunch_switch_tool tmux %q opencode", ctx)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "/opt/opencode")
	assertContains(t, logOut, "--prompt")
	assertContains(t, normalizeShellEscapedSpaces(logOut), "taking over")
}

// An opencode pane that already has its own session resumes it (--continue)
// and must NOT also be fed the handoff prompt.
func TestRelaunchSwitchTool_opencode_with_own_session_not_seeded(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	bin := poolMockTmux(t, dir, rec)
	stampTmuxEnv(t, dir, "WISP_DECK_OPENCODE_ACTIVE", "1")
	pool := filepath.Join(dir, "session-pool", "relaunch")
	writeTempFile(t, pool, "handoff.md", "# Conversation so far\n\n**User:** x\n")
	writeTempFile(t, pool, "meta", "last_export_tool=claude\n")
	ctx := poolCtx(t, dir, "claude")
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, poolSwitchSnippet(t,
		fmt.Sprintf("relaunch_switch_tool tmux %q opencode", ctx)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "--continue")
	assertNotContains(t, logOut, "--prompt")
}

// reload_switcher_lib must also re-source session-pool.sh: a long-running
// ledger that predates the pool would otherwise switch with pool helpers
// missing — capture and handoff silently skipped — until the pane restarts.
func TestReloadSwitcherLib_loads_session_pool(t *testing.T) {
	dir := t.TempDir()
	root := projectRoot(t)
	for _, f := range []string{"account-switch.sh", "session-pool.sh"} {
		src, err := os.ReadFile(filepath.Join(root, "lib", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), src, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate a pre-pool ledger: account-switch.sh loaded, session-pool NOT.
	body := fmt.Sprintf(`
source %q
type pool_dir >/dev/null 2>&1 && echo BEFORE-DEFINED || echo BEFORE-MISSING
reload_switcher_lib %q
type pool_dir >/dev/null 2>&1 && echo AFTER-DEFINED || echo AFTER-MISSING
`, filepath.Join(dir, "account-switch.sh"), dir)
	out, code := runBashSnippet(t, body, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "BEFORE-MISSING")
	assertContains(t, out, "AFTER-DEFINED")
}

// A /new-closed claude conversation must not be resurrected through the pool:
// when the durable stamp survives but the live id moved on, leaving claude
// clears the handoff so the next agent starts clean.
func TestRelaunchSwitchTool_new_closed_claude_clears_handoff(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	bin := poolMockTmux(t, dir, rec)
	stampTmuxEnv(t, dir, "WISP_DECK_CLAUDE_SESSION", "sid-old")
	stampTmuxEnv(t, dir, "WISP_DECK_CLAUDE_LIVE_SESSION", "sid-new")
	pool := filepath.Join(dir, "session-pool", "relaunch")
	writeTempFile(t, pool, "handoff.md", "# Conversation so far\n\n**User:** old convo\n")
	writeTempFile(t, pool, "meta", "last_export_tool=claude\n")
	ctx := poolCtx(t, dir, "claude")
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, poolSwitchSnippet(t,
		fmt.Sprintf("relaunch_switch_tool tmux %q codex", ctx)), env)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(filepath.Join(pool, "handoff.md")); !os.IsNotExist(err) {
		t.Fatalf("expected handoff cleared after /new-closed claude conversation")
	}
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertNotContains(t, normalizeShellEscapedSpaces(logOut), "taking over")
}
