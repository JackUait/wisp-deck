package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeRollout creates a codex rollout file under root's dated layout with the
// given session uuid, cwd, and mtime; extra lines follow the session_meta line.
func writeRollout(t *testing.T, root, uuid, cwd string, mtime time.Time, extra ...string) string {
	t.Helper()
	dir := filepath.Join(root, "2026", "07", "11")
	lines := append([]string{
		fmt.Sprintf(`{"timestamp":"x","type":"session_meta","payload":{"id":"%s","cwd":"%s","originator":"codex_cli_rs"}}`, uuid, cwd),
	}, extra...)
	name := "rollout-2026-07-11T10-00-00-" + uuid + ".jsonl"
	p := writeTempFile(t, dir, name, strings.Join(lines, "\n")+"\n")
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	return p
}

func TestCodexCurrentSession_picks_newest_matching_cwd_after_since(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	writeRollout(t, root, "019c4ee5-2e51-7400-ba62-000000000001", "/proj", now.Add(-100*time.Second))
	writeRollout(t, root, "019c4ee5-2e51-7400-ba62-000000000002", "/proj", now.Add(-50*time.Second))
	writeRollout(t, root, "019c4ee5-2e51-7400-ba62-000000000003", "/other", now.Add(-10*time.Second))
	since := fmt.Sprintf("%d", now.Add(-200*time.Second).Unix())
	out, code := runBashFunc(t, "lib/session-pool.sh", "codex_current_session",
		[]string{root, "/proj", since}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "019c4ee5-2e51-7400-ba62-000000000002" {
		t.Fatalf("expected newest matching-cwd uuid, got %q", out)
	}
}

func TestCodexCurrentSession_ignores_rollouts_before_since(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	writeRollout(t, root, "019c4ee5-2e51-7400-ba62-000000000001", "/proj", now.Add(-100*time.Second))
	since := fmt.Sprintf("%d", now.Add(-50*time.Second).Unix())
	out, code := runBashFunc(t, "lib/session-pool.sh", "codex_current_session",
		[]string{root, "/proj", since}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected empty for pre-since rollout, got %q", out)
	}
}

func TestCodexCurrentSession_empty_when_no_cwd_match(t *testing.T) {
	root := t.TempDir()
	writeRollout(t, root, "019c4ee5-2e51-7400-ba62-000000000001", "/other", time.Now())
	out, code := runBashFunc(t, "lib/session-pool.sh", "codex_current_session",
		[]string{root, "/proj", "0"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected empty when no cwd matches, got %q", out)
	}
}

func TestPoolSetGet_round_trip_and_overwrite(t *testing.T) {
	dir := t.TempDir()
	meta := filepath.Join(dir, "meta")
	_, code := runBashFunc(t, "lib/session-pool.sh", "pool_set", []string{meta, "codex", "abc"}, nil)
	assertExitCode(t, code, 0)
	_, code = runBashFunc(t, "lib/session-pool.sh", "pool_set", []string{meta, "last_tool", "codex"}, nil)
	assertExitCode(t, code, 0)
	_, code = runBashFunc(t, "lib/session-pool.sh", "pool_set", []string{meta, "codex", "def"}, nil)
	assertExitCode(t, code, 0)
	out, code := runBashFunc(t, "lib/session-pool.sh", "pool_get", []string{meta, "codex"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "def" {
		t.Fatalf("expected overwritten value def, got %q", out)
	}
	out, _ = runBashFunc(t, "lib/session-pool.sh", "pool_get", []string{meta, "last_tool"}, nil)
	if strings.TrimSpace(out) != "codex" {
		t.Fatalf("expected last_tool codex, got %q", out)
	}
	out, _ = runBashFunc(t, "lib/session-pool.sh", "pool_get", []string{meta, "missing"}, nil)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected empty for missing key, got %q", out)
	}
}

func TestPoolDir_creates_dir_and_prunes_stale_siblings(t *testing.T) {
	cfg := t.TempDir()
	old := filepath.Join(cfg, "session-pool", "dev-old-123")
	writeTempFile(t, old, "meta", "claude=x\n")
	stale := time.Now().Add(-40 * 24 * time.Hour)
	if err := os.Chtimes(old, stale, stale); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	fresh := filepath.Join(cfg, "session-pool", "dev-fresh-456")
	writeTempFile(t, fresh, "meta", "claude=y\n")

	out, code := runBashFunc(t, "lib/session-pool.sh", "pool_dir", []string{cfg, "dev-app-789"}, nil)
	assertExitCode(t, code, 0)
	want := filepath.Join(cfg, "session-pool", "dev-app-789")
	if strings.TrimSpace(out) != want {
		t.Fatalf("expected %q, got %q", want, out)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("pool dir not created: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("stale pool dir not pruned")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh pool dir wrongly pruned: %v", err)
	}
}

func TestExportCodexHandoff_writes_user_and_assistant_texts_only(t *testing.T) {
	root := t.TempDir()
	rollout := writeRollout(t, root, "019c4ee5-2e51-7400-ba62-000000000009", "/proj", time.Now(),
		`{"type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"sandbox rules"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<codex_internal_context>hidden</codex_internal_context>"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the login bug"}]}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"shell","arguments":"{}"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Fixed it in auth.ts"}]}}`,
	)
	out := filepath.Join(root, "handoff.md")
	_, code := runBashFunc(t, "lib/session-pool.sh", "export_codex_handoff",
		[]string{rollout, out}, nil)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("handoff not written: %v", err)
	}
	md := string(data)
	assertContains(t, md, "fix the login bug")
	assertContains(t, md, "Fixed it in auth.ts")
	assertNotContains(t, md, "sandbox rules")
	assertNotContains(t, md, "codex_internal_context")
}

func TestExportClaudeHandoff_handles_string_and_block_content(t *testing.T) {
	dir := t.TempDir()
	transcript := writeTempFile(t, dir, "sid.jsonl", strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"please add tests"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"secret"},{"type":"text","text":"Added three tests"}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"now run them"},{"type":"tool_result","content":"x"}]}}`,
		`{"type":"progress","data":"noise"}`,
	}, "\n")+"\n")
	out := filepath.Join(dir, "handoff.md")
	_, code := runBashFunc(t, "lib/session-pool.sh", "export_claude_handoff",
		[]string{transcript, out}, nil)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("handoff not written: %v", err)
	}
	md := string(data)
	assertContains(t, md, "please add tests")
	assertContains(t, md, "Added three tests")
	assertContains(t, md, "now run them")
	assertNotContains(t, md, "secret")
}

// claude wraps slash commands and local-command output in <...> pseudo-tags
// ("<command-name>/clear</command-name>", "<local-command-caveat>..."). Those
// are harness plumbing, not the user's words — they must not reach the handoff.
func TestExportClaudeHandoff_skips_command_wrapper_messages(t *testing.T) {
	dir := t.TempDir()
	transcript := writeTempFile(t, dir, "sid.jsonl", strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"<command-name>/clear</command-name>"}}`,
		`{"type":"user","message":{"role":"user","content":"<local-command-caveat>Caveat: local commands</local-command-caveat>"}}`,
		`{"type":"user","message":{"role":"user","content":"real question"}}`,
	}, "\n")+"\n")
	out := filepath.Join(dir, "handoff.md")
	_, code := runBashFunc(t, "lib/session-pool.sh", "export_claude_handoff",
		[]string{transcript, out}, nil)
	assertExitCode(t, code, 0)
	data, _ := os.ReadFile(out)
	assertContains(t, string(data), "real question")
	assertNotContains(t, string(data), "command-name")
	assertNotContains(t, string(data), "Caveat")
}

// A real transcript tail is dominated by assistant chunks (one entry per
// streamed block); a flat last-N window exported ZERO user messages in the
// observed failure. Each role gets its own quota so the user's voice always
// survives.
func TestExportClaudeHandoff_keeps_user_voice_when_assistant_dominates(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	lines = append(lines,
		`{"type":"user","message":{"role":"user","content":"first ask"}}`,
		`{"type":"user","message":{"role":"user","content":"second ask"}}`,
	)
	for i := 0; i < 40; i++ {
		lines = append(lines,
			fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"chunk %d"}]}}`, i))
	}
	transcript := writeTempFile(t, dir, "sid.jsonl", strings.Join(lines, "\n")+"\n")
	out := filepath.Join(dir, "handoff.md")
	_, code := runBashFunc(t, "lib/session-pool.sh", "export_claude_handoff",
		[]string{transcript, out}, nil)
	assertExitCode(t, code, 0)
	data, _ := os.ReadFile(out)
	assertContains(t, string(data), "first ask")
	assertContains(t, string(data), "second ask")
	assertContains(t, string(data), "chunk 39")
	// Order must stay chronological: the asks precede the chunks.
	if strings.Index(string(data), "second ask") > strings.Index(string(data), "chunk 39") {
		t.Fatalf("messages out of chronological order:\n%s", data)
	}
}

func TestExportCodexHandoff_keeps_user_voice_when_assistant_dominates(t *testing.T) {
	root := t.TempDir()
	extra := []string{
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"the only ask"}]}}`,
	}
	for i := 0; i < 40; i++ {
		extra = append(extra,
			fmt.Sprintf(`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"chunk %d"}]}}`, i))
	}
	rollout := writeRollout(t, root, "019c4ee5-2e51-7400-ba62-00000000000d", "/proj", time.Now(), extra...)
	out := filepath.Join(root, "handoff.md")
	_, code := runBashFunc(t, "lib/session-pool.sh", "export_codex_handoff",
		[]string{rollout, out}, nil)
	assertExitCode(t, code, 0)
	data, _ := os.ReadFile(out)
	assertContains(t, string(data), "the only ask")
	assertContains(t, string(data), "chunk 39")
}

func TestExportHandoff_missing_source_fails_without_writing(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "handoff.md")
	_, code := runBashFunc(t, "lib/session-pool.sh", "export_claude_handoff",
		[]string{filepath.Join(dir, "nope.jsonl"), out}, nil)
	if code == 0 {
		t.Fatalf("expected non-zero exit for missing transcript")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("handoff must not be written for a missing source")
	}
}

func TestHandoffPrompt_names_file_and_source_agent(t *testing.T) {
	out, code := runBashFunc(t, "lib/session-pool.sh", "handoff_prompt",
		[]string{"/cfg/session-pool/dev-app-1/handoff.md", "codex"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "/cfg/session-pool/dev-app-1/handoff.md")
	assertContains(t, out, "codex")
	assertContains(t, out, "continue")
}

func TestCodexRolloutFor_finds_rollout_by_uuid(t *testing.T) {
	root := t.TempDir()
	want := writeRollout(t, root, "019c4ee5-2e51-7400-ba62-000000000007", "/proj", time.Now())
	writeRollout(t, root, "019c4ee5-2e51-7400-ba62-000000000008", "/proj", time.Now())
	out, code := runBashFunc(t, "lib/session-pool.sh", "codex_rollout_for",
		[]string{root, "019c4ee5-2e51-7400-ba62-000000000007"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != want {
		t.Fatalf("expected %q, got %q", want, out)
	}
}

func TestPoolClaudeTranscript_munges_project_path(t *testing.T) {
	home := t.TempDir()
	out, code := runBashFunc(t, "lib/session-pool.sh", "pool_claude_transcript",
		[]string{"/Users/me/proj.x", "abc-123"}, []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")})
	assertExitCode(t, code, 0)
	want := home + "/.claude/projects/-Users-me-proj-x/abc-123.jsonl"
	if strings.TrimSpace(out) != want {
		t.Fatalf("expected %q, got %q", want, out)
	}
}
