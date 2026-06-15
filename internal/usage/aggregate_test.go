package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAggregate_emptyDir(t *testing.T) {
	out, err := Aggregate(t.TempDir(), filepath.Join(t.TempDir(), "cache.json"))
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
	out, err := Aggregate(dir, filepath.Join(t.TempDir(), "cache.json"))
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
	if _, err := Aggregate(dir, cachePath); err != nil {
		t.Fatal(err)
	}
	// Tamper the cached value WITHOUT touching the file on disk. If Aggregate
	// reuses the cache (file unchanged), the tampered value must survive.
	c := LoadCache(cachePath)
	info, _ := os.Stat(p)
	c.Files[p] = fileCacheEntry{
		Meta:   FileMeta{ModTime: info.ModTime(), Size: info.Size()},
		Months: map[string]*MonthlyUsage{"2026-05": {Month: "2026-05", Input: 999}},
	}
	if err := c.Save(cachePath); err != nil {
		t.Fatal(err)
	}
	out, err := Aggregate(dir, cachePath)
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
	if _, err := Aggregate(dir, cachePath); err != nil {
		t.Fatal(err)
	}
	// Append a new record and bump mtime so size/mtime differ from cache.
	more := `{"type":"assistant","timestamp":"2026-05-02T10:00:00Z","message":{"id":"b","usage":{"input_tokens":5,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}` + "\n"
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(more)
	f.Close()
	future := time.Now().Add(2 * time.Second)
	os.Chtimes(p, future, future)

	out, err := Aggregate(dir, cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Input != 15 {
		t.Errorf("Input = %+v, want 15 (file re-parsed after change)", out)
	}
}

func TestAggregate_dropsDeletedFileFromCache(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	p := writeFixture(t, dir, "a.jsonl",
		`{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"a","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")
	if _, err := Aggregate(dir, cachePath); err != nil {
		t.Fatal(err)
	}
	os.Remove(p)
	out, err := Aggregate(dir, cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("out = %+v, want empty after file deleted", out)
	}
	if _, ok := LoadCache(cachePath).Files[p]; ok {
		t.Errorf("deleted file still present in cache")
	}
}
