package bash_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================
// subagent status line tests (TestSubagentStatusline_*)
//
// Claude Code's subagentStatusLine command runs once per refresh tick with all
// visible subagent rows passed as a single JSON object on stdin (base hook
// fields + `columns` + a `tasks` array). It must write one JSON line per row to
// override, of the form {"id":"<task id>","content":"<row body>"}. render_subagent_rows
// turns the tasks array into those override lines so the current subagent's
// info (name, status, description, tokens) renders as the user switches between
// subagents in the agent panel.
// ============================================================

// subagentRow mirrors one {"id","content"} override line.
type subagentRow struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

// parseSubagentRows splits render_subagent_rows output into one decoded row per
// non-empty line, failing the test if any line is not valid JSON.
func parseSubagentRows(t *testing.T, out string) []subagentRow {
	t.Helper()
	var rows []subagentRow
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r subagentRow
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("output line is not valid JSON: %q (%v)", line, err)
		}
		rows = append(rows, r)
	}
	return rows
}

func renderRows(t *testing.T, stdin string) (string, int) {
	t.Helper()
	return runBashFuncWithStdin(t, "lib/subagent-statusline.sh", "render_subagent_rows",
		nil, nil, stdin)
}

func TestSubagentStatusline_emits_one_override_line_per_task(t *testing.T) {
	in := `{"columns":120,"tasks":[
		{"id":"t1","name":"explorer","type":"Explore","status":"running","description":"scan the repo","tokenCount":1500},
		{"id":"t2","name":"builder","type":"general","status":"completed","description":"build it","tokenCount":300}
	]}`

	out, code := renderRows(t, in)
	assertExitCode(t, code, 0)

	rows := parseSubagentRows(t, out)
	if len(rows) != 2 {
		t.Fatalf("expected 2 override rows, got %d: %q", len(rows), out)
	}
	if rows[0].ID != "t1" || rows[1].ID != "t2" {
		t.Fatalf("row ids = %q,%q, want t1,t2", rows[0].ID, rows[1].ID)
	}
	// Each row carries the subagent's distinguishing info — name, description and
	// token count — but NOT the status word, which is identical across active
	// rows and just adds noise.
	if !strings.Contains(rows[0].Content, "explorer") {
		t.Errorf("row t1 content missing name: %q", rows[0].Content)
	}
	if !strings.Contains(rows[0].Content, "scan the repo") {
		t.Errorf("row t1 content missing description: %q", rows[0].Content)
	}
	if strings.Contains(rows[0].Content, "running") {
		t.Errorf("row t1 content should not include the status word: %q", rows[0].Content)
	}
	if !strings.Contains(rows[0].Content, "1.5k") {
		t.Errorf("row t1 content missing formatted tokens 1.5k: %q", rows[0].Content)
	}
	if !strings.Contains(rows[1].Content, "builder") {
		t.Errorf("row t2 content missing name: %q", rows[1].Content)
	}
	if strings.Contains(rows[1].Content, "completed") {
		t.Errorf("row t2 content should not include the status word: %q", rows[1].Content)
	}
	if !strings.Contains(rows[1].Content, "300 tok") {
		t.Errorf("row t2 content missing small token count: %q", rows[1].Content)
	}
}

func TestSubagentStatusline_omits_status_and_type_label_for_unnamed_agent(t *testing.T) {
	// Real Claude payloads send name:null for unnamed local agents. The row must
	// then show description + token count only — never the status word ("running")
	// nor the internal type ("local_agent"), both of which read identically across
	// rows and crowd out the description that actually distinguishes them.
	in := `{"columns":120,"tasks":[{"id":"t","name":null,"type":"local_agent","status":"running","description":"W2 move and duplicate fixes","tokenCount":184800}]}`
	out, code := renderRows(t, in)
	assertExitCode(t, code, 0)
	rows := parseSubagentRows(t, out)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if strings.Contains(rows[0].Content, "local_agent") {
		t.Errorf("type label should be dropped for unnamed agents: %q", rows[0].Content)
	}
	if strings.Contains(rows[0].Content, "running") {
		t.Errorf("status word should be dropped: %q", rows[0].Content)
	}
	if !strings.Contains(rows[0].Content, "W2 move and duplicate fixes") {
		t.Errorf("description should be shown: %q", rows[0].Content)
	}
	if !strings.Contains(rows[0].Content, "184.8k tok") {
		t.Errorf("token count should be shown: %q", rows[0].Content)
	}
}

func TestSubagentStatusline_row_content_carries_ansi_color(t *testing.T) {
	in := `{"columns":120,"tasks":[{"id":"a","name":"explorer","status":"running","tokenCount":10}]}`
	out, code := renderRows(t, in)
	assertExitCode(t, code, 0)
	rows := parseSubagentRows(t, out)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if !strings.Contains(rows[0].Content, "\x1b[") {
		t.Errorf("content should embed ANSI escape codes: %q", rows[0].Content)
	}
}

func TestSubagentStatusline_missing_tasks_produces_no_output(t *testing.T) {
	out, code := renderRows(t, `{"columns":80}`)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty output when no tasks, got %q", out)
	}
}

func TestSubagentStatusline_empty_tasks_array_produces_no_output(t *testing.T) {
	out, code := renderRows(t, `{"columns":80,"tasks":[]}`)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty output for empty tasks, got %q", out)
	}
}

func TestSubagentStatusline_missing_token_count_renders_zero(t *testing.T) {
	in := `{"columns":80,"tasks":[{"id":"x","name":"agent","status":"pending"}]}`
	out, code := renderRows(t, in)
	assertExitCode(t, code, 0)
	rows := parseSubagentRows(t, out)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if !strings.Contains(rows[0].Content, "0 tok") {
		t.Errorf("missing token count should render '0 tok': %q", rows[0].Content)
	}
}

func TestSubagentStatusline_long_description_truncated_to_fit_columns(t *testing.T) {
	longDesc := strings.Repeat("word ", 40) // ~200 chars
	in := fmt.Sprintf(`{"columns":40,"tasks":[{"id":"t","name":"explorer","status":"running","description":%q,"tokenCount":100}]}`, longDesc)
	out, code := renderRows(t, in)
	assertExitCode(t, code, 0)
	rows := parseSubagentRows(t, out)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if !strings.Contains(rows[0].Content, "…") {
		t.Errorf("over-long description should be truncated with an ellipsis: %q", rows[0].Content)
	}
	if strings.Contains(rows[0].Content, strings.TrimSpace(longDesc)) {
		t.Errorf("full description should not appear when truncated: %q", rows[0].Content)
	}
}

func TestSubagentStatusline_invalid_json_does_not_crash(t *testing.T) {
	out, code := renderRows(t, `not json at all`)
	// Must degrade gracefully (exit 0, no output) so the agent panel keeps its
	// default rendering rather than the whole panel erroring out.
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected no output on invalid input, got %q", out)
	}
}

// ============================================================
// merge_subagent_statusline tests (settings.json wiring)
// ============================================================

func TestSettingsJson_merge_subagent_statusline_creates_file_when_missing(t *testing.T) {
	tmpDir := t.TempDir()
	settingsFile := filepath.Join(tmpDir, "settings.json")

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`merge_subagent_statusline %q`, settingsFile))
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "subagent status line")

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("settings.json should have been created: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("settings.json should be valid JSON: %v\n%s", err, data)
	}
	if _, ok := parsed["subagentStatusLine"]; !ok {
		t.Errorf("expected subagentStatusLine key, got: %s", data)
	}
	assertContains(t, string(data), "subagent-statusline.sh")
}

func TestSettingsJson_merge_subagent_statusline_appends_to_existing_statusline(t *testing.T) {
	tmpDir := t.TempDir()
	settingsFile := filepath.Join(tmpDir, "settings.json")

	// First add the regular statusLine, then the subagent one — the real install
	// order. The result must be valid JSON carrying BOTH keys.
	snippet := settingsJsonSnippet(t, fmt.Sprintf(
		`merge_claude_settings %q && merge_subagent_statusline %q`, settingsFile, settingsFile))
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "subagent status line")

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("settings.json should be valid JSON after both merges: %v\n%s", err, data)
	}
	if _, ok := parsed["statusLine"]; !ok {
		t.Errorf("statusLine key lost: %s", data)
	}
	if _, ok := parsed["subagentStatusLine"]; !ok {
		t.Errorf("subagentStatusLine key missing: %s", data)
	}
}

func TestSettingsJson_merge_subagent_statusline_is_idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	settingsFile := filepath.Join(tmpDir, "settings.json")

	snippet := settingsJsonSnippet(t, fmt.Sprintf(
		`merge_subagent_statusline %q && merge_subagent_statusline %q`, settingsFile, settingsFile))
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "already configured")

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}
	if n := strings.Count(string(data), `"subagentStatusLine"`); n != 1 {
		t.Errorf("expected exactly one subagentStatusLine key, got %d:\n%s", n, data)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("settings.json should remain valid JSON: %v\n%s", err, data)
	}
}

// ============================================================
// shared-settings allowlist: subagent scripts propagate to every login
// ============================================================

func TestSyncSharedSettings_symlinks_subagent_statusline_scripts(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "standard")
	account := filepath.Join(dir, "account")
	writeSharedFile(t, filepath.Join(source, "subagent-statusline.sh"), "echo hi")
	writeSharedFile(t, filepath.Join(source, "subagent-statusline-helpers.sh"), "echo helpers")
	if err := os.MkdirAll(account, 0o755); err != nil {
		t.Fatal(err)
	}

	_, code := runSync(t, source, account)
	assertExitCode(t, code, 0)

	for _, item := range []string{"subagent-statusline.sh", "subagent-statusline-helpers.sh"} {
		dest := filepath.Join(account, item)
		target, err := os.Readlink(dest)
		if err != nil {
			t.Fatalf("%s should be a symlink: %v", item, err)
		}
		if target != filepath.Join(source, item) {
			t.Fatalf("%s links to %q, want %q", item, target, filepath.Join(source, item))
		}
	}
}
