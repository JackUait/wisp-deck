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

// Rows are measured through the package's stripANSI so the width assertions
// count what the terminal draws, not the color bytes.

// The main status line renders these two figures as `<brain glyph> 45.0%` and
// `Fable 5 [xhigh]` (statusline-wrapper.sh lines 231/239). The subagent row
// shows the same two facts, so it must present them the same way — a user
// glancing between the bar and the panel should not have to translate between
// `fable-5 · 21% ctx` and `󰧑 21.0% · Fable 5`.
const (
	ctxGlyph  = "\U000f09d1" // the same glyph the status bar prints
	ctxPctSGR = "38;5;178"   // percentage color used by ccstatusline
	modelSGR  = "01;34"      // model color used by the wrapper
	glyphSGR  = "01;33"      // glyph color used by the wrapper
)

// Claude Code sends `model`, `effort` and `contextWindowSize` on every task
// alongside `tokenCount` — contextWindowSize being the resolved window for that
// task's model. Those are exactly what "which model is this agent on and how
// full is it" needs, so the row must spend them rather than drop them.
func TestSubagentStatusline_shows_model_and_used_context(t *testing.T) {
	in := `{"columns":120,"tasks":[{"id":"t1","name":"Explore","status":"running","description":"find the thing","model":"claude-opus-4-5-20260101","effort":"high","contextWindowSize":200000,"tokenCount":68200}]}`
	out, code := renderRows(t, in)
	assertExitCode(t, code, 0)
	rows := parseSubagentRows(t, out)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d: %q", len(rows), out)
	}
	got := stripANSI(rows[0].Content)
	// Model reads as the status bar's display name, with the effort in brackets.
	if !strings.Contains(got, "Opus 4.5 [high]") {
		t.Errorf("model should render like the status line's `Opus 4.5 [high]`: %q", got)
	}
	if strings.Contains(got, "claude-") || strings.Contains(got, "20260101") {
		t.Errorf("vendor prefix and release date should not appear: %q", got)
	}
	// 68200 / 200000 = 34.1%, shown to one decimal exactly like the status bar.
	if !strings.Contains(got, ctxGlyph+" 34.1%") {
		t.Errorf("context should render as `%s 34.1%%`: %q", ctxGlyph, got)
	}
	if strings.Contains(got, "ctx") {
		t.Errorf("the status bar carries no `ctx` label, so neither should the row: %q", got)
	}
	if !strings.Contains(got, "68.2k tok") {
		t.Errorf("token count should still be shown: %q", got)
	}
	// Same colors as the bar, so the two surfaces read as one system. The glyph
	// and model carry the wrapper's exact SGR; the percentage carries the color
	// ccstatusline paints it in, folded together with the bold it also sets.
	raw := rows[0].Content
	for _, sgr := range []string{glyphSGR, modelSGR} {
		if !strings.Contains(raw, "\x1b["+sgr+"m") {
			t.Errorf("row should reuse the status line's SGR %q: %q", sgr, raw)
		}
	}
	if !strings.Contains(raw, ctxPctSGR) {
		t.Errorf("percentage should use the status line's color %q: %q", ctxPctSGR, raw)
	}
}

// A whole percentage still shows its decimal place, matching the bar's `45.0%`.
func TestSubagentStatusline_whole_percentage_keeps_one_decimal(t *testing.T) {
	in := `{"columns":120,"tasks":[{"id":"t","name":"a","model":"claude-fable-5","contextWindowSize":200000,"tokenCount":40000}]}`
	out, code := renderRows(t, in)
	assertExitCode(t, code, 0)
	rows := parseSubagentRows(t, out)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if got := stripANSI(rows[0].Content); !strings.Contains(got, "20.0%") {
		t.Errorf("20%% should render as 20.0%%: %q", got)
	}
}

// Model ids come in several shapes; each must reduce to the name the status bar
// would show. No effort field means no bracket.
func TestSubagentStatusline_model_display_names(t *testing.T) {
	for _, tc := range []struct{ id, want string }{
		{"claude-fable-5", "Fable 5"},
		{"claude-sonnet-4-5", "Sonnet 4.5"},
		{"claude-haiku-4-5-20251001", "Haiku 4.5"},
		{"claude-opus-4-5-20260101", "Opus 4.5"},
		{"claude-3-5-sonnet-20241022", "Sonnet 3.5"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			in := fmt.Sprintf(`{"columns":120,"tasks":[{"id":"t","name":"a","model":%q,"tokenCount":10}]}`, tc.id)
			out, code := renderRows(t, in)
			assertExitCode(t, code, 0)
			rows := parseSubagentRows(t, out)
			if len(rows) != 1 {
				t.Fatalf("expected 1 row, got %d", len(rows))
			}
			got := stripANSI(rows[0].Content)
			if !strings.Contains(got, tc.want) {
				t.Errorf("model %q should display as %q: %q", tc.id, tc.want, got)
			}
			if strings.Contains(got, "[") {
				t.Errorf("no effort supplied, so no bracket expected: %q", got)
			}
		})
	}
}

// Not every task carries a model (a queued agent has not picked one yet). The
// row must then simply omit those segments rather than render an empty slot or
// a stray separator.
func TestSubagentStatusline_omits_model_and_context_when_absent(t *testing.T) {
	in := `{"columns":120,"tasks":[{"id":"t","name":"agent","description":"do a thing","tokenCount":500}]}`
	out, code := renderRows(t, in)
	assertExitCode(t, code, 0)
	rows := parseSubagentRows(t, out)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	got := stripANSI(rows[0].Content)
	if strings.Contains(got, ctxGlyph) || strings.Contains(got, "%") {
		t.Errorf("no context segment expected without contextWindowSize: %q", got)
	}
	if !strings.Contains(got, "500 tok") {
		t.Errorf("token count should still render: %q", got)
	}
	if strings.Contains(got, "· ·") || strings.HasSuffix(strings.TrimSpace(got), "·") {
		t.Errorf("absent segments must not leave a dangling separator: %q", got)
	}
}

// A zero or missing window must not divide by zero — that would abort the jq
// program and blank out EVERY row in the panel, not just this one.
func TestSubagentStatusline_zero_context_window_renders_no_percentage(t *testing.T) {
	in := `{"columns":120,"tasks":[
		{"id":"a","name":"one","model":"claude-opus-4-5","contextWindowSize":0,"tokenCount":10},
		{"id":"b","name":"two","model":"claude-opus-4-5","contextWindowSize":null,"tokenCount":20}
	]}`
	out, code := renderRows(t, in)
	assertExitCode(t, code, 0)
	rows := parseSubagentRows(t, out)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %q", len(rows), out)
	}
	for _, r := range rows {
		got := stripANSI(r.Content)
		if strings.Contains(got, ctxGlyph) || strings.Contains(got, "%") {
			t.Errorf("row %s should have no context segment: %q", r.ID, got)
		}
		if !strings.Contains(got, "Opus 4.5") {
			t.Errorf("row %s should still show the model: %q", r.ID, got)
		}
	}
}

// The new segments eat width, so the description budget has to account for them
// or the row overflows and tmux wraps it.
func TestSubagentStatusline_row_with_model_and_context_fits_columns(t *testing.T) {
	const cols = 60
	longDesc := strings.Repeat("word ", 40)
	in := fmt.Sprintf(`{"columns":%d,"tasks":[{"id":"t","name":"explorer","description":%q,"model":"claude-opus-4-5-20260101","contextWindowSize":200000,"tokenCount":123456}]}`,
		cols, longDesc)
	out, code := renderRows(t, in)
	assertExitCode(t, code, 0)
	rows := parseSubagentRows(t, out)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	got := stripANSI(rows[0].Content)
	// The glyph is drawn double-width, so it costs one cell more than its rune.
	width := len([]rune(got)) + strings.Count(got, ctxGlyph)
	if width > cols {
		t.Errorf("row is %d cells wide, exceeds columns=%d: %q", width, cols, got)
	}
	if !strings.Contains(got, "Opus 4.5") || !strings.Contains(got, "%") {
		t.Errorf("truncation must sacrifice the description, not the stats: %q", got)
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
