package usage

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const historySchemaVersion = 1

type historyPaths struct {
	Primary string
	Backup  string
	Lock    string
}

type historySource struct {
	ParserVersion int                      `json:"parser_version"`
	Meta          FileMeta                 `json:"meta"`
	Months        map[string]*MonthlyUsage `json:"months"`
}

type historyRecord struct {
	Schema        int                               `json:"schema"`
	Sequence      uint64                            `json:"sequence"`
	Sources       map[string]historySource          `json:"sources,omitempty"`
	Archive       map[string]map[string]*ModelUsage `json:"archive,omitempty"`
	Sealed        []string                          `json:"sealed,omitempty"`
	ImportsLegacy bool                              `json:"imports_legacy,omitempty"`
	Checksum      string                            `json:"checksum"`
}

type historyState struct {
	Sources        map[string]historySource
	Archive        map[string]map[string]*ModelUsage
	Sealed         map[string]bool
	ImportedLegacy bool
	LastSequence   uint64
	Records        map[uint64]historyRecord
}

type historyFileRecords struct {
	Records map[uint64]historyRecord
	Exists  bool
	Damaged bool
}

type historyCopies struct {
	PrimaryRecords map[uint64]historyRecord
	BackupRecords  map[uint64]historyRecord
	PrimaryExists  bool
	BackupExists   bool
	PrimaryDamaged bool
	BackupDamaged  bool
}

func emptyHistoryState() historyState {
	return historyState{
		Sources: map[string]historySource{},
		Archive: map[string]map[string]*ModelUsage{},
		Sealed:  map[string]bool{},
		Records: map[uint64]historyRecord{},
	}
}

func historyPathsForCache(cachePath string) historyPaths {
	dir := filepath.Dir(cachePath)
	if filepath.Base(cachePath) == "usage-cache.json" {
		return historyPaths{
			Primary: filepath.Join(dir, "usage-history.jsonl"),
			Backup:  filepath.Join(dir, "usage-history.backup.jsonl"),
			Lock:    filepath.Join(dir, ".usage-history.lock"),
		}
	}
	return historyPaths{
		Primary: cachePath + ".history.jsonl",
		Backup:  cachePath + ".history.backup.jsonl",
		Lock:    cachePath + ".history.lock",
	}
}

func historyRecordChecksum(rec historyRecord) (string, error) {
	rec.Checksum = ""
	data, err := json.Marshal(rec)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func encodeHistoryRecord(rec historyRecord) (historyRecord, []byte, error) {
	if rec.Schema == 0 {
		rec.Schema = historySchemaVersion
	}
	checksum, err := historyRecordChecksum(rec)
	if err != nil {
		return historyRecord{}, nil, err
	}
	rec.Checksum = checksum
	line, err := json.Marshal(rec)
	if err != nil {
		return historyRecord{}, nil, err
	}
	return rec, append(line, '\n'), nil
}

func validHistoryRecord(rec historyRecord) (bool, error) {
	if rec.Schema != historySchemaVersion {
		return false, fmt.Errorf("unsupported usage history schema %d", rec.Schema)
	}
	want, err := historyRecordChecksum(rec)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(rec.Checksum, want), nil
}

func readHistoryFile(path string) (historyFileRecords, error) {
	out := historyFileRecords{Records: map[uint64]historyRecord{}}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	defer f.Close()
	out.Exists = true

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for scanner.Scan() {
		if len(strings.TrimSpace(scanner.Text())) == 0 {
			continue
		}
		var rec historyRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			out.Damaged = true
			continue
		}
		valid, err := validHistoryRecord(rec)
		if err != nil {
			return out, err
		}
		if !valid {
			out.Damaged = true
			continue
		}
		if prev, ok := out.Records[rec.Sequence]; ok && prev.Checksum != rec.Checksum {
			return out, fmt.Errorf("conflicting usage history records at sequence %d", rec.Sequence)
		}
		out.Records[rec.Sequence] = rec
	}
	if err := scanner.Err(); err != nil {
		return out, err
	}
	return out, nil
}

func readHistoryCopies(paths historyPaths) (historyState, historyCopies, error) {
	primary, err := readHistoryFile(paths.Primary)
	if err != nil {
		return historyState{}, historyCopies{}, err
	}
	backup, err := readHistoryFile(paths.Backup)
	if err != nil {
		return historyState{}, historyCopies{}, err
	}
	copies := historyCopies{
		PrimaryRecords: primary.Records,
		BackupRecords:  backup.Records,
		PrimaryExists:  primary.Exists,
		BackupExists:   backup.Exists,
		PrimaryDamaged: primary.Damaged,
		BackupDamaged:  backup.Damaged,
	}
	if primary.Damaged && !backup.Exists {
		return historyState{}, copies, fmt.Errorf("primary usage history is damaged and no backup exists")
	}
	if backup.Damaged && !primary.Exists {
		return historyState{}, copies, fmt.Errorf("backup usage history is damaged and no primary exists")
	}
	if primary.Damaged && backup.Damaged {
		return historyState{}, copies, fmt.Errorf("both usage history copies are damaged")
	}

	union := make(map[uint64]historyRecord, len(primary.Records)+len(backup.Records))
	for seq, rec := range primary.Records {
		union[seq] = rec
	}
	for seq, rec := range backup.Records {
		if prev, ok := union[seq]; ok && prev.Checksum != rec.Checksum {
			return historyState{}, copies, fmt.Errorf("conflicting usage history records at sequence %d", seq)
		}
		union[seq] = rec
	}

	sequences := make([]uint64, 0, len(union))
	for seq := range union {
		sequences = append(sequences, seq)
	}
	sort.Slice(sequences, func(i, j int) bool { return sequences[i] < sequences[j] })

	state := emptyHistoryState()
	for _, seq := range sequences {
		rec := union[seq]
		for path, source := range rec.Sources {
			state.Sources[path] = source
		}
		foldArchive(state.Archive, rec.Archive)
		for _, path := range rec.Sealed {
			state.Sealed[path] = true
		}
		state.ImportedLegacy = state.ImportedLegacy || rec.ImportsLegacy
		state.LastSequence = seq
		state.Records[seq] = rec
	}
	return state, copies, nil
}
