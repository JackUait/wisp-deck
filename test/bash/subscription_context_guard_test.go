package bash_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// A conversation grown under a roomy backend does not shrink when the pane is
// retargeted at a smaller one: the switch relaunches `claude -c` and the whole
// transcript is replayed to the new provider. Landing over its cap is
// unrecoverable — /compact resends the same oversized transcript and 400s too —
// so the size has to be checked while the roomy backend is still live.
func TestClaudeConfigContextBudget_reads_the_declared_window(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "a declared window is returned",
			content: `{"env":{"CLAUDE_CODE_MAX_CONTEXT_TOKENS":"262144"}}`,
			want:    "262144",
		},
		{
			name:    "a config declaring no window yields nothing",
			content: `{"env":{"ANTHROPIC_BASE_URL":"https://example.test"}}`,
			want:    "",
		},
		{
			name:    "a non-numeric window is refused rather than compared",
			content: `{"env":{"CLAUDE_CODE_MAX_CONTEXT_TOKENS":"262144; rm -rf /"}}`,
			want:    "",
		},
		{
			name:    "a numeric JSON value is not a string and is refused",
			content: `{"env":{"CLAUDE_CODE_MAX_CONTEXT_TOKENS":262144}}`,
			want:    "",
		},
		{
			name:    "malformed JSON yields nothing",
			content: `{not json`,
			want:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeTempFile(t, dir, "settings.json", tt.content)
			out, code := runBashFunc(t, "lib/account-switch.sh", "claude_config_context_budget",
				[]string{filepath.Join(dir, "settings.json")}, nil)
			assertExitCode(t, code, 0)
			if strings.TrimSpace(out) != tt.want {
				t.Errorf("got %q, want %q", strings.TrimSpace(out), tt.want)
			}
		})
	}
}

// The provider counts input + cache-read + cache-creation against its window;
// output tokens are not part of the next request's prompt.
func TestAiTranscriptContextTokens_reports_the_last_turns_prompt_size(t *testing.T) {
	dir := t.TempDir()
	transcript := strings.Join([]string{
		`{"type":"assistant","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":20,"cache_creation_input_tokens":5,"output_tokens":999}}}`,
		`{"type":"user","message":{"role":"user"}}`,
		`{"type":"assistant","message":{"usage":{"input_tokens":1026,"cache_read_input_tokens":252928,"cache_creation_input_tokens":0,"output_tokens":254}}}`,
		`{"type":"system","subtype":"turn_duration"}`,
	}, "\n") + "\n"
	writeTempFile(t, dir, "t.jsonl", transcript)
	out, code := runBashFunc(t, "lib/account-switch.sh", "ai_transcript_context_tokens",
		[]string{filepath.Join(dir, "t.jsonl")}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "253954" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "253954")
	}
}

func TestAiTranscriptContextTokens_is_silent_without_a_usable_transcript(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "empty.jsonl", "")
	for _, name := range []string{"empty.jsonl", "absent.jsonl"} {
		out, code := runBashFunc(t, "lib/account-switch.sh", "ai_transcript_context_tokens",
			[]string{filepath.Join(dir, name)}, nil)
		assertExitCode(t, code, 0)
		if strings.TrimSpace(out) != "" {
			t.Errorf("%s: got %q, want empty", name, strings.TrimSpace(out))
		}
	}
}

func TestSubscriptionHoldsConversation(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "kimi.json", `{"env":{"CLAUDE_CODE_MAX_CONTEXT_TOKENS":"262144"}}`)
	writeTempFile(t, dir, "nowindow.json", `{"env":{}}`)
	settings := filepath.Join(dir, "kimi.json")

	fits := strings.Join([]string{
		`{"type":"assistant","message":{"usage":{"input_tokens":1000,"cache_read_input_tokens":120000,"cache_creation_input_tokens":0}}}`,
	}, "\n") + "\n"
	writeTempFile(t, dir, "fits.jsonl", fits)

	overflows := `{"type":"assistant","message":{"usage":{"input_tokens":1026,"cache_read_input_tokens":300000,"cache_creation_input_tokens":0}}}` + "\n"
	writeTempFile(t, dir, "over.jsonl", overflows)

	tests := []struct {
		name       string
		settings   string
		transcript string
		wantCode   int
	}{
		{"a conversation inside the window is allowed", settings, "fits.jsonl", 0},
		{"a conversation larger than the window is refused", settings, "over.jsonl", 1},
		// Refusing on missing data would block routine switches; only a
		// measured overflow is worth blocking.
		{"an unmeasurable conversation is allowed", settings, "absent.jsonl", 0},
		{"a target declaring no window is allowed", filepath.Join(dir, "nowindow.json"), "over.jsonl", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, code := runBashFunc(t, "lib/account-switch.sh", "subscription_holds_conversation",
				[]string{tt.settings, filepath.Join(dir, tt.transcript)}, nil)
			assertExitCode(t, code, tt.wantCode)
		})
	}
}

// The switch derives the transcript directory itself instead of sourcing
// session-restore.sh (which drags in terminals/ghostty.sh). Two copies of one
// rule diverge silently — a wrong directory makes the guard measure nothing and
// wave every switch through — so they are pinned together here.
func TestClaudeProjectDirRule_matches_session_restore(t *testing.T) {
	const project = "/Users/someone/Packages/my_repo.v2-x"
	want, code := runBashFunc(t, "lib/session-restore.sh", "claude_project_dir",
		[]string{project}, nil)
	assertExitCode(t, code, 0)

	got, code := runBashSnippet(t, `
		_rc_project_dir='`+project+`'
		printf '%s\n' "$HOME/.claude/projects/${_rc_project_dir//[^A-Za-z0-9]/-}"
	`, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(got) != strings.TrimSpace(want) {
		t.Errorf("switch derived %q, session-restore derived %q", strings.TrimSpace(got), strings.TrimSpace(want))
	}
}

// A failed turn is recorded as a "<synthetic>" assistant entry whose usage is
// all zeros, and it lands AFTER the last real turn. Reading the final usage
// blindly reports a 254k conversation as 0 tokens, which waves through exactly
// the switch this guard exists to stop.
func TestAiTranscriptContextTokens_ignores_a_synthetic_zero_usage_tail(t *testing.T) {
	dir := t.TempDir()
	transcript := strings.Join([]string{
		`{"type":"assistant","message":{"model":"claude-fable-5","usage":{"input_tokens":1026,"cache_read_input_tokens":252928,"cache_creation_input_tokens":0,"output_tokens":254}}}`,
		`{"type":"user","message":{"role":"user"}}`,
		`{"type":"assistant","message":{"model":"<synthetic>","usage":{"input_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":0}}}`,
		`{"type":"system","subtype":"turn_duration"}`,
	}, "\n") + "\n"
	writeTempFile(t, dir, "synthetic.jsonl", transcript)
	out, code := runBashFunc(t, "lib/account-switch.sh", "ai_transcript_context_tokens",
		[]string{filepath.Join(dir, "synthetic.jsonl")}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "253954" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "253954")
	}
}
