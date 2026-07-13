package attention

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

const (
	maxClaudeBackgroundSnapshotBytes    = 1024 * 1024
	maxClaudeBackgroundPersistenceBytes = 1024 * 1024
	maxClaudeBackgroundRows             = 4096
	maxClaudeBackgroundJobIDBytes       = 256
	maxClaudeBackgroundWaitingBytes     = 4096
	maxClaudeBackgroundConfigRootBytes  = 16 * 1024
	claudeBackgroundPersistenceVersion  = 1
)

// ClaudeBackgroundStatus is the normalized state vocabulary consumed by the
// account-global notification broker. Raw CLI spellings remain available on
// ClaudeBackgroundJob so schema changes are visible at the parser boundary.
type ClaudeBackgroundStatus string

const (
	ClaudeBackgroundWorking   ClaudeBackgroundStatus = "working"
	ClaudeBackgroundBlocked   ClaudeBackgroundStatus = "blocked"
	ClaudeBackgroundCompleted ClaudeBackgroundStatus = "completed"
	ClaudeBackgroundFailed    ClaudeBackgroundStatus = "failed"
	ClaudeBackgroundStopped   ClaudeBackgroundStatus = "stopped"
)

// ClaudeBackgroundJob is one validated `claude agents --json --all`
// background row. Interactive rows never enter this type.
type ClaudeBackgroundJob struct {
	ID         string
	RawState   string
	Status     ClaudeBackgroundStatus
	WaitingFor string
}

// ClaudeBackgroundEvent is one newly observed attention transition. The exact
// config root is part of the identity; callers must not clean or resolve it and
// accidentally merge two independently configured Claude supervisors.
type ClaudeBackgroundEvent struct {
	ConfigRoot string
	JobID      string
	Status     ClaudeBackgroundStatus
	WaitingFor string
}

// ParseClaudeBackgroundJobs strictly parses the official CLI's top-level array.
// Unknown row fields are tolerated for forward compatibility. Every row still
// has to be an object with a string kind; only kind=background requires id and
// state. A malformed background row rejects the complete snapshot so a broker
// cannot persist a misleading partial view.
func ParseClaudeBackgroundJobs(data []byte) ([]ClaudeBackgroundJob, error) {
	if len(data) == 0 {
		return nil, errors.New("empty Claude background snapshot")
	}
	if len(data) > maxClaudeBackgroundSnapshotBytes {
		return nil, fmt.Errorf("Claude background snapshot exceeds %d bytes", maxClaudeBackgroundSnapshotBytes)
	}
	if !utf8.Valid(data) {
		return nil, errors.New("Claude background snapshot is not valid UTF-8")
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("parse Claude background snapshot: %w", err)
	}
	if delimiter, ok := opening.(json.Delim); !ok || delimiter != '[' {
		return nil, errors.New("Claude background snapshot must be an array")
	}

	jobs := make([]ClaudeBackgroundJob, 0)
	seenJobs := make(map[string]struct{})
	rowCount := 0
	for decoder.More() {
		rowCount++
		if rowCount > maxClaudeBackgroundRows {
			return nil, fmt.Errorf("Claude background snapshot exceeds %d rows", maxClaudeBackgroundRows)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, fmt.Errorf("parse Claude agents row %d: %w", rowCount, err)
		}
		fields, err := parseClaudeBackgroundObject(raw, "Claude agents row")
		if err != nil {
			return nil, fmt.Errorf("parse Claude agents row %d: %w", rowCount, err)
		}
		kind, err := parseClaudeBackgroundString(fields["kind"])
		if err != nil {
			return nil, fmt.Errorf("parse Claude agents row %d kind: %w", rowCount, err)
		}
		if kind != "background" {
			continue
		}

		job, err := parseClaudeBackgroundJob(fields)
		if err != nil {
			return nil, fmt.Errorf("parse Claude background row %d: %w", rowCount, err)
		}
		if _, duplicate := seenJobs[job.ID]; duplicate {
			return nil, fmt.Errorf("duplicate Claude background job %q", job.ID)
		}
		seenJobs[job.ID] = struct{}{}
		jobs = append(jobs, job)
	}
	closing, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("close Claude background snapshot: %w", err)
	}
	if delimiter, ok := closing.(json.Delim); !ok || delimiter != ']' {
		return nil, errors.New("Claude background snapshot array is not closed")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("Claude background snapshot has trailing data")
		}
		return nil, fmt.Errorf("parse Claude background snapshot trailing data: %w", err)
	}
	return jobs, nil
}

func parseClaudeBackgroundJob(fields map[string]json.RawMessage) (ClaudeBackgroundJob, error) {
	id, err := parseClaudeBackgroundString(fields["id"])
	if err != nil {
		return ClaudeBackgroundJob{}, fmt.Errorf("invalid id: %w", err)
	}
	if err := validateClaudeBackgroundJobID(id); err != nil {
		return ClaudeBackgroundJob{}, err
	}
	rawState, err := parseClaudeBackgroundString(fields["state"])
	if err != nil {
		return ClaudeBackgroundJob{}, fmt.Errorf("invalid state: %w", err)
	}
	status, err := normalizeClaudeBackgroundState(rawState)
	if err != nil {
		return ClaudeBackgroundJob{}, err
	}

	waitingFor := ""
	if status == ClaudeBackgroundBlocked {
		if raw, ok := fields["waitingFor"]; ok {
			waitingFor, err = parseClaudeBackgroundString(raw)
			if err != nil {
				return ClaudeBackgroundJob{}, fmt.Errorf("invalid waitingFor: %w", err)
			}
			if len(waitingFor) > maxClaudeBackgroundWaitingBytes || strings.IndexFunc(waitingFor, unicode.IsControl) >= 0 {
				return ClaudeBackgroundJob{}, errors.New("invalid waitingFor")
			}
		}
	}
	return ClaudeBackgroundJob{
		ID:         id,
		RawState:   rawState,
		Status:     status,
		WaitingFor: waitingFor,
	}, nil
}

func normalizeClaudeBackgroundState(raw string) (ClaudeBackgroundStatus, error) {
	switch raw {
	case "working":
		return ClaudeBackgroundWorking, nil
	case "blocked":
		return ClaudeBackgroundBlocked, nil
	case "done":
		return ClaudeBackgroundCompleted, nil
	case "failed":
		return ClaudeBackgroundFailed, nil
	case "stopped":
		return ClaudeBackgroundStopped, nil
	default:
		return "", fmt.Errorf("unknown Claude background state %q", raw)
	}
}

func parseClaudeBackgroundObject(data []byte, label string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := opening.(json.Delim); !ok || delimiter != '{' {
		return nil, fmt.Errorf("%s must be an object", label)
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("%s key is not a string", label)
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, fmt.Errorf("duplicate %s field %q", label, key)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields[key] = value
	}
	closing, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := closing.(json.Delim); !ok || delimiter != '}' {
		return nil, fmt.Errorf("%s object is not closed", label)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("%s has trailing data", label)
		}
		return nil, err
	}
	return fields, nil
}

func parseClaudeBackgroundString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("field is missing")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}

func validateClaudeBackgroundJobID(id string) error {
	if id == "" {
		return errors.New("Claude background job id is empty")
	}
	if len(id) > maxClaudeBackgroundJobIDBytes {
		return fmt.Errorf("Claude background job id exceeds %d bytes", maxClaudeBackgroundJobIDBytes)
	}
	if !utf8.ValidString(id) || strings.IndexFunc(id, unicode.IsControl) >= 0 {
		return errors.New("Claude background job id is invalid")
	}
	return nil
}

func validateClaudeBackgroundConfigRoot(configRoot string) error {
	if configRoot == "" {
		return errors.New("Claude background config root is empty")
	}
	if len(configRoot) > maxClaudeBackgroundConfigRootBytes {
		return fmt.Errorf("Claude background config root exceeds %d bytes", maxClaudeBackgroundConfigRootBytes)
	}
	if !utf8.ValidString(configRoot) || strings.IndexFunc(configRoot, unicode.IsControl) >= 0 {
		return errors.New("Claude background config root is invalid")
	}
	return nil
}

func validateClaudeBackgroundStatus(status ClaudeBackgroundStatus) error {
	switch status {
	case ClaudeBackgroundWorking, ClaudeBackgroundBlocked,
		ClaudeBackgroundCompleted, ClaudeBackgroundFailed, ClaudeBackgroundStopped:
		return nil
	default:
		return fmt.Errorf("unknown Claude background status %q", status)
	}
}

func claudeBackgroundNeedsAttention(status ClaudeBackgroundStatus) bool {
	switch status {
	case ClaudeBackgroundBlocked, ClaudeBackgroundCompleted, ClaudeBackgroundFailed, ClaudeBackgroundStopped:
		return true
	default:
		return false
	}
}

type claudeBackgroundPersistence struct {
	version     int
	configRoot  string
	initialized bool
	jobs        map[string]ClaudeBackgroundStatus
}

type claudeBackgroundPersistenceRecord struct {
	Version     int                                  `json:"version"`
	ConfigRoot  string                               `json:"configRoot"`
	Initialized bool                                 `json:"initialized"`
	Jobs        []claudeBackgroundPersistenceJobJSON `json:"jobs"`
}

type claudeBackgroundPersistenceJobJSON struct {
	ID     string                 `json:"id"`
	Status ClaudeBackgroundStatus `json:"status"`
}

// ClaudeBackgroundTracker reduces validated account snapshots and persists the
// last state before returning events. Persist-before-return makes broker
// handoff/restart at-most-once: a repeated poll cannot duplicate a notification.
// Leadership election remains outside this pure component.
type ClaudeBackgroundTracker struct {
	mu sync.Mutex

	path       string
	configRoot string
	state      claudeBackgroundPersistence
}

// NewClaudeBackgroundTracker opens one exact config root's durable dedupe file.
// Its parent directory must already exist; this keeps storage placement under
// the broker/controller's authority.
func NewClaudeBackgroundTracker(path, configRoot string) (*ClaudeBackgroundTracker, error) {
	if path == "" {
		return nil, errors.New("Claude background persistence path is empty")
	}
	if err := validateClaudeBackgroundConfigRoot(configRoot); err != nil {
		return nil, err
	}
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil {
		return nil, fmt.Errorf("stat Claude background persistence parent: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("Claude background persistence parent %q is not a directory", parent)
	}

	state := claudeBackgroundPersistence{
		version:    claudeBackgroundPersistenceVersion,
		configRoot: configRoot,
		jobs:       make(map[string]ClaudeBackgroundStatus),
	}
	data, err := os.ReadFile(path)
	if err == nil {
		state, err = parseClaudeBackgroundPersistence(data)
		if err != nil {
			return nil, fmt.Errorf("read Claude background persistence: %w", err)
		}
		if state.configRoot != configRoot {
			return nil, fmt.Errorf("Claude background persistence belongs to config root %q, not %q", state.configRoot, configRoot)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read Claude background persistence: %w", err)
	}

	return &ClaudeBackgroundTracker{
		path:       path,
		configRoot: configRoot,
		state:      state,
	}, nil
}

// ObserveSnapshot is the convenience boundary for a command runner: parsing is
// all-or-nothing, then the validated jobs enter the durable reducer.
func (t *ClaudeBackgroundTracker) ObserveSnapshot(data []byte) ([]ClaudeBackgroundEvent, error) {
	jobs, err := ParseClaudeBackgroundJobs(data)
	if err != nil {
		return nil, err
	}
	return t.Observe(jobs)
}

// Observe persists the complete new snapshot before returning newly reduced
// attention events. Initial terminal jobs are a baseline; initial blocked jobs
// still need attention. Once initialized, a newly appearing terminal job also
// emits because it may have started and finished between broker polls.
func (t *ClaudeBackgroundTracker) Observe(jobs []ClaudeBackgroundJob) ([]ClaudeBackgroundEvent, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(jobs) > maxClaudeBackgroundRows {
		return nil, fmt.Errorf("Claude background snapshot exceeds %d jobs", maxClaudeBackgroundRows)
	}
	byID := make(map[string]ClaudeBackgroundJob, len(jobs))
	ids := make([]string, 0, len(jobs))
	for _, job := range jobs {
		if err := validateClaudeBackgroundJobID(job.ID); err != nil {
			return nil, err
		}
		if err := validateClaudeBackgroundStatus(job.Status); err != nil {
			return nil, err
		}
		if job.Status != ClaudeBackgroundBlocked {
			// waitingFor is live-status metadata. The official background state is
			// authoritative, so stale metadata cannot promote or invalidate it.
			job.WaitingFor = ""
		} else if len(job.WaitingFor) > maxClaudeBackgroundWaitingBytes || strings.IndexFunc(job.WaitingFor, unicode.IsControl) >= 0 {
			return nil, fmt.Errorf("invalid waitingFor for Claude background job %q", job.ID)
		}
		if _, duplicate := byID[job.ID]; duplicate {
			return nil, fmt.Errorf("duplicate Claude background job %q", job.ID)
		}
		byID[job.ID] = job
		ids = append(ids, job.ID)
	}
	sort.Strings(ids)

	// Absence is not a state transition. Start from the durable ledger and
	// overlay the current snapshot so a deleted/temporarily omitted terminal job
	// stays deduped if it later reappears unchanged.
	nextJobs := make(map[string]ClaudeBackgroundStatus, len(t.state.jobs)+len(jobs))
	for id, status := range t.state.jobs {
		nextJobs[id] = status
	}
	currentIDs := make(map[string]struct{}, len(jobs))
	var events []ClaudeBackgroundEvent
	for _, id := range ids {
		job := byID[id]
		nextJobs[id] = job.Status
		currentIDs[id] = struct{}{}
		previous, existed := t.state.jobs[id]
		if !claudeBackgroundNeedsAttention(job.Status) {
			continue
		}
		shouldEmit := false
		if !t.state.initialized {
			shouldEmit = job.Status == ClaudeBackgroundBlocked
		} else {
			shouldEmit = !existed || previous != job.Status
		}
		if shouldEmit {
			events = append(events, ClaudeBackgroundEvent{
				ConfigRoot: t.configRoot,
				JobID:      job.ID,
				Status:     job.Status,
				WaitingFor: job.WaitingFor,
			})
		}
	}

	next := claudeBackgroundPersistence{
		version:     claudeBackgroundPersistenceVersion,
		configRoot:  t.configRoot,
		initialized: true,
		jobs:        nextJobs,
	}
	if sameClaudeBackgroundPersistence(t.state, next) {
		return events, nil
	}
	next, data, err := boundClaudeBackgroundPersistence(next, currentIDs)
	if err != nil {
		return nil, err
	}
	if err := atomicReplaceClaudeBackgroundPersistence(t.path, data); err != nil {
		return nil, err
	}
	t.state = next
	return events, nil
}

func sameClaudeBackgroundPersistence(left, right claudeBackgroundPersistence) bool {
	if left.version != right.version || left.configRoot != right.configRoot || left.initialized != right.initialized || len(left.jobs) != len(right.jobs) {
		return false
	}
	for id, status := range left.jobs {
		if right.jobs[id] != status {
			return false
		}
	}
	return true
}

// boundClaudeBackgroundPersistence retains the lexicographically earliest
// absent IDs until both hard bounds fit. Current snapshot IDs are never
// eligible for pruning. The ordering is deliberately content-based so every
// broker leader makes the same choice after a handoff.
func boundClaudeBackgroundPersistence(
	state claudeBackgroundPersistence,
	currentIDs map[string]struct{},
) (claudeBackgroundPersistence, []byte, error) {
	if len(currentIDs) > maxClaudeBackgroundRows {
		return claudeBackgroundPersistence{}, nil, fmt.Errorf(
			"current Claude background snapshot exceeds persistence row limit %d",
			maxClaudeBackgroundRows,
		)
	}
	absentIDs := make([]string, 0, len(state.jobs)-len(currentIDs))
	for id := range state.jobs {
		if _, current := currentIDs[id]; !current {
			absentIDs = append(absentIDs, id)
		}
	}
	sort.Strings(absentIDs)
	maxAbsent := maxClaudeBackgroundRows - len(currentIDs)
	if len(absentIDs) > maxAbsent {
		absentIDs = absentIDs[:maxAbsent]
	}

	buildCandidate := func(absentCount int) (claudeBackgroundPersistence, []byte, error) {
		jobs := make(map[string]ClaudeBackgroundStatus, len(currentIDs)+absentCount)
		for id := range currentIDs {
			jobs[id] = state.jobs[id]
		}
		for _, id := range absentIDs[:absentCount] {
			jobs[id] = state.jobs[id]
		}
		candidate := claudeBackgroundPersistence{
			version:     state.version,
			configRoot:  state.configRoot,
			initialized: state.initialized,
			jobs:        jobs,
		}
		data, err := marshalClaudeBackgroundPersistenceRecord(candidate)
		return candidate, data, err
	}

	// Record size is monotonic as the sorted absent prefix grows. Binary search
	// the largest prefix that fits rather than repeatedly serializing thousands
	// of nearly identical megabyte records.
	low, high := 0, len(absentIDs)
	var best claudeBackgroundPersistence
	var bestData []byte
	for low <= high {
		middle := low + (high-low)/2
		candidate, data, err := buildCandidate(middle)
		if err != nil {
			return claudeBackgroundPersistence{}, nil, err
		}
		if len(data) <= maxClaudeBackgroundPersistenceBytes {
			best = candidate
			bestData = data
			low = middle + 1
		} else {
			high = middle - 1
		}
	}
	if bestData == nil {
		return claudeBackgroundPersistence{}, nil, fmt.Errorf(
			"current Claude background snapshot exceeds persistence byte limit %d",
			maxClaudeBackgroundPersistenceBytes,
		)
	}
	return best, bestData, nil
}

func marshalClaudeBackgroundPersistenceRecord(state claudeBackgroundPersistence) ([]byte, error) {
	ids := make([]string, 0, len(state.jobs))
	for id := range state.jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	jobs := make([]claudeBackgroundPersistenceJobJSON, 0, len(ids))
	for _, id := range ids {
		jobs = append(jobs, claudeBackgroundPersistenceJobJSON{ID: id, Status: state.jobs[id]})
	}
	record := claudeBackgroundPersistenceRecord{
		Version:     claudeBackgroundPersistenceVersion,
		ConfigRoot:  state.configRoot,
		Initialized: state.initialized,
		Jobs:        jobs,
	}
	data, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("marshal Claude background persistence: %w", err)
	}
	data = append(data, '\n')
	return data, nil
}

func parseClaudeBackgroundPersistence(data []byte) (claudeBackgroundPersistence, error) {
	if len(data) == 0 {
		return claudeBackgroundPersistence{}, errors.New("empty Claude background persistence")
	}
	if len(data) > maxClaudeBackgroundPersistenceBytes {
		return claudeBackgroundPersistence{}, fmt.Errorf("Claude background persistence exceeds %d bytes", maxClaudeBackgroundPersistenceBytes)
	}
	if !utf8.Valid(data) {
		return claudeBackgroundPersistence{}, errors.New("Claude background persistence is not valid UTF-8")
	}
	fields, err := parseClaudeBackgroundObject(data, "Claude background persistence")
	if err != nil {
		return claudeBackgroundPersistence{}, err
	}
	allowed := map[string]struct{}{
		"version": {}, "configRoot": {}, "initialized": {}, "jobs": {},
	}
	for field := range fields {
		if _, ok := allowed[field]; !ok {
			return claudeBackgroundPersistence{}, fmt.Errorf("unknown Claude background persistence field %q", field)
		}
	}

	var version int
	if len(fields["version"]) == 0 {
		return claudeBackgroundPersistence{}, errors.New("Claude background persistence version is missing")
	}
	if err := json.Unmarshal(fields["version"], &version); err != nil || version != claudeBackgroundPersistenceVersion || string(fields["version"]) != "1" {
		return claudeBackgroundPersistence{}, fmt.Errorf("unsupported Claude background persistence version %s", fields["version"])
	}
	configRoot, err := parseClaudeBackgroundString(fields["configRoot"])
	if err != nil {
		return claudeBackgroundPersistence{}, fmt.Errorf("invalid Claude background persistence config root: %w", err)
	}
	if err := validateClaudeBackgroundConfigRoot(configRoot); err != nil {
		return claudeBackgroundPersistence{}, err
	}
	if len(fields["initialized"]) == 0 {
		return claudeBackgroundPersistence{}, errors.New("Claude background persistence initialized field is missing")
	}
	var initialized bool
	if err := json.Unmarshal(fields["initialized"], &initialized); err != nil {
		return claudeBackgroundPersistence{}, fmt.Errorf("invalid Claude background persistence initialized field: %w", err)
	}
	jobs, err := parseClaudeBackgroundPersistenceJobs(fields["jobs"])
	if err != nil {
		return claudeBackgroundPersistence{}, err
	}
	if !initialized && len(jobs) != 0 {
		return claudeBackgroundPersistence{}, errors.New("uninitialized Claude background persistence contains jobs")
	}
	return claudeBackgroundPersistence{
		version:     version,
		configRoot:  configRoot,
		initialized: initialized,
		jobs:        jobs,
	}, nil
}

func parseClaudeBackgroundPersistenceJobs(data []byte) (map[string]ClaudeBackgroundStatus, error) {
	if len(data) == 0 {
		return nil, errors.New("Claude background persistence jobs field is missing")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := opening.(json.Delim); !ok || delimiter != '[' {
		return nil, errors.New("Claude background persistence jobs must be an array")
	}
	jobs := make(map[string]ClaudeBackgroundStatus)
	count := 0
	for decoder.More() {
		count++
		if count > maxClaudeBackgroundRows {
			return nil, fmt.Errorf("Claude background persistence exceeds %d jobs", maxClaudeBackgroundRows)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		fields, err := parseClaudeBackgroundObject(raw, "Claude background persistence job")
		if err != nil {
			return nil, err
		}
		if len(fields) != 2 {
			return nil, errors.New("Claude background persistence job must contain exactly id and status")
		}
		for field := range fields {
			if field != "id" && field != "status" {
				return nil, fmt.Errorf("unknown Claude background persistence job field %q", field)
			}
		}
		id, err := parseClaudeBackgroundString(fields["id"])
		if err != nil {
			return nil, fmt.Errorf("invalid persisted Claude background job id: %w", err)
		}
		if err := validateClaudeBackgroundJobID(id); err != nil {
			return nil, err
		}
		statusText, err := parseClaudeBackgroundString(fields["status"])
		if err != nil {
			return nil, fmt.Errorf("invalid persisted Claude background job status: %w", err)
		}
		status := ClaudeBackgroundStatus(statusText)
		if err := validateClaudeBackgroundStatus(status); err != nil {
			return nil, err
		}
		if _, duplicate := jobs[id]; duplicate {
			return nil, fmt.Errorf("duplicate persisted Claude background job %q", id)
		}
		jobs[id] = status
	}
	closing, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := closing.(json.Delim); !ok || delimiter != ']' {
		return nil, errors.New("Claude background persistence jobs array is not closed")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("Claude background persistence jobs has trailing data")
		}
		return nil, err
	}
	return jobs, nil
}

func atomicReplaceClaudeBackgroundPersistence(path string, data []byte) error {
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("stat Claude background persistence parent: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("Claude background persistence parent %q is not a directory", parent)
	}
	temporary, err := os.CreateTemp(parent, ".claude-background-*")
	if err != nil {
		return fmt.Errorf("create Claude background persistence temp file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod Claude background persistence temp file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write Claude background persistence temp file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync Claude background persistence temp file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close Claude background persistence temp file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace Claude background persistence: %w", err)
	}
	return nil
}
