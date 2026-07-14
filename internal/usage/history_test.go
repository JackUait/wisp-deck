package usage

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testHistoryPaths(t *testing.T) historyPaths {
	t.Helper()
	dir := t.TempDir()
	return historyPaths{
		Primary: filepath.Join(dir, "usage-history.jsonl"),
		Backup:  filepath.Join(dir, "usage-history.backup.jsonl"),
		Lock:    filepath.Join(dir, ".usage-history.lock"),
	}
}

func testHistorySource(input int64) historySource {
	return historySource{
		ParserVersion: cacheVersion,
		Meta:          FileMeta{ModTime: time.Unix(input, 0).UTC(), Size: input},
		Months: map[string]*MonthlyUsage{
			"2026-07": {
				Month: "2026-07",
				Input: input,
				Models: []ModelUsage{{
					Model: "claude-opus-4-7",
					Input: input,
				}},
			},
		},
	}
}

func appendTestHistoryRecord(t *testing.T, path string, rec historyRecord) historyRecord {
	t.Helper()
	sealed, line, err := encodeHistoryRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(line); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return sealed
}

func TestHistoryReplay_latestSourceSnapshotWins(t *testing.T) {
	paths := testHistoryPaths(t)
	appendTestHistoryRecord(t, paths.Primary, historyRecord{
		Sequence: 1,
		Sources:  map[string]historySource{"/a": testHistorySource(10)},
	})
	appendTestHistoryRecord(t, paths.Primary, historyRecord{
		Sequence: 2,
		Sources:  map[string]historySource{"/a": testHistorySource(25)},
	})

	state, _, err := readHistoryCopies(paths)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Sources["/a"].Months["2026-07"].Input; got != 25 {
		t.Fatalf("input = %d, want 25", got)
	}
	if state.LastSequence != 2 {
		t.Fatalf("last sequence = %d, want 2", state.LastSequence)
	}
}

func TestHistoryReplay_sourceUpdateDoesNotAffectOtherSources(t *testing.T) {
	paths := testHistoryPaths(t)
	appendTestHistoryRecord(t, paths.Primary, historyRecord{
		Sequence: 1,
		Sources: map[string]historySource{
			"/a": testHistorySource(10),
			"/b": testHistorySource(20),
		},
	})
	appendTestHistoryRecord(t, paths.Primary, historyRecord{
		Sequence: 2,
		Sources:  map[string]historySource{"/a": testHistorySource(30)},
	})

	state, _, err := readHistoryCopies(paths)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Sources["/a"].Months["2026-07"].Input; got != 30 {
		t.Fatalf("/a input = %d, want 30", got)
	}
	if got := state.Sources["/b"].Months["2026-07"].Input; got != 20 {
		t.Fatalf("/b input = %d, want 20", got)
	}
}

func TestHistoryReplay_validBackupRecoversBadPrimaryChecksum(t *testing.T) {
	paths := testHistoryPaths(t)
	rec := appendTestHistoryRecord(t, paths.Backup, historyRecord{
		Sequence: 1,
		Sources:  map[string]historySource{"/a": testHistorySource(10)},
	})
	rec.Checksum = strings.Repeat("0", 64)
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Primary, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	state, copies, err := readHistoryCopies(paths)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Sources["/a"].Months["2026-07"].Input; got != 10 {
		t.Fatalf("input = %d, want 10 from backup", got)
	}
	if !copies.PrimaryDamaged {
		t.Fatal("primary should be marked damaged")
	}
}

func TestHistoryReplay_loneBadChecksumReturnsError(t *testing.T) {
	paths := testHistoryPaths(t)
	rec := historyRecord{
		Schema:   historySchemaVersion,
		Sequence: 1,
		Sources:  map[string]historySource{"/a": testHistorySource(10)},
		Checksum: strings.Repeat("0", 64),
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Primary, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := readHistoryCopies(paths); err == nil {
		t.Fatal("bad checksum without a valid counterpart should fail")
	}
}

func TestHistoryReplay_conflictingValidSequenceReturnsError(t *testing.T) {
	paths := testHistoryPaths(t)
	appendTestHistoryRecord(t, paths.Primary, historyRecord{
		Sequence: 1,
		Sources:  map[string]historySource{"/a": testHistorySource(10)},
	})
	appendTestHistoryRecord(t, paths.Backup, historyRecord{
		Sequence: 1,
		Sources:  map[string]historySource{"/a": testHistorySource(20)},
	})

	if _, _, err := readHistoryCopies(paths); err == nil {
		t.Fatal("conflicting valid records at one sequence should fail")
	}
}

func TestHistoryReplay_legacyArchivePreservesCacheWrite1h(t *testing.T) {
	paths := testHistoryPaths(t)
	appendTestHistoryRecord(t, paths.Primary, historyRecord{
		Sequence: 1,
		Archive: map[string]map[string]*ModelUsage{
			"2026-06": {
				"claude-opus-4-7": {
					Model:        "claude-opus-4-7",
					CacheWrite:   10,
					CacheWrite1h: 6,
				},
			},
		},
		Sealed:        []string{"/gone"},
		ImportsLegacy: true,
	})

	state, _, err := readHistoryCopies(paths)
	if err != nil {
		t.Fatal(err)
	}
	model := state.Archive["2026-06"]["claude-opus-4-7"]
	if model.CacheWrite != 10 || model.CacheWrite1h != 6 {
		t.Fatalf("archive model = %+v, want total 10 and 1h 6", model)
	}
	if !state.Sealed["/gone"] || !state.ImportedLegacy {
		t.Fatalf("legacy state not replayed: sealed=%v imported=%v", state.Sealed, state.ImportedLegacy)
	}
}

func TestHistoryReplay_unknownSchemaReturnsError(t *testing.T) {
	paths := testHistoryPaths(t)
	rec, line, err := encodeHistoryRecord(historyRecord{
		Sequence: 1,
		Sources:  map[string]historySource{"/a": testHistorySource(10)},
	})
	if err != nil {
		t.Fatal(err)
	}
	line = []byte(strings.Replace(string(line), `"schema":1`, `"schema":99`, 1))
	if rec.Schema != historySchemaVersion {
		t.Fatalf("test fixture schema = %d", rec.Schema)
	}
	if err := os.WriteFile(paths.Primary, line, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := readHistoryCopies(paths); err == nil {
		t.Fatal("unknown history schema should fail")
	}
}

func TestHistoryCommit_writesIdenticalPrivateMirrors(t *testing.T) {
	paths := testHistoryPaths(t)
	state, err := commitHistory(paths, historyUpdate{
		Sources: map[string]historySource{"/a": testHistorySource(10)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.LastSequence != 1 {
		t.Fatalf("last sequence = %d, want 1", state.LastSequence)
	}
	primary, err := os.ReadFile(paths.Primary)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(paths.Backup)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(primary, backup) {
		t.Fatalf("journal mirrors differ:\nprimary: %s\nbackup: %s", primary, backup)
	}
	for _, path := range []string{paths.Primary, paths.Backup, paths.Lock} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %04o, want 0600", filepath.Base(path), got)
		}
	}
}

func TestHistoryRepair_restoresDeletedPrimaryFromBackup(t *testing.T) {
	paths := testHistoryPaths(t)
	if _, err := commitHistory(paths, historyUpdate{
		Sources: map[string]historySource{"/a": testHistorySource(10)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths.Primary); err != nil {
		t.Fatal(err)
	}

	state, err := commitHistory(paths, historyUpdate{})
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Sources["/a"].Months["2026-07"].Input; got != 10 {
		t.Fatalf("input = %d, want 10", got)
	}
	primary, _ := os.ReadFile(paths.Primary)
	backup, _ := os.ReadFile(paths.Backup)
	if !bytes.Equal(primary, backup) {
		t.Fatal("primary was not repaired from backup")
	}
}

func TestHistoryRepair_restoresDeletedBackupFromPrimary(t *testing.T) {
	paths := testHistoryPaths(t)
	if _, err := commitHistory(paths, historyUpdate{
		Sources: map[string]historySource{"/a": testHistorySource(10)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths.Backup); err != nil {
		t.Fatal(err)
	}

	if _, err := commitHistory(paths, historyUpdate{}); err != nil {
		t.Fatal(err)
	}
	primary, _ := os.ReadFile(paths.Primary)
	backup, _ := os.ReadFile(paths.Backup)
	if !bytes.Equal(primary, backup) {
		t.Fatal("backup was not repaired from primary")
	}
}

func TestHistoryRepair_truncatedTailDoesNotBlockLaterCommit(t *testing.T) {
	paths := testHistoryPaths(t)
	if _, err := commitHistory(paths, historyUpdate{
		Sources: map[string]historySource{"/a": testHistorySource(10)},
	}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(paths.Primary, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"schema":1`); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	state, err := commitHistory(paths, historyUpdate{
		Sources: map[string]historySource{"/b": testHistorySource(20)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Sources) != 2 {
		t.Fatalf("sources = %+v, want /a and /b", state.Sources)
	}
	state, _, err = readHistoryCopies(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Sources) != 2 {
		t.Fatalf("replayed sources = %+v, want /a and /b", state.Sources)
	}
}

func TestHistoryCommit_staleSnapshotCannotReplaceNewer(t *testing.T) {
	paths := testHistoryPaths(t)
	if _, err := commitHistory(paths, historyUpdate{
		Sources: map[string]historySource{"/a": testHistorySource(20)},
	}); err != nil {
		t.Fatal(err)
	}
	state, err := commitHistory(paths, historyUpdate{
		Sources: map[string]historySource{"/a": testHistorySource(10)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Sources["/a"].Months["2026-07"].Input; got != 20 {
		t.Fatalf("stale commit replaced newer source: input = %d, want 20", got)
	}
	if state.LastSequence != 1 {
		t.Fatalf("stale no-op allocated sequence %d, want 1", state.LastSequence)
	}
}

func TestHistoryConcurrent_disjointCommitsPreserveUnion(t *testing.T) {
	paths := testHistoryPaths(t)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for path, input := range map[string]int64{"/a": 10, "/b": 20} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := commitHistory(paths, historyUpdate{
				Sources: map[string]historySource{path: testHistorySource(input)},
			})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	state, _, err := readHistoryCopies(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Sources) != 2 {
		t.Fatalf("sources = %+v, want both concurrent updates", state.Sources)
	}
}

func TestHistoryCommit_oneSidedFailureIsReturnedAndRepaired(t *testing.T) {
	paths := testHistoryPaths(t)
	validBackup := paths.Backup
	// The backup's parent is the primary path. It does not exist during replay,
	// then becomes a regular file after the primary append, making the backup
	// append fail with ENOTDIR after one durable copy has been written.
	paths.Backup = filepath.Join(paths.Primary, "backup.jsonl")
	if _, err := commitHistory(paths, historyUpdate{
		Sources: map[string]historySource{"/a": testHistorySource(10)},
	}); err == nil {
		t.Fatal("backup append failure should be returned")
	}
	if _, err := os.Stat(paths.Primary); err != nil {
		t.Fatalf("primary should retain the one-sided durable record: %v", err)
	}
	paths.Backup = validBackup

	state, err := commitHistory(paths, historyUpdate{})
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Sources["/a"].Months["2026-07"].Input; got != 10 {
		t.Fatalf("repaired input = %d, want 10", got)
	}
	if _, err := os.Stat(paths.Backup); err != nil {
		t.Fatalf("backup was not repaired: %v", err)
	}
}
