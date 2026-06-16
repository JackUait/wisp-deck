package usage

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTotal_sumsAllColumns(t *testing.T) {
	m := MonthlyUsage{Input: 1, Output: 2, CacheWrite: 3, CacheRead: 4}
	if got := m.Total(); got != 10 {
		t.Fatalf("Total() = %d, want 10", got)
	}
}

func TestParseFile_groupsByMonthAndSumsTypes(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"assistant","timestamp":"2026-05-01T10:00:00.000Z","message":{"id":"a","usage":{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":5,"cache_read_input_tokens":1}}}
{"type":"assistant","timestamp":"2026-05-02T10:00:00.000Z","message":{"id":"b","usage":{"input_tokens":1,"output_tokens":2,"cache_creation_input_tokens":3,"cache_read_input_tokens":4}}}
{"type":"assistant","timestamp":"2026-06-01T10:00:00.000Z","message":{"id":"c","usage":{"input_tokens":100,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
`
	p := writeFixture(t, dir, "s.jsonl", content)
	months, meta, err := ParseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Size == 0 {
		t.Errorf("meta.Size = 0, want non-zero")
	}
	may := months["2026-05"]
	if may == nil || may.Input != 11 || may.Output != 22 || may.CacheWrite != 8 || may.CacheRead != 5 {
		t.Errorf("May = %+v, want input 11 output 22 cacheW 8 cacheR 5", may)
	}
	if jun := months["2026-06"]; jun == nil || jun.Input != 100 {
		t.Errorf("Jun = %+v, want input 100", jun)
	}
}

func TestParseFile_dedupsByMessageID(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"assistant","timestamp":"2026-05-01T10:00:00.000Z","message":{"id":"dup","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
{"type":"assistant","timestamp":"2026-05-01T10:00:01.000Z","message":{"id":"dup","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
`
	p := writeFixture(t, dir, "s.jsonl", content)
	months, _, err := ParseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := months["2026-05"].Input; got != 10 {
		t.Errorf("Input = %d, want 10 (dedup by id)", got)
	}
}

func TestParseFile_skipsNonAssistantNoUsageAndMalformed(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"user","timestamp":"2026-05-01T10:00:00.000Z","message":{"id":"u"}}
{"type":"assistant","timestamp":"2026-05-01T10:00:00.000Z","message":{"id":"nousage"}}
this is not json
{"type":"assistant","timestamp":"2026-05-01T10:00:00.000Z","message":{"id":"ok","usage":{"input_tokens":7,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
`
	p := writeFixture(t, dir, "s.jsonl", content)
	months, _, err := ParseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(months) != 1 || months["2026-05"].Input != 7 {
		t.Errorf("months = %+v, want only the one valid assistant record (input 7)", months)
	}
}

func TestParseFile_emptyFile(t *testing.T) {
	dir := t.TempDir()
	p := writeFixture(t, dir, "empty.jsonl", "")
	months, _, err := ParseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(months) != 0 {
		t.Errorf("months = %+v, want empty", months)
	}
}

func TestParseFile_groupsByModel(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"a","model":"claude-opus-4-7","usage":{"input_tokens":10,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
{"type":"assistant","timestamp":"2026-05-02T10:00:00Z","message":{"id":"b","model":"claude-sonnet-4-6","usage":{"input_tokens":5,"output_tokens":2,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
{"type":"assistant","timestamp":"2026-05-03T10:00:00Z","message":{"id":"c","model":"claude-opus-4-7","usage":{"input_tokens":1,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
`
	p := writeFixture(t, dir, "s.jsonl", content)
	months, _, err := ParseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	may := months["2026-05"]
	if may == nil {
		t.Fatal("no May")
	}
	if len(may.Models) != 2 {
		t.Fatalf("models = %d, want 2", len(may.Models))
	}
	if may.Models[0].Model != "claude-opus-4-7" || may.Models[0].Input != 11 {
		t.Errorf("models[0] = %+v, want opus input 11", may.Models[0])
	}
	if may.Models[1].Model != "claude-sonnet-4-6" {
		t.Errorf("models[1] = %+v, want sonnet", may.Models[1])
	}
	if may.Input != 16 {
		t.Errorf("month input = %d, want 16 (sum)", may.Input)
	}
}

func TestParseFile_missingModelIsUnknown(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"a","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}` + "\n"
	p := writeFixture(t, dir, "s.jsonl", content)
	months, _, _ := ParseFile(p)
	if months["2026-05"].Models[0].Model != "unknown" {
		t.Errorf("missing model = %q, want unknown", months["2026-05"].Models[0].Model)
	}
}

func TestParseFile_dropsZeroTokenModels(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"a","model":"<synthetic>","usage":{"input_tokens":0,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
{"type":"assistant","timestamp":"2026-05-01T10:00:01Z","message":{"id":"b","model":"claude-opus-4-7","usage":{"input_tokens":5,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
`
	p := writeFixture(t, dir, "s.jsonl", content)
	months, _, _ := ParseFile(p)
	may := months["2026-05"]
	if may == nil || len(may.Models) != 1 || may.Models[0].Model != "claude-opus-4-7" {
		t.Errorf("zero-token model not dropped: %+v", may)
	}
}

func TestParseFile_dropsAllZeroMonth(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"a","model":"<synthetic>","usage":{"input_tokens":0,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}` + "\n"
	p := writeFixture(t, dir, "s.jsonl", content)
	months, _, _ := ParseFile(p)
	if _, ok := months["2026-05"]; ok {
		t.Errorf("month with only zero-token usage should be dropped: %+v", months)
	}
}
