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
	"syscall"
	"time"
	"unicode"
)

const (
	maxClaudeRegistryRecordBytes = 256 * 1024
	maxClaudeRegistryJobIDBytes  = 256
	maxClaudeRegistryCwdBytes    = 4096
	// A bound on the account-local registry directory, which holds one file per
	// live Claude process. Anything past it is not a session list.
	maxClaudeRegistrySessionFiles = 4096
	claudePSExecutable            = "/bin/ps"
)

var psSnapshotLine = regexp.MustCompile(`^[ \t]*([0-9]+)[ \t]+([0-9]+)[ \t]+(.+?)[ \t]*$`)

var claudeRegistryFileName = regexp.MustCompile(`^([0-9]+)\.json$`)

// ProcessSnapshotFunc captures one complete process table. A mapper calls it
// exactly once per Poll, so ancestry and process start identities come from the
// same point-in-time view.
type ProcessSnapshotFunc func(context.Context) ([]byte, error)

// ReadFileFunc is an explicitly trusted mapper filesystem boundary for tests
// and other platforms. Production uses the bounded no-follow reader below.
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
		claudePSExecutable,
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
//
// PID, SessionID and Cwd always name the supervised interactive session, the
// conversation it currently holds, and the directory it is working in. While
// that session is parked, the status fields come from the background job
// running its turn — see resolveParkedJob.
type ClaudeRegistryStatus struct {
	PID            int
	SessionID      string
	Cwd            string
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
	// Processes is the process table the supervisor tick already read. When it
	// is supplied the poll reuses it rather than materialising the table a
	// second time within the same tick.
	Processes []SupervisorProcess
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

	readFile := m.ReadFile
	if readFile == nil {
		readFile = readClaudeRegistryFile
	}

	// This poll runs 4 times a second in every open session. An injected
	// Snapshot is a test seam and still travels as ps-shaped bytes; production
	// reads the same fields straight from the kernel, because forking `ps` here
	// cost 80 CPU-ms a call and kept one `ps` process resident per session.
	var processes map[int]snapshotProcess
	if m.Snapshot == nil && len(m.Processes) > 0 {
		processes = make(map[int]snapshotProcess, len(m.Processes))
		for _, row := range m.Processes {
			processes[row.PID] = snapshotProcess{parent: row.PPID, startSec: row.StartSec}
		}
	} else if m.Snapshot != nil {
		data, err := m.Snapshot(ctx)
		if err != nil {
			return ClaudeRegistryStatus{}, false, fmt.Errorf("capture Claude process snapshot: %w", err)
		}
		parsed, err := parseProcessSnapshot(data)
		if err != nil {
			return ClaudeRegistryStatus{}, false, fmt.Errorf("parse Claude process snapshot: %w", err)
		}
		processes = parsed
	} else {
		table, err := systemProcessTable()
		if err != nil {
			return ClaudeRegistryStatus{}, false, fmt.Errorf("capture Claude process snapshot: %w", err)
		}
		processes = make(map[int]snapshotProcess, len(table))
		for _, row := range table {
			processes[row.PID] = snapshotProcess{parent: row.PPID, startSec: row.StartSec}
		}
	}
	if _, ok := processes[m.LaunchRootPID]; !ok {
		return ClaudeRegistryStatus{}, false, nil
	}

	type candidate struct {
		pid      int
		depth    int
		startSec int64
	}
	ancestry := newAncestryResolver(m.LaunchRootPID, len(processes), func(pid int) (int, bool) {
		process, ok := processes[pid]
		return process.parent, ok
	})

	var candidates []candidate
	for pid, process := range processes {
		// bash -c may exec its final simple command in place. In that common
		// fresh-launch case Claude is the stable supervised root, not a child.
		if pid == m.LaunchRootPID {
			candidates = append(candidates, candidate{pid: pid, depth: 0, startSec: process.startSec})
			continue
		}
		depth, descendant := ancestry.depth(pid)
		if !descendant {
			continue
		}
		candidates = append(candidates, candidate{pid: pid, depth: depth, startSec: process.startSec})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].depth != candidates[j].depth {
			return candidates[i].depth < candidates[j].depth
		}
		return candidates[i].pid < candidates[j].pid
	})

	var best ClaudeRegistryStatus
	bestParkedJobID := ""
	bestDepth := 0
	found := false
	ambiguous := false
	for _, candidate := range candidates {
		// Candidates are sorted shallowest first, so once a record is in hand
		// nothing deeper can beat it or make it ambiguous — only another record
		// at the SAME depth can, and those come first. Without this the poll
		// opened a registry file for every process in the supervised tree on
		// every tick, which for a session running a test sweep is thousands.
		if found && candidate.depth > bestDepth {
			break
		}
		path := filepath.Join(m.ConfigDir, "sessions", strconv.Itoa(candidate.pid)+".json")
		recordData, readErr := readFile(path)
		if readErr != nil {
			continue
		}
		record, parseErr := parseClaudeRegistryRecord(recordData)
		if parseErr != nil || record.kind != "interactive" ||
			record.PID != candidate.pid || record.procStartSec != candidate.startSec {
			continue
		}

		if !found || candidate.depth < bestDepth {
			best = record.ClaudeRegistryStatus
			bestParkedJobID = record.parkedJobID
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
	if bestParkedJobID != "" {
		job, resolved := resolveParkedJob(m.ConfigDir, readFile, processes, bestParkedJobID)
		if !resolved {
			return ClaudeRegistryStatus{}, false, nil
		}
		best.Status = job.Status
		best.StatusIdentity = job.StatusIdentity
		best.WaitingFor = job.WaitingFor
	}
	return best, true, nil
}

// resolveParkedJob answers what a parked session is actually doing.
//
// Claude parks a turn by handing it to a background job process the daemon owns.
// That process is claimed from the daemon's own pool, so it is never a
// descendant of the supervised launch root and tree-scoped discovery
// structurally cannot reach it. Worse, parking writes only parkedJobId and
// unparking only clears it — neither ever touches status — so a parked session's
// own status stays frozen at whatever it held when the turn moved away, for as
// long as the park lasts. Reading it as the session's current state is how a
// working agent came to be reported idle, ending its turn and raising a
// notification that then had nothing left to clear it.
//
// The link out of the tree is the session's own parkedJobId, so the job record
// is trusted no further than a launch-tree one: it must be a background record
// naming that job, and it must describe its own live process, so a leftover file
// for a recycled PID can never speak for the session. Ambiguity and an
// unresolvable job are uncertainty, never a guess.
func resolveParkedJob(
	configDir string,
	readFile ReadFileFunc,
	processes map[int]snapshotProcess,
	jobID string,
) (ClaudeRegistryStatus, bool) {
	sessionsDir := filepath.Join(configDir, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil || len(entries) > maxClaudeRegistrySessionFiles {
		return ClaudeRegistryStatus{}, false
	}

	var job ClaudeRegistryStatus
	found := false
	for _, entry := range entries {
		match := claudeRegistryFileName.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		pid, pidErr := parsePositiveInt(match[1])
		if pidErr != nil {
			continue
		}
		// The point-in-time process table prunes every dead session's leftover
		// record without opening it, keeping the scan proportional to the live
		// ones.
		process, live := processes[pid]
		if !live {
			continue
		}
		recordData, readErr := readFile(filepath.Join(sessionsDir, entry.Name()))
		if readErr != nil {
			continue
		}
		record, parseErr := parseClaudeRegistryRecord(recordData)
		if parseErr != nil || record.kind != "bg" || record.jobID != jobID ||
			record.PID != pid || record.procStartSec != process.startSec {
			continue
		}
		if found {
			return ClaudeRegistryStatus{}, false
		}
		job = record.ClaudeRegistryStatus
		found = true
	}
	return job, found
}

// readClaudeRegistryFile reads one bounded regular registry record without
// following a final symlink or ever blocking on a substituted FIFO. The open
// and fstat operate on the same descriptor, closing the lstat/open race.
func readClaudeRegistryFile(path string) ([]byte, error) {
	fd, err := syscall.Open(
		path,
		syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("open Claude registry record: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = syscall.Close(fd)
		return nil, errors.New("open Claude registry record: invalid file descriptor")
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat Claude registry record: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("Claude registry record is not a regular file")
	}
	if info.Size() > maxClaudeRegistryRecordBytes {
		return nil, fmt.Errorf("Claude registry record exceeds %d bytes", maxClaudeRegistryRecordBytes)
	}

	data, err := io.ReadAll(io.LimitReader(file, maxClaudeRegistryRecordBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Claude registry record: %w", err)
	}
	if len(data) > maxClaudeRegistryRecordBytes {
		return nil, fmt.Errorf("Claude registry record exceeds %d bytes", maxClaudeRegistryRecordBytes)
	}
	return data, nil
}

// lstartLayout is the format `ps -o lstart=` prints under LC_ALL=C and TZ=UTC,
// and the shape Claude Code writes procStart in. `_2` is the space-padded day.
// It is parsed, never rendered: a start time travels as seconds.
const lstartLayout = "Mon Jan _2 15:04:05 2006"

type snapshotProcess struct {
	parent   int
	startSec int64
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
		started, err := time.Parse(lstartLayout, strings.TrimSpace(match[3]))
		if err != nil {
			return nil, fmt.Errorf("invalid start time on ps row %d: %w", lineNumber+1, err)
		}
		if _, duplicate := processes[pid]; duplicate {
			return nil, fmt.Errorf("duplicate PID %d in ps snapshot", pid)
		}
		processes[pid] = snapshotProcess{parent: parent, startSec: started.Unix()}
	}
	return processes, nil
}

// ancestryResolver answers "how far below root is this process?" for many
// processes over one snapshot, remembering each answer.
//
// It replaces walking every PID's ancestry independently. That was quadratic in
// the SUPERVISED tree — a chain of N descendants walked N chains of average
// length N/2 — and built a cycle-detection set for every walk. Measured on a
// 3500-row table: a 200-process tree cost 4.2ms a poll and a 1000-process one
// 61ms, which at 4Hz is half a core for one session. An agent running a test
// sweep or a dev server routinely puts hundreds of processes under its root.
//
// Only descendants are remembered. A process that does not descend from root
// walks up to init and stops, exactly as before, so a machine full of unrelated
// processes costs no map the size of the machine.
type ancestryResolver struct {
	parentOf func(int) (int, bool)
	root     int
	// size is how many processes the snapshot holds. A chain longer than that
	// must repeat one, which is the cycle the per-walk set used to catch.
	size   int
	depths map[int]int
	// pending is reused across calls, so a walk of any depth allocates nothing.
	pending []int
}

func newAncestryResolver(root, size int, parentOf func(int) (int, bool)) *ancestryResolver {
	return &ancestryResolver{parentOf: parentOf, root: root, size: size, depths: map[int]int{}}
}

// depth reports how many parent links separate pid from the root, and whether
// pid descends from it at all. The root itself is not its own descendant.
func (r *ancestryResolver) depth(pid int) (int, bool) {
	if pid <= 0 || pid == r.root {
		return 0, false
	}
	if known, ok := r.depths[pid]; ok {
		return known, true
	}
	r.pending = r.pending[:0]
	current := pid
	depth := -1
	for {
		if current == r.root {
			depth = 0
			break
		}
		if known, ok := r.depths[current]; ok {
			depth = known
			break
		}
		if current <= 0 || len(r.pending) > r.size {
			break
		}
		parent, known := r.parentOf(current)
		if !known {
			break
		}
		r.pending = append(r.pending, current)
		current = parent
	}
	if depth < 0 {
		return 0, false
	}
	for index := len(r.pending) - 1; index >= 0; index-- {
		depth++
		r.depths[r.pending[index]] = depth
	}
	return depth, true
}

type claudeRegistryRecord struct {
	ClaudeRegistryStatus
	procStartSec int64
	// kind is validated but not filtered here: the caller decides which kind it
	// is asking about — "interactive" for the supervised session, "bg" for the
	// job a parked session handed its turn to.
	kind        string
	jobID       string
	parkedJobID string
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
	if err != nil || kind == "" {
		return claudeRegistryRecord{}, errors.New("invalid Claude registry kind")
	}
	procStart, err := parseJSONString(fields["procStart"])
	if err != nil {
		return claudeRegistryRecord{}, fmt.Errorf("invalid Claude registry procStart: %w", err)
	}
	started, err := time.Parse(lstartLayout, procStart)
	if err != nil {
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

	sessionID, err := parseClaudeRegistryIdentifier(fields, "sessionId")
	if err != nil {
		return claudeRegistryRecord{}, err
	}
	cwd, err := parseClaudeRegistryWorkingDirectory(fields)
	if err != nil {
		return claudeRegistryRecord{}, err
	}
	jobID, err := parseClaudeRegistryIdentifier(fields, "jobId")
	if err != nil {
		return claudeRegistryRecord{}, err
	}
	parkedJobID, err := parseClaudeRegistryIdentifier(fields, "parkedJobId")
	if err != nil {
		return claudeRegistryRecord{}, err
	}

	return claudeRegistryRecord{
		ClaudeRegistryStatus: ClaudeRegistryStatus{
			PID:            pid,
			SessionID:      sessionID,
			Cwd:            cwd,
			Status:         status,
			StatusIdentity: identity,
			WaitingFor:     waitingFor,
		},
		procStartSec: started.Unix(),
		kind:         kind,
		jobID:        jobID,
		parkedJobID:  parkedJobID,
	}, nil
}

// parseClaudeRegistryWorkingDirectory reads the session's optional cwd. Absent
// is an ordinary answer — a Claude predating the field reports none — but a
// present one is handed to a shell that respawns panes into it, so it fails
// closed on anything but a plain absolute path rather than being sanitized.
func parseClaudeRegistryWorkingDirectory(fields map[string]json.RawMessage) (string, error) {
	raw, ok := fields["cwd"]
	if !ok {
		return "", nil
	}
	value, err := parseJSONString(raw)
	if err != nil || value == "" || !strings.HasPrefix(value, "/") ||
		len(value) > maxClaudeRegistryCwdBytes ||
		strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", errors.New("invalid Claude registry cwd")
	}
	return value, nil
}

// parseClaudeRegistryIdentifier reads one optional record identifier. Absent is
// an ordinary answer — only background records carry jobId, only a parked
// session carries parkedJobId, and a Claude predating the field reports no
// sessionId — but a present one must be a plain bounded string, because these
// identifiers are compared: between two records to decide which one speaks, and
// across polls to decide whether the conversation was replaced.
func parseClaudeRegistryIdentifier(fields map[string]json.RawMessage, key string) (string, error) {
	raw, ok := fields[key]
	if !ok {
		return "", nil
	}
	value, err := parseJSONString(raw)
	if err != nil || len(value) > maxClaudeRegistryJobIDBytes ||
		strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("invalid Claude registry %s", key)
	}
	return value, nil
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
