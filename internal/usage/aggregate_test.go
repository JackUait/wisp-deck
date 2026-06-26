package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAggregate_emptyDir(t *testing.T) {
	out, err := Aggregate(t.TempDir(), "", filepath.Join(t.TempDir(), "cache.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("out = %+v, want empty", out)
	}
}

func TestAggregate_mergesFilesAndSortsDescending(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "proj")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, sub, "a.jsonl",
		`{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"a","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")
	writeFixture(t, sub, "b.jsonl",
		`{"type":"assistant","timestamp":"2026-05-02T10:00:00Z","message":{"id":"b","usage":{"input_tokens":5,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
{"type":"assistant","timestamp":"2026-06-01T10:00:00Z","message":{"id":"c","usage":{"input_tokens":1,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")
	out, err := Aggregate(dir, "", filepath.Join(t.TempDir(), "cache.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	if out[0].Month != "2026-06" || out[1].Month != "2026-05" {
		t.Errorf("order = [%s,%s], want descending [2026-06,2026-05]", out[0].Month, out[1].Month)
	}
	if out[1].Input != 15 {
		t.Errorf("May Input = %d, want 15 (merged across files)", out[1].Input)
	}
}

func TestAggregate_reusesCacheForUnchangedFiles(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	p := writeFixture(t, dir, "a.jsonl",
		`{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"a","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")

	// First pass builds the cache.
	if _, err := Aggregate(dir, "", cachePath); err != nil {
		t.Fatal(err)
	}
	// Tamper the cached value WITHOUT touching the file on disk. If Aggregate
	// reuses the cache (file unchanged), the tampered value must survive.
	c := LoadCache(cachePath)
	info, _ := os.Stat(p)
	// Output is rebuilt from per-model rows, so tamper a model row (not the flat
	// field) — it must survive if the unchanged file is served from cache.
	c.Files[p] = fileCacheEntry{
		Meta: FileMeta{ModTime: info.ModTime(), Size: info.Size()},
		Months: map[string]*MonthlyUsage{"2026-05": {
			Month: "2026-05", Input: 999,
			Models: []ModelUsage{{Model: "unknown", Input: 999}},
		}},
	}
	if err := c.Save(cachePath); err != nil {
		t.Fatal(err)
	}
	out, err := Aggregate(dir, "", cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Input != 999 {
		t.Errorf("Input = %+v, want 999 (cache reused for unchanged file)", out)
	}
}

func TestAggregate_reparsesChangedFile(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	p := writeFixture(t, dir, "a.jsonl",
		`{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"a","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")
	if _, err := Aggregate(dir, "", cachePath); err != nil {
		t.Fatal(err)
	}
	// Append a new record and bump mtime so size/mtime differ from cache.
	more := `{"type":"assistant","timestamp":"2026-05-02T10:00:00Z","message":{"id":"b","usage":{"input_tokens":5,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}` + "\n"
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(more)
	f.Close()
	future := time.Now().Add(2 * time.Second)
	os.Chtimes(p, future, future)

	out, err := Aggregate(dir, "", cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Input != 15 {
		t.Errorf("Input = %+v, want 15 (file re-parsed after change)", out)
	}
}

func TestAggregate_sealsDeletedFileIntoArchive(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	p := writeFixture(t, dir, "a.jsonl",
		`{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"a","model":"claude-opus-4-7","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")
	if _, err := Aggregate(dir, "", cachePath); err != nil {
		t.Fatal(err)
	}
	os.Remove(p)
	out, err := Aggregate(dir, "", cachePath)
	if err != nil {
		t.Fatal(err)
	}
	// The month survives even though its transcript is gone.
	if len(out) != 1 || out[0].Month != "2026-05" || out[0].Input != 10 {
		t.Fatalf("out = %+v, want one 2026-05 row with input 10", out)
	}
	c := LoadCache(cachePath)
	if _, ok := c.Files[p]; ok {
		t.Errorf("deleted file should not remain in Files")
	}
	if !c.Sealed[p] {
		t.Errorf("deleted file should be recorded in Sealed")
	}
	if c.Archive["2026-05"]["claude-opus-4-7"].Input != 10 {
		t.Errorf("archive = %+v, want 2026-05/opus input 10", c.Archive)
	}
}

func TestAggregate_sealedFileNotDoubleCountedOnReappear(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	record := `{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"a","model":"claude-opus-4-7","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}` + "\n"
	p := writeFixture(t, dir, "a.jsonl", record)
	if _, err := Aggregate(dir, "", cachePath); err != nil {
		t.Fatal(err)
	}
	os.Remove(p)
	if _, err := Aggregate(dir, "", cachePath); err != nil { // seals into archive
		t.Fatal(err)
	}
	writeFixture(t, dir, "a.jsonl", record) // same path reappears
	out, err := Aggregate(dir, "", cachePath)
	if err != nil {
		t.Fatal(err)
	}
	// Counted once (from the archive), NOT 20.
	if len(out) != 1 || out[0].Input != 10 {
		t.Errorf("out = %+v, want input 10 (sealed path skipped, not re-added)", out)
	}
}

func TestAggregate_mergesArchiveWithLiveMonth(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	// Two May files; delete one so it seals, keep one live. Both feed 2026-05.
	gone := writeFixture(t, dir, "gone.jsonl",
		`{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"g","model":"claude-opus-4-7","usage":{"input_tokens":4,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")
	writeFixture(t, dir, "live.jsonl",
		`{"type":"assistant","timestamp":"2026-05-02T10:00:00Z","message":{"id":"l","model":"claude-opus-4-7","usage":{"input_tokens":6,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")
	if _, err := Aggregate(dir, "", cachePath); err != nil {
		t.Fatal(err)
	}
	os.Remove(gone)
	out, err := Aggregate(dir, "", cachePath)
	if err != nil {
		t.Fatal(err)
	}
	// Archived 4 (gone) + live 6 = 10 for 2026-05.
	if len(out) != 1 || out[0].Input != 10 {
		t.Errorf("out = %+v, want 2026-05 input 10 (archive 4 + live 6)", out)
	}
}

func TestAddModelRows_accumulatesByModel(t *testing.T) {
	dst := map[string]*ModelUsage{}
	addModelRows(dst, []ModelUsage{
		{Model: "claude-opus-4-7", Input: 10, Output: 1},
		{Model: "claude-opus-4-7", Input: 5, CacheRead: 2},
		{Model: "claude-fable-5", Input: 3},
	})
	if dst["claude-opus-4-7"].Input != 15 || dst["claude-opus-4-7"].Output != 1 ||
		dst["claude-opus-4-7"].CacheRead != 2 {
		t.Errorf("opus row = %+v, want input 15 output 1 cacheRead 2", dst["claude-opus-4-7"])
	}
	if dst["claude-fable-5"].Input != 3 {
		t.Errorf("fable row = %+v, want input 3", dst["claude-fable-5"])
	}
}

func TestFoldMonths_accumulatesByMonthAndModel(t *testing.T) {
	acc := map[string]map[string]*ModelUsage{}
	foldMonths(acc, map[string]*MonthlyUsage{
		"2026-05": {Month: "2026-05", Models: []ModelUsage{{Model: "m", Input: 4}}},
	})
	foldMonths(acc, map[string]*MonthlyUsage{
		"2026-05": {Month: "2026-05", Models: []ModelUsage{{Model: "m", Input: 6}}},
	})
	if acc["2026-05"]["m"].Input != 10 {
		t.Errorf("acc = %+v, want 2026-05/m input 10", acc)
	}
}

func TestAggregate_mergesModelsAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	writeFixture(t, dir, "a.jsonl", `{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"a","model":"claude-opus-4-7","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")
	writeFixture(t, dir, "b.jsonl", `{"type":"assistant","timestamp":"2026-05-02T10:00:00Z","message":{"id":"b","model":"claude-opus-4-7","usage":{"input_tokens":5,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")
	out, err := Aggregate(dir, "", cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || len(out[0].Models) != 1 || out[0].Models[0].Input != 15 {
		t.Errorf("merged models = %+v, want one opus row input 15", out)
	}
}

func TestAggregate_mergesOpenCodeWithClaude(t *testing.T) {
	claudeDir := t.TempDir()
	ocDir := t.TempDir()
	cachePath := filepath.Join(t.TempDir(), "cache.json")

	// Claude transcript: opus-4-8, input 10, May 2026.
	writeFixture(t, claudeDir, "a.jsonl",
		`{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"a","model":"claude-opus-4-8","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")

	// OpenCode messages live under <ocDir>/<sessionID>/msg_*.json. Same model
	// opus-4-8 in May (must MERGE into one row with Claude), plus a gpt-5 message
	// in June (an OpenCode-only month).
	sess := filepath.Join(ocDir, "ses_1")
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	may := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC).UnixMilli()
	jun := time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC).UnixMilli()
	writeFixture(t, sess, "msg_a.json", ocMsg("assistant", "claude-opus-4-8", may, 5, 0, 0, 0, 0))
	writeFixture(t, sess, "msg_b.json", ocMsg("assistant", "gpt-5", jun, 7, 0, 0, 0, 0))

	out, err := Aggregate(claudeDir, ocDir, cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2 (May + June)", len(out))
	}
	if out[0].Month != "2026-06" || out[1].Month != "2026-05" {
		t.Fatalf("order = [%s,%s], want [2026-06,2026-05]", out[0].Month, out[1].Month)
	}
	may2 := out[1]
	if len(may2.Models) != 1 || may2.Models[0].Model != "claude-opus-4-8" || may2.Models[0].Input != 15 {
		t.Errorf("May models = %+v, want one claude-opus-4-8 row input 15 (claude 10 + opencode 5)", may2.Models)
	}
	if may2.Input != 15 {
		t.Errorf("May input = %d, want 15", may2.Input)
	}
	jun2 := out[0]
	if len(jun2.Models) != 1 || jun2.Models[0].Model != "gpt-5" || jun2.Models[0].Input != 7 {
		t.Errorf("June models = %+v, want gpt-5 input 7 (opencode-only)", jun2.Models)
	}
}

func TestAggregateAll_mergesMultipleClaudeDirs(t *testing.T) {
	// Two independent Claude transcript roots (e.g. the Default ~/.claude and an
	// extra native account) must both be counted into the same month.
	dirA := t.TempDir()
	dirB := t.TempDir()
	writeFixture(t, dirA, "a.jsonl",
		`{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"a","model":"claude-opus-4-8","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")
	writeFixture(t, dirB, "b.jsonl",
		`{"type":"assistant","timestamp":"2026-05-02T10:00:00Z","message":{"id":"b","model":"claude-opus-4-8","usage":{"input_tokens":5,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")

	out, err := AggregateAll([]string{dirA, dirB}, "", filepath.Join(t.TempDir(), "cache.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Month != "2026-05" {
		t.Fatalf("out = %+v, want one 2026-05 row", out)
	}
	if out[0].Input != 15 {
		t.Errorf("May Input = %d, want 15 (10 from dirA + 5 from dirB)", out[0].Input)
	}
}

func TestAggregateAll_sealingIgnoresOtherRootsFiles(t *testing.T) {
	// With a shared cache across roots, files in one root must NOT be sealed away
	// just because the walk of another root didn't see them.
	dirA := t.TempDir()
	dirB := t.TempDir()
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	writeFixture(t, dirA, "a.jsonl",
		`{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"a","model":"claude-opus-4-8","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")
	writeFixture(t, dirB, "b.jsonl",
		`{"type":"assistant","timestamp":"2026-05-02T10:00:00Z","message":{"id":"b","model":"claude-opus-4-8","usage":{"input_tokens":5,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")

	if _, err := AggregateAll([]string{dirA, dirB}, "", cachePath); err != nil {
		t.Fatal(err)
	}
	// Second pass over both roots; nothing deleted, so totals must be unchanged
	// and no path may be sealed.
	out, err := AggregateAll([]string{dirA, dirB}, "", cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Input != 15 {
		t.Fatalf("out = %+v, want 2026-05 input 15 on reaggregate", out)
	}
	c := LoadCache(cachePath)
	if len(c.Sealed) != 0 {
		t.Errorf("Sealed = %+v, want none (no files vanished)", c.Sealed)
	}
}

func TestClaudeAccountProjectDirs_includesDefaultAndAccounts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	mkdirAll(t, filepath.Join(home, ".claude", "projects"))
	accounts := filepath.Join(home, ".config", "wisp-deck", "claude-accounts")
	mkdirAll(t, filepath.Join(accounts, "personal", "projects"))
	mkdirAll(t, filepath.Join(accounts, "work", "projects"))
	// An account dir without a projects/ subdir must be skipped.
	mkdirAll(t, filepath.Join(accounts, "empty"))

	got := ClaudeAccountProjectDirs(home)
	want := map[string]bool{
		filepath.Join(home, ".claude", "projects"):      true,
		filepath.Join(accounts, "personal", "projects"): true,
		filepath.Join(accounts, "work", "projects"):     true,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d dirs %+v, want %d", len(got), got, len(want))
	}
	for _, d := range got {
		if !want[d] {
			t.Errorf("unexpected dir %q", d)
		}
	}
	if got[0] != filepath.Join(home, ".claude", "projects") {
		t.Errorf("first dir = %q, want the Default ~/.claude/projects", got[0])
	}
}

func TestClaudeAccountProjectDirs_defaultOnlyWhenNoAccounts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	got := ClaudeAccountProjectDirs(home)
	if len(got) != 1 || got[0] != filepath.Join(home, ".claude", "projects") {
		t.Errorf("got %+v, want only the Default ~/.claude/projects", got)
	}
}

func TestClaudeAccountProjectDirs_honorsXDGConfigHome(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	accounts := filepath.Join(xdg, "wisp-deck", "claude-accounts")
	mkdirAll(t, filepath.Join(accounts, "personal", "projects"))

	got := ClaudeAccountProjectDirs(home)
	want := filepath.Join(accounts, "personal", "projects")
	found := false
	for _, d := range got {
		if d == want {
			found = true
		}
	}
	if !found {
		t.Errorf("got %+v, want it to include %q from XDG_CONFIG_HOME", got, want)
	}
}

func TestDefaultPaths_opencodeMessageDir(t *testing.T) {
	t.Setenv("OPENCODE_DATA_DIR", "")
	_, ocDir, _ := DefaultPaths("/home/u")
	want := filepath.Join("/home/u", ".local", "share", "opencode", "storage", "message")
	if ocDir != want {
		t.Errorf("opencodeDir = %q, want %q", ocDir, want)
	}
}

func TestDefaultPaths_opencodeDataDirEnvOverride(t *testing.T) {
	t.Setenv("OPENCODE_DATA_DIR", "/custom/oc")
	_, ocDir, _ := DefaultPaths("/home/u")
	want := filepath.Join("/custom/oc", "storage", "message")
	if ocDir != want {
		t.Errorf("opencodeDir = %q, want %q", ocDir, want)
	}
}

func TestDefaultPaths_opencodeDataDirCommaList(t *testing.T) {
	// OPENCODE_DATA_DIR may be a comma-separated list; we use the first entry.
	t.Setenv("OPENCODE_DATA_DIR", "/first/oc,/second/oc")
	_, ocDir, _ := DefaultPaths("/home/u")
	want := filepath.Join("/first/oc", "storage", "message")
	if ocDir != want {
		t.Errorf("opencodeDir = %q, want %q (first of comma list)", ocDir, want)
	}
}
