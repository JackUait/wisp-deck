package attention

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const maxClaudeRegistryRecordBytes = 256 * 1024

var psSnapshotLine = regexp.MustCompile(`^[ \t]*([0-9]+)[ \t]+([0-9]+)[ \t]+(.+?)[ \t]*$`)

// ProcessSnapshotFunc captures one complete process table. A mapper calls it
// exactly once per Poll, so ancestry and process start identities come from the
// same point-in-time view.
type ProcessSnapshotFunc func(context.Context) ([]byte, error)

// ReadFileFunc is the mapper's filesystem boundary. Production callers can use
// os.ReadFile; tests and other platforms can provide a deterministic reader.
type ReadFileFunc func(string) ([]byte, error)

// CommandOutputFunc is the injectable process boundary used by PSSnapshotter.
// env contains explicit key=value overrides, not the caller's inherited
// environment.
type CommandOutputFunc func(context.Context, string, []string, []string) ([]byte, error)

// PSSnapshotter captures the portable fields needed to validate a Claude
// registry record. Locale and timezone are fixed because procStart is compared
// byte-for-byte with Claude's process identity.
type PSSnapshotter struct {
	Run CommandOutputFunc
}

// Snapshot runs one locale-stable, UTC process-table command.
func (s PSSnapshotter) Snapshot(ctx context.Context) ([]byte, error) {
	run := s.Run
	if run == nil {
		run = commandOutput
	}
	output, err := run(
		ctx,
		"ps",
		[]string{"-axo", "pid=,ppid=,lstart="},
		[]string{"LC_ALL=C", "TZ=UTC"},
	)
	if err != nil {
		return nil, fmt.Errorf("snapshot processes: %w", err)
	}
	return output, nil
}

// ClaudeRegistryStatus is one strictly validated foreground registry
// observation. StatusIdentity is the canonical decimal updatedAt marker; the
// reducer can use it to deduplicate repeated observations without importing
// registry schema details.
type ClaudeRegistryStatus struct {
	PID            int
	Status         string
	StatusIdentity string
	WaitingFor     string
}

// ClaudeRegistryMapper correlates Claude's account-local registry with the
// supervised launch tree. Configuration and platform boundaries are explicit
// so polling is deterministic and account roots cannot be mixed.
type ClaudeRegistryMapper struct {
	ConfigDir     string
	LaunchRootPID int
	Snapshot      ProcessSnapshotFunc
	ReadFile      ReadFileFunc
}

// Poll returns the unique shallowest valid interactive session in the
// supervised launch tree. The root itself is eligible because a shell may exec
// Claude in place. Registry corruption, stale records, unknown status, and
// ambiguity are observations of uncertainty, so they return found=false rather
// than selecting a guess.
func (m ClaudeRegistryMapper) Poll(ctx context.Context) (ClaudeRegistryStatus, bool, error) {
	if m.ConfigDir == "" {
		return ClaudeRegistryStatus{}, false, errors.New("Claude config directory is empty")
	}
	if m.LaunchRootPID <= 0 {
		return ClaudeRegistryStatus{}, false, errors.New("Claude launch root PID must be positive")
	}

	snapshot := m.Snapshot
	if snapshot == nil {
		snapshot = (PSSnapshotter{}).Snapshot
	}
	readFile := m.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}

	data, err := snapshot(ctx)
	if err != nil {
		return ClaudeRegistryStatus{}, false, fmt.Errorf("capture Claude process snapshot: %w", err)
	}
	processes, err := parseProcessSnapshot(data)
	if err != nil {
		return ClaudeRegistryStatus{}, false, fmt.Errorf("parse Claude process snapshot: %w", err)
	}
	if _, ok := processes[m.LaunchRootPID]; !ok {
		return ClaudeRegistryStatus{}, false, nil
	}

	type candidate struct {
		pid   int
		depth int
		start string
	}
	var candidates []candidate
	for pid, process := range processes {
		// bash -c may exec its final simple command in place. In that common
		// fresh-launch case Claude is the stable supervised root, not a child.
		if pid == m.LaunchRootPID {
			candidates = append(candidates, candidate{pid: pid, depth: 0, start: process.start})
			continue
		}
		depth, descendant := processDepth(processes, pid, m.LaunchRootPID)
		if !descendant {
			continue
		}
		candidates = append(candidates, candidate{pid: pid, depth: depth, start: process.start})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].depth != candidates[j].depth {
			return candidates[i].depth < candidates[j].depth
		}
		return candidates[i].pid < candidates[j].pid
	})

	var best ClaudeRegistryStatus
	bestDepth := 0
	found := false
	ambiguous := false
	for _, candidate := range candidates {
		path := filepath.Join(m.ConfigDir, "sessions", strconv.Itoa(candidate.pid)+".json")
		recordData, readErr := readFile(path)
		if readErr != nil {
			continue
		}
		record, parseErr := parseClaudeRegistryRecord(recordData)
		if parseErr != nil || record.PID != candidate.pid || record.procStart != candidate.start {
			continue
		}

		if !found || candidate.depth < bestDepth {
			best = record.ClaudeRegistryStatus
			bestDepth = candidate.depth
			found = true
			ambiguous = false
			continue
		}
		if candidate.depth == bestDepth {
			ambiguous = true
		}
	}
	if !found || ambiguous {
		return ClaudeRegistryStatus{}, false, nil
	}
	return best, true, nil
}

type snapshotProcess struct {
	parent int
	start  string
}

func parseProcessSnapshot(data []byte) (map[int]snapshotProcess, error) {
	processes := make(map[int]snapshotProcess)
	lines := bytes.Split(data, []byte{'\n'})
	for lineNumber, rawLine := range lines {
		if len(rawLine) == 0 && lineNumber == len(lines)-1 {
			continue
		}
		line := string(rawLine)
		match := psSnapshotLine.FindStringSubmatch(line)
		if match == nil {
			return nil, fmt.Errorf("invalid ps row %d", lineNumber+1)
		}
		pid, err := parsePositiveInt(match[1])
		if err != nil {
			return nil, fmt.Errorf("invalid PID on ps row %d: %w", lineNumber+1, err)
		}
		parent, err := parseNonnegativeInt(match[2])
		if err != nil {
			return nil, fmt.Errorf("invalid parent PID on ps row %d: %w", lineNumber+1, err)
		}
		start := strings.TrimSpace(match[3])
		if _, err := time.Parse("Mon Jan _2 15:04:05 2006", start); err != nil {
			return nil, fmt.Errorf("invalid start time on ps row %d: %w", lineNumber+1, err)
		}
		if _, duplicate := processes[pid]; duplicate {
			return nil, fmt.Errorf("duplicate PID %d in ps snapshot", pid)
		}
		processes[pid] = snapshotProcess{parent: parent, start: start}
	}
	return processes, nil
}

func processDepth(processes map[int]snapshotProcess, pid, root int) (int, bool) {
	if pid == root {
		return 0, false
	}
	seen := make(map[int]struct{})
	current := pid
	depth := 0
	for current != root {
		if _, duplicate := seen[current]; duplicate {
			return 0, false
		}
		seen[current] = struct{}{}
		process, ok := processes[current]
		if !ok {
			return 0, false
		}
		current = process.parent
		depth++
	}
	return depth, true
}

type claudeRegistryRecord struct {
	ClaudeRegistryStatus
	procStart string
}

func parseClaudeRegistryRecord(data []byte) (claudeRegistryRecord, error) {
	if len(data) == 0 {
		return claudeRegistryRecord{}, errors.New("empty Claude registry record")
	}
	if len(data) > maxClaudeRegistryRecordBytes {
		return claudeRegistryRecord{}, fmt.Errorf("Claude registry record exceeds %d bytes", maxClaudeRegistryRecordBytes)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	opening, err := decoder.Token()
	if err != nil {
		return claudeRegistryRecord{}, err
	}
	if delimiter, ok := opening.(json.Delim); !ok || delimiter != '{' {
		return claudeRegistryRecord{}, errors.New("Claude registry record must be an object")
	}

	fields := make(map[string]json.RawMessage)
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, tokenErr := decoder.Token()
		if tokenErr != nil {
			return claudeRegistryRecord{}, tokenErr
		}
		key, ok := keyToken.(string)
		if !ok {
			return claudeRegistryRecord{}, errors.New("Claude registry key is not a string")
		}
		if _, duplicate := seen[key]; duplicate {
			return claudeRegistryRecord{}, fmt.Errorf("duplicate Claude registry field %q", key)
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return claudeRegistryRecord{}, err
		}
		fields[key] = value
	}
	closing, err := decoder.Token()
	if err != nil {
		return claudeRegistryRecord{}, err
	}
	if delimiter, ok := closing.(json.Delim); !ok || delimiter != '}' {
		return claudeRegistryRecord{}, errors.New("Claude registry object is not closed")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return claudeRegistryRecord{}, errors.New("Claude registry record has trailing data")
		}
		return claudeRegistryRecord{}, err
	}

	pid, err := parseJSONPositiveInt(fields["pid"])
	if err != nil {
		return claudeRegistryRecord{}, fmt.Errorf("invalid Claude registry pid: %w", err)
	}
	kind, err := parseJSONString(fields["kind"])
	if err != nil || kind != "interactive" {
		return claudeRegistryRecord{}, errors.New("Claude registry kind is not interactive")
	}
	procStart, err := parseJSONString(fields["procStart"])
	if err != nil {
		return claudeRegistryRecord{}, fmt.Errorf("invalid Claude registry procStart: %w", err)
	}
	if _, err := time.Parse("Mon Jan _2 15:04:05 2006", procStart); err != nil {
		return claudeRegistryRecord{}, fmt.Errorf("invalid Claude registry procStart: %w", err)
	}
	status, err := parseJSONString(fields["status"])
	if err != nil {
		return claudeRegistryRecord{}, fmt.Errorf("invalid Claude registry status: %w", err)
	}
	switch status {
	case "idle", "busy", "waiting":
	default:
		return claudeRegistryRecord{}, fmt.Errorf("unknown Claude registry status %q", status)
	}
	identity, err := parseJSONNonnegativeDecimal(fields["updatedAt"])
	if err != nil {
		return claudeRegistryRecord{}, fmt.Errorf("invalid Claude registry updatedAt: %w", err)
	}

	waitingFor := ""
	if raw, ok := fields["waitingFor"]; ok {
		waitingFor, err = parseJSONString(raw)
		if err != nil || len(waitingFor) > 1024 || strings.IndexFunc(waitingFor, unicode.IsControl) >= 0 {
			return claudeRegistryRecord{}, errors.New("invalid Claude registry waitingFor")
		}
	}

	return claudeRegistryRecord{
		ClaudeRegistryStatus: ClaudeRegistryStatus{
			PID:            pid,
			Status:         status,
			StatusIdentity: identity,
			WaitingFor:     waitingFor,
		},
		procStart: procStart,
	}, nil
}

func parseJSONString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("field is missing")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}

func parseJSONPositiveInt(raw json.RawMessage) (int, error) {
	if len(raw) == 0 {
		return 0, errors.New("field is missing")
	}
	return parsePositiveInt(string(raw))
}

func parseJSONNonnegativeDecimal(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("field is missing")
	}
	text := string(raw)
	if text == "0" {
		return text, nil
	}
	if text == "" || text[0] < '1' || text[0] > '9' {
		return "", errors.New("value is not a canonical nonnegative integer")
	}
	for index := 1; index < len(text); index++ {
		if text[index] < '0' || text[index] > '9' {
			return "", errors.New("value is not a canonical nonnegative integer")
		}
	}
	if _, err := strconv.ParseUint(text, 10, 64); err != nil {
		return "", err
	}
	return text, nil
}

func parsePositiveInt(text string) (int, error) {
	value, err := parseNonnegativeInt(text)
	if err != nil {
		return 0, err
	}
	if value == 0 {
		return 0, errors.New("value must be positive")
	}
	return value, nil
}

func parseNonnegativeInt(text string) (int, error) {
	canonical, err := parseJSONNonnegativeDecimal(json.RawMessage(text))
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(canonical, 10, strconv.IntSize)
	if err != nil {
		return 0, err
	}
	return int(value), nil
}

func commandOutput(ctx context.Context, name string, args, overrides []string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Env = applyEnvironmentOverrides(os.Environ(), overrides)
	return command.Output()
}

func applyEnvironmentOverrides(environment, overrides []string) []string {
	keys := make(map[string]struct{}, len(overrides))
	for _, override := range overrides {
		if key, _, ok := strings.Cut(override, "="); ok {
			keys[key] = struct{}{}
		}
	}
	result := make([]string, 0, len(environment)+len(overrides))
	for _, entry := range environment {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, replaced := keys[key]; replaced {
				continue
			}
		}
		result = append(result, entry)
	}
	return append(result, overrides...)
}
