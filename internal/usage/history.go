package usage

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
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

type historyUpdate struct {
	Sources       map[string]historySource
	LegacyArchive map[string]map[string]*ModelUsage
	LegacySealed  map[string]bool
	ImportLegacy  bool
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
		applyHistoryRecord(&state, union[seq])
	}
	return state, copies, nil
}

func applyHistoryRecord(state *historyState, rec historyRecord) {
	for path, source := range rec.Sources {
		state.Sources[path] = source
	}
	foldArchive(state.Archive, rec.Archive)
	for _, path := range rec.Sealed {
		state.Sealed[path] = true
	}
	state.ImportedLegacy = state.ImportedLegacy || rec.ImportsLegacy
	if rec.Sequence > state.LastSequence {
		state.LastSequence = rec.Sequence
	}
	state.Records[rec.Sequence] = rec
}

func appendHistoryRecord(path string, rec historyRecord) error {
	_, line, err := encodeHistoryRecord(rec)
	if err != nil {
		return err
	}
	return appendHistoryLine(path, line)
}

func appendHistoryLine(path string, line []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() > 0 {
		var last [1]byte
		if _, err := f.ReadAt(last[:], info.Size()-1); err != nil && err != io.EOF {
			return err
		}
		if last[0] != '\n' {
			if _, err := f.Write([]byte{'\n'}); err != nil {
				return err
			}
		}
	}
	if _, err := f.Write(line); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return syncHistoryDir(filepath.Dir(path))
}

func syncHistoryDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func sortedHistorySequences(records map[uint64]historyRecord) []uint64 {
	sequences := make([]uint64, 0, len(records))
	for seq := range records {
		sequences = append(sequences, seq)
	}
	sort.Slice(sequences, func(i, j int) bool { return sequences[i] < sequences[j] })
	return sequences
}

func repairHistoryCopies(paths historyPaths, state historyState, copies historyCopies) error {
	for _, seq := range sortedHistorySequences(state.Records) {
		rec := state.Records[seq]
		if current, ok := copies.PrimaryRecords[seq]; !ok || current.Checksum != rec.Checksum {
			if err := appendHistoryRecord(paths.Primary, rec); err != nil {
				return fmt.Errorf("repair primary usage history: %w", err)
			}
		}
		if current, ok := copies.BackupRecords[seq]; !ok || current.Checksum != rec.Checksum {
			if err := appendHistoryRecord(paths.Backup, rec); err != nil {
				return fmt.Errorf("repair backup usage history: %w", err)
			}
		}
	}
	return nil
}

func sourceUpdateIsNewer(existing, candidate historySource) bool {
	if candidate.ParserVersion != existing.ParserVersion {
		return candidate.ParserVersion > existing.ParserVersion
	}
	if candidate.Meta.ModTime.After(existing.Meta.ModTime) {
		return true
	}
	if candidate.Meta.ModTime.Before(existing.Meta.ModTime) {
		return false
	}
	return candidate.Meta.Size > existing.Meta.Size
}

func filteredHistorySources(state historyState, updates map[string]historySource) map[string]historySource {
	filtered := map[string]historySource{}
	for path, candidate := range updates {
		existing, ok := state.Sources[path]
		if ok && !sourceUpdateIsNewer(existing, candidate) {
			continue
		}
		filtered[path] = candidate
	}
	return filtered
}

func historySealedPaths(sealed map[string]bool) []string {
	paths := make([]string, 0, len(sealed))
	for path, isSealed := range sealed {
		if isSealed {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths
}

func commitHistory(paths historyPaths, update historyUpdate) (historyState, error) {
	if err := os.MkdirAll(filepath.Dir(paths.Lock), 0o700); err != nil {
		return historyState{}, err
	}
	lock, err := os.OpenFile(paths.Lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return historyState{}, err
	}
	defer lock.Close()
	if err := lock.Chmod(0o600); err != nil {
		return historyState{}, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return historyState{}, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck -- close also releases the lock

	state, copies, err := readHistoryCopies(paths)
	if err != nil {
		return historyState{}, err
	}
	if err := repairHistoryCopies(paths, state, copies); err != nil {
		return historyState{}, err
	}

	rec := historyRecord{
		Sequence: state.LastSequence + 1,
		Sources:  filteredHistorySources(state, update.Sources),
	}
	if update.ImportLegacy && !state.ImportedLegacy {
		rec.Archive = update.LegacyArchive
		rec.Sealed = historySealedPaths(update.LegacySealed)
		rec.ImportsLegacy = true
	}
	if len(rec.Sources) == 0 && !rec.ImportsLegacy {
		return state, nil
	}
	sealed, line, err := encodeHistoryRecord(rec)
	if err != nil {
		return historyState{}, err
	}
	if err := appendHistoryLine(paths.Primary, line); err != nil {
		return historyState{}, fmt.Errorf("append primary usage history: %w", err)
	}
	if err := appendHistoryLine(paths.Backup, line); err != nil {
		return historyState{}, fmt.Errorf("append backup usage history: %w", err)
	}
	applyHistoryRecord(&state, sealed)
	return state, nil
}
