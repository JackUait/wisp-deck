package attention

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestParseClaudeBackgroundJobsUsesOfficialBackgroundRows(t *testing.T) {
	data := []byte(`[
		{"kind":"interactive","status":"busy","pid":42},
		{"kind":"background","id":"job-working","state":"working","name":"build"},
		{"kind":"background","id":"job-done","state":"done","sessionId":"session-a"},
		{"kind":"background","id":"job-blocked","state":"blocked","waitingFor":"permission prompt"}
	]`)

	jobs, err := ParseClaudeBackgroundJobs(data)
	if err != nil {
		t.Fatalf("ParseClaudeBackgroundJobs() error = %v", err)
	}
	want := []ClaudeBackgroundJob{
		{ID: "job-working", RawState: "working", Status: ClaudeBackgroundWorking},
		{ID: "job-done", RawState: "done", Status: ClaudeBackgroundCompleted},
		{ID: "job-blocked", RawState: "blocked", Status: ClaudeBackgroundBlocked, WaitingFor: "permission prompt"},
	}
	if !reflect.DeepEqual(jobs, want) {
		t.Fatalf("ParseClaudeBackgroundJobs() = %#v, want %#v", jobs, want)
	}
}

func TestParseClaudeBackgroundJobsAcceptsDocumentedSemanticStates(t *testing.T) {
	tests := []struct {
		raw  string
		want ClaudeBackgroundStatus
	}{
		{raw: "working", want: ClaudeBackgroundWorking},
		{raw: "blocked", want: ClaudeBackgroundBlocked},
		{raw: "done", want: ClaudeBackgroundCompleted},
		{raw: "failed", want: ClaudeBackgroundFailed},
		{raw: "stopped", want: ClaudeBackgroundStopped},
	}

	for _, test := range tests {
		t.Run(test.raw, func(t *testing.T) {
			data := []byte(`[{"kind":"background","id":"job-a","state":"` + test.raw + `"}]`)
			jobs, err := ParseClaudeBackgroundJobs(data)
			if err != nil {
				t.Fatalf("ParseClaudeBackgroundJobs() error = %v", err)
			}
			if len(jobs) != 1 || jobs[0].Status != test.want || jobs[0].RawState != test.raw {
				t.Fatalf("ParseClaudeBackgroundJobs() = %#v, want status %q and raw state %q", jobs, test.want, test.raw)
			}
		})
	}
}

func TestParseClaudeBackgroundJobsIgnoresContradictoryLiveStatusMetadata(t *testing.T) {
	data := []byte(`[
		{"kind":"background","id":"working","state":"working","status":"waiting","waitingFor":"permission prompt"},
		{"kind":"background","id":"done","state":"done","status":"waiting","waitingFor":{"future":true}},
		{"kind":"background","id":"failed","state":"failed","status":"busy","waitingFor":"stale\ncontrol"}
	]`)
	jobs, err := ParseClaudeBackgroundJobs(data)
	if err != nil {
		t.Fatalf("ParseClaudeBackgroundJobs() error = %v", err)
	}
	want := []ClaudeBackgroundJob{
		{ID: "working", RawState: "working", Status: ClaudeBackgroundWorking},
		{ID: "done", RawState: "done", Status: ClaudeBackgroundCompleted},
		{ID: "failed", RawState: "failed", Status: ClaudeBackgroundFailed},
	}
	if !reflect.DeepEqual(jobs, want) {
		t.Fatalf("ParseClaudeBackgroundJobs() = %#v, want %#v", jobs, want)
	}
}

func TestClaudeBackgroundOpaqueJobIDRoundTripsThroughPersistence(t *testing.T) {
	const jobID = "job:α/β ?#%[]()"
	data := []byte(`[{"kind":"background","id":"` + jobID + `","state":"blocked","waitingFor":"question"}]`)
	jobs, err := ParseClaudeBackgroundJobs(data)
	if err != nil {
		t.Fatalf("ParseClaudeBackgroundJobs() error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != jobID {
		t.Fatalf("ParseClaudeBackgroundJobs() = %#v, want opaque id %q", jobs, jobID)
	}

	path := filepath.Join(t.TempDir(), "state.json")
	tracker := mustNewClaudeBackgroundTracker(t, path, "/accounts/a")
	want := []ClaudeBackgroundEvent{{ConfigRoot: "/accounts/a", JobID: jobID, Status: ClaudeBackgroundBlocked, WaitingFor: "question"}}
	assertBackgroundEvents(t, tracker, jobs, want)
	restarted := mustNewClaudeBackgroundTracker(t, path, "/accounts/a")
	assertBackgroundEvents(t, restarted, jobs, nil)
	if got := readClaudeBackgroundPersistenceForTest(t, path).jobs[jobID]; got != ClaudeBackgroundBlocked {
		t.Fatalf("persisted opaque id status = %q, want %q", got, ClaudeBackgroundBlocked)
	}
}

func TestParseClaudeBackgroundJobsRejectsUndocumentedStateAliases(t *testing.T) {
	for _, raw := range []string{"running", "busy", "idle", "waiting", "completed"} {
		t.Run(raw, func(t *testing.T) {
			data := []byte(`[{"kind":"background","id":"job-a","state":"` + raw + `"}]`)
			if jobs, err := ParseClaudeBackgroundJobs(data); err == nil {
				t.Fatalf("ParseClaudeBackgroundJobs() = %#v, want error", jobs)
			}
		})
	}
}

func TestParseClaudeBackgroundJobsRejectsMalformedSnapshotAtomically(t *testing.T) {
	valid := `{"kind":"background","id":"job-a","state":"working"}`
	tests := []struct {
		name string
		data string
	}{
		{name: "empty", data: ""},
		{name: "top level object", data: `{}`},
		{name: "trailing data", data: `[] true`},
		{name: "non object row", data: `[42]`},
		{name: "missing kind", data: `[{}]`},
		{name: "non string kind", data: `[{"kind":1}]`},
		{name: "missing background id", data: `[{"kind":"background","state":"working"}]`},
		{name: "missing background state", data: `[{"kind":"background","id":"job-a"}]`},
		{name: "empty id", data: `[{"kind":"background","id":"","state":"working"}]`},
		{name: "control in id", data: `[{"kind":"background","id":"job\nname","state":"working"}]`},
		{name: "unknown state", data: `[{"kind":"background","id":"job-a","state":"surprised"}]`},
		{name: "non string state", data: `[{"kind":"background","id":"job-a","state":1}]`},
		{name: "non string blocked waiting", data: `[{"kind":"background","id":"job-a","state":"blocked","waitingFor":{}}]`},
		{name: "control in blocked waiting", data: `[{"kind":"background","id":"job-a","state":"blocked","waitingFor":"permission\nplease"}]`},
		{name: "duplicate field", data: `[{"kind":"background","id":"job-a","id":"job-b","state":"working"}]`},
		{name: "duplicate job", data: `[` + valid + `,` + valid + `]`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if jobs, err := ParseClaudeBackgroundJobs([]byte(test.data)); err == nil {
				t.Fatalf("ParseClaudeBackgroundJobs() = %#v, want error", jobs)
			}
		})
	}

	tooLarge := []byte(`[{"kind":"background","id":"job-a","state":"working","extra":"` +
		strings.Repeat("x", maxClaudeBackgroundSnapshotBytes) + `"}]`)
	if _, err := ParseClaudeBackgroundJobs(tooLarge); err == nil {
		t.Fatal("ParseClaudeBackgroundJobs(oversized) error = nil")
	}

	tooMany := `[` + strings.Repeat(`{"kind":"interactive"},`, maxClaudeBackgroundRows) + `{"kind":"interactive"}]`
	if _, err := ParseClaudeBackgroundJobs([]byte(tooMany)); err == nil {
		t.Fatal("ParseClaudeBackgroundJobs(too many rows) error = nil")
	}
}

func TestParseClaudeBackgroundJobsToleratesUnknownFields(t *testing.T) {
	data := []byte(`[
		{"kind":"interactive","future":{"nested":true}},
		{"kind":"background","id":"job-a","state":"working","future":{"nested":[1,2,3]}}
	]`)
	jobs, err := ParseClaudeBackgroundJobs(data)
	if err != nil {
		t.Fatalf("ParseClaudeBackgroundJobs() error = %v", err)
	}
	if want := []ClaudeBackgroundJob{{ID: "job-a", RawState: "working", Status: ClaudeBackgroundWorking}}; !reflect.DeepEqual(jobs, want) {
		t.Fatalf("ParseClaudeBackgroundJobs() = %#v, want %#v", jobs, want)
	}
}

func TestClaudeBackgroundTrackerBaselinesInitialTerminalJobsAndAlertsBlocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude-background.json")
	tracker, err := NewClaudeBackgroundTracker(path, "/accounts/exact")
	if err != nil {
		t.Fatalf("NewClaudeBackgroundTracker() error = %v", err)
	}
	jobs := []ClaudeBackgroundJob{
		{ID: "done", RawState: "done", Status: ClaudeBackgroundCompleted},
		{ID: "failed", RawState: "failed", Status: ClaudeBackgroundFailed},
		{ID: "stopped", RawState: "stopped", Status: ClaudeBackgroundStopped},
		{ID: "working", RawState: "working", Status: ClaudeBackgroundWorking},
		{ID: "blocked", RawState: "blocked", Status: ClaudeBackgroundBlocked, WaitingFor: "question"},
	}

	events, err := tracker.Observe(jobs)
	if err != nil {
		t.Fatalf("Observe(initial) error = %v", err)
	}
	want := []ClaudeBackgroundEvent{{
		ConfigRoot: "/accounts/exact",
		JobID:      "blocked",
		Status:     ClaudeBackgroundBlocked,
		WaitingFor: "question",
	}}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("Observe(initial) = %#v, want %#v", events, want)
	}

	events, err = tracker.Observe(jobs)
	if err != nil {
		t.Fatalf("Observe(repeated) error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("Observe(repeated) = %#v, want no events", events)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat persistence: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("persistence mode = %#o, want 0600", got)
	}
}

func TestClaudeBackgroundTrackerEmitsEachNewAttentionTransitionOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude-background.json")
	tracker, err := NewClaudeBackgroundTracker(path, "/accounts/a")
	if err != nil {
		t.Fatalf("NewClaudeBackgroundTracker() error = %v", err)
	}

	assertBackgroundEvents(t, tracker, []ClaudeBackgroundJob{backgroundJob("job-a", ClaudeBackgroundWorking)}, nil)
	assertBackgroundEvents(t, tracker, []ClaudeBackgroundJob{backgroundJob("job-a", ClaudeBackgroundBlocked)}, []ClaudeBackgroundEvent{{ConfigRoot: "/accounts/a", JobID: "job-a", Status: ClaudeBackgroundBlocked}})
	assertBackgroundEvents(t, tracker, []ClaudeBackgroundJob{backgroundJob("job-a", ClaudeBackgroundBlocked)}, nil)
	assertBackgroundEvents(t, tracker, []ClaudeBackgroundJob{backgroundJob("job-a", ClaudeBackgroundCompleted)}, []ClaudeBackgroundEvent{{ConfigRoot: "/accounts/a", JobID: "job-a", Status: ClaudeBackgroundCompleted}})
	assertBackgroundEvents(t, tracker, []ClaudeBackgroundJob{backgroundJob("job-a", ClaudeBackgroundCompleted)}, nil)
	assertBackgroundEvents(t, tracker, []ClaudeBackgroundJob{backgroundJob("job-a", ClaudeBackgroundFailed)}, []ClaudeBackgroundEvent{{ConfigRoot: "/accounts/a", JobID: "job-a", Status: ClaudeBackgroundFailed}})
	assertBackgroundEvents(t, tracker, []ClaudeBackgroundJob{backgroundJob("job-a", ClaudeBackgroundStopped)}, []ClaudeBackgroundEvent{{ConfigRoot: "/accounts/a", JobID: "job-a", Status: ClaudeBackgroundStopped}})
	assertBackgroundEvents(t, tracker, []ClaudeBackgroundJob{backgroundJob("job-a", ClaudeBackgroundWorking)}, nil)
	assertBackgroundEvents(t, tracker, []ClaudeBackgroundJob{backgroundJob("job-a", ClaudeBackgroundCompleted)}, []ClaudeBackgroundEvent{{ConfigRoot: "/accounts/a", JobID: "job-a", Status: ClaudeBackgroundCompleted}})

	// A job that starts and finishes between polls is still new after the saved
	// baseline, so its first terminal observation must not be mistaken for the
	// process-start baseline.
	assertBackgroundEvents(t, tracker, []ClaudeBackgroundJob{
		backgroundJob("job-a", ClaudeBackgroundCompleted),
		backgroundJob("job-b", ClaudeBackgroundCompleted),
	}, []ClaudeBackgroundEvent{{ConfigRoot: "/accounts/a", JobID: "job-b", Status: ClaudeBackgroundCompleted}})
}

func TestClaudeBackgroundTrackerPersistsDedupeAcrossLeadershipHandoff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude-background.json")
	first := mustNewClaudeBackgroundTracker(t, path, "/accounts/a")
	assertBackgroundEvents(t, first, []ClaudeBackgroundJob{backgroundJob("job-a", ClaudeBackgroundBlocked)}, []ClaudeBackgroundEvent{{ConfigRoot: "/accounts/a", JobID: "job-a", Status: ClaudeBackgroundBlocked}})

	second := mustNewClaudeBackgroundTracker(t, path, "/accounts/a")
	assertBackgroundEvents(t, second, []ClaudeBackgroundJob{backgroundJob("job-a", ClaudeBackgroundBlocked)}, nil)
	assertBackgroundEvents(t, second, []ClaudeBackgroundJob{backgroundJob("job-a", ClaudeBackgroundWorking)}, nil)

	third := mustNewClaudeBackgroundTracker(t, path, "/accounts/a")
	assertBackgroundEvents(t, third, []ClaudeBackgroundJob{backgroundJob("job-a", ClaudeBackgroundCompleted)}, []ClaudeBackgroundEvent{{ConfigRoot: "/accounts/a", JobID: "job-a", Status: ClaudeBackgroundCompleted}})

	fourth := mustNewClaudeBackgroundTracker(t, path, "/accounts/a")
	assertBackgroundEvents(t, fourth, []ClaudeBackgroundJob{backgroundJob("job-a", ClaudeBackgroundCompleted)}, nil)
}

func TestClaudeBackgroundTrackerKeepsMissingTerminalJobInDedupeLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude-background.json")
	first := mustNewClaudeBackgroundTracker(t, path, "/accounts/a")
	terminal := []ClaudeBackgroundJob{backgroundJob("job-a", ClaudeBackgroundCompleted)}
	assertBackgroundEvents(t, first, terminal, nil)
	assertBackgroundEvents(t, first, nil, nil)

	// Leadership may transfer while Claude temporarily omits or deletes a
	// completed job. Reappearing with the same terminal state is not a new
	// transition and must remain silent.
	second := mustNewClaudeBackgroundTracker(t, path, "/accounts/a")
	assertBackgroundEvents(t, second, terminal, nil)

	stateData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := parseClaudeBackgroundPersistence(stateData)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.jobs["job-a"]; got != ClaudeBackgroundCompleted {
		t.Fatalf("persisted missing job status = %q, want %q", got, ClaudeBackgroundCompleted)
	}
}

func TestClaudeBackgroundTrackerPrunesOnlyAbsentLedgerJobsAtRowLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude-background.json")
	tracker := mustNewClaudeBackgroundTracker(t, path, "/accounts/a")
	initial := make([]ClaudeBackgroundJob, 0, maxClaudeBackgroundRows)
	for index := 0; index < maxClaudeBackgroundRows; index++ {
		initial = append(initial, backgroundJob(fmt.Sprintf("a%04d", index), ClaudeBackgroundCompleted))
	}
	assertBackgroundEvents(t, tracker, initial, nil)

	current := []ClaudeBackgroundJob{backgroundJob("z-current", ClaudeBackgroundWorking)}
	assertBackgroundEvents(t, tracker, current, nil)
	state := readClaudeBackgroundPersistenceForTest(t, path)
	if got := len(state.jobs); got != maxClaudeBackgroundRows {
		t.Fatalf("persisted jobs = %d, want row cap %d", got, maxClaudeBackgroundRows)
	}
	if got := state.jobs["z-current"]; got != ClaudeBackgroundWorking {
		t.Fatalf("current job status = %q, want %q", got, ClaudeBackgroundWorking)
	}
	if got := state.jobs["a0000"]; got != ClaudeBackgroundCompleted {
		t.Fatalf("deterministically retained absent job status = %q, want %q", got, ClaudeBackgroundCompleted)
	}
	if _, retained := state.jobs[fmt.Sprintf("a%04d", maxClaudeBackgroundRows-1)]; retained {
		t.Fatal("lexicographically last absent job retained past row cap")
	}
}

func TestClaudeBackgroundTrackerPrunesOnlyAbsentLedgerJobsAtByteLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude-background.json")
	tracker := mustNewClaudeBackgroundTracker(t, path, "/accounts/a")
	const initialCount = 3400
	initial := make([]ClaudeBackgroundJob, 0, initialCount)
	for index := 0; index < initialCount; index++ {
		initial = append(initial, backgroundJob(longClaudeBackgroundJobID("a", index), ClaudeBackgroundCompleted))
	}
	assertBackgroundEvents(t, tracker, initial, nil)

	const currentCount = 500
	current := make([]ClaudeBackgroundJob, 0, currentCount)
	for index := 0; index < currentCount; index++ {
		current = append(current, backgroundJob(longClaudeBackgroundJobID("z", index), ClaudeBackgroundWorking))
	}
	assertBackgroundEvents(t, tracker, current, nil)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > maxClaudeBackgroundPersistenceBytes {
		t.Fatalf("persistence bytes = %d, max %d", len(data), maxClaudeBackgroundPersistenceBytes)
	}
	state, err := parseClaudeBackgroundPersistence(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.jobs) <= currentCount || len(state.jobs) >= initialCount+currentCount {
		t.Fatalf("bounded persistence retained %d jobs, want all current plus a pruned subset of absent jobs", len(state.jobs))
	}
	for _, job := range current {
		if got := state.jobs[job.ID]; got != ClaudeBackgroundWorking {
			t.Fatalf("current job %q status = %q, want %q", job.ID, got, ClaudeBackgroundWorking)
		}
	}
	firstAbsent := longClaudeBackgroundJobID("a", 0)
	lastAbsent := longClaudeBackgroundJobID("a", initialCount-1)
	if got := state.jobs[firstAbsent]; got != ClaudeBackgroundCompleted {
		t.Fatalf("deterministically retained absent job status = %q, want %q", got, ClaudeBackgroundCompleted)
	}
	if _, retained := state.jobs[lastAbsent]; retained {
		t.Fatal("lexicographically last absent job retained past byte cap")
	}

	// A retained absent terminal job remains deduped across a new tracker.
	restarted := mustNewClaudeBackgroundTracker(t, path, "/accounts/a")
	assertBackgroundEvents(t, restarted, []ClaudeBackgroundJob{backgroundJob(firstAbsent, ClaudeBackgroundCompleted)}, nil)
}

func TestClaudeBackgroundTrackerKeysStateByExactConfigRoot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.json")
	rootA := mustNewClaudeBackgroundTracker(t, path, "/accounts/a")
	assertBackgroundEvents(t, rootA, []ClaudeBackgroundJob{backgroundJob("same-id", ClaudeBackgroundBlocked)}, []ClaudeBackgroundEvent{{ConfigRoot: "/accounts/a", JobID: "same-id", Status: ClaudeBackgroundBlocked}})

	if _, err := NewClaudeBackgroundTracker(path, "/accounts/A"); err == nil {
		t.Fatal("NewClaudeBackgroundTracker(different exact root) error = nil")
	}

	rootB := mustNewClaudeBackgroundTracker(t, filepath.Join(dir, "root-b.json"), "/accounts/A")
	assertBackgroundEvents(t, rootB, []ClaudeBackgroundJob{backgroundJob("same-id", ClaudeBackgroundBlocked)}, []ClaudeBackgroundEvent{{ConfigRoot: "/accounts/A", JobID: "same-id", Status: ClaudeBackgroundBlocked}})
}

func TestClaudeBackgroundTrackerObserveSnapshotKeepsParserSeparate(t *testing.T) {
	tracker := mustNewClaudeBackgroundTracker(t, filepath.Join(t.TempDir(), "state.json"), "/accounts/a")
	events, err := tracker.ObserveSnapshot([]byte(`[{"kind":"background","id":"job-a","state":"blocked","future":true}]`))
	if err != nil {
		t.Fatalf("ObserveSnapshot() error = %v", err)
	}
	want := []ClaudeBackgroundEvent{{ConfigRoot: "/accounts/a", JobID: "job-a", Status: ClaudeBackgroundBlocked}}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("ObserveSnapshot() = %#v, want %#v", events, want)
	}
}

func TestClaudeBackgroundTrackerRejectsCorruptPersistence(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		data string
	}{
		{name: "invalid JSON", data: `{`},
		{name: "duplicate field", data: `{"version":1,"version":1,"configRoot":"/accounts/a","initialized":true,"jobs":[]}`},
		{name: "unknown field", data: `{"version":1,"configRoot":"/accounts/a","initialized":true,"jobs":[],"future":true}`},
		{name: "duplicate job", data: `{"version":1,"configRoot":"/accounts/a","initialized":true,"jobs":[{"id":"job-a","status":"working"},{"id":"job-a","status":"working"}]}`},
		{name: "unknown status", data: `{"version":1,"configRoot":"/accounts/a","initialized":true,"jobs":[{"id":"job-a","status":"surprised"}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(test.name, " ", "-")+".json")
			if err := os.WriteFile(path, []byte(test.data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := NewClaudeBackgroundTracker(path, "/accounts/a"); err == nil {
				t.Fatal("NewClaudeBackgroundTracker(corrupt) error = nil")
			}
		})
	}
}

func TestClaudeBackgroundTrackerRecoveryRequiresBoundedRegularFile(t *testing.T) {
	t.Run("oversized sparse file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(maxClaudeBackgroundPersistenceBytes + 1); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}

		if _, err := NewClaudeBackgroundTracker(path, "/accounts/a"); err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("NewClaudeBackgroundTracker(oversized sparse file) error = %v, want size-limit error", err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target.json")
		link := filepath.Join(dir, "state.json")
		data := []byte(`{"version":1,"configRoot":"/accounts/a","initialized":false,"jobs":[]}`)
		if err := os.WriteFile(target, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}

		if _, err := NewClaudeBackgroundTracker(link, "/accounts/a"); err == nil {
			t.Fatal("NewClaudeBackgroundTracker(symlink) error = nil")
		}
	})

	t.Run("directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}

		if _, err := NewClaudeBackgroundTracker(path, "/accounts/a"); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("NewClaudeBackgroundTracker(directory) error = %v, want regular-file error", err)
		}
	})

	t.Run("fifo does not block", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			t.Fatal(err)
		}

		result := make(chan error, 1)
		go func() {
			_, err := NewClaudeBackgroundTracker(path, "/accounts/a")
			result <- err
		}()

		select {
		case err := <-result:
			if err == nil || !strings.Contains(err.Error(), "regular file") {
				t.Fatalf("NewClaudeBackgroundTracker(FIFO) error = %v, want regular-file error", err)
			}
		case <-time.After(2 * time.Second):
			releaseDone := make(chan struct{})
			go func() {
				_ = os.WriteFile(path, []byte(`{}`), 0o600)
				close(releaseDone)
			}()
			select {
			case <-result:
			case <-time.After(2 * time.Second):
			}
			select {
			case <-releaseDone:
			case <-time.After(2 * time.Second):
			}
			t.Fatal("NewClaudeBackgroundTracker blocked opening a FIFO")
		}
	})
}

func TestClaudeBackgroundTrackerDoesNotAdvanceAfterPersistenceFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	tracker := mustNewClaudeBackgroundTracker(t, path, "/accounts/a")
	assertBackgroundEvents(t, tracker, []ClaudeBackgroundJob{backgroundJob("job-a", ClaudeBackgroundWorking)}, nil)

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	blocked := []ClaudeBackgroundJob{backgroundJob("job-a", ClaudeBackgroundBlocked)}
	if events, err := tracker.Observe(blocked); err == nil {
		t.Fatalf("Observe(with missing persistence parent) = %#v, nil; want error", events)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	assertBackgroundEvents(t, tracker, blocked, []ClaudeBackgroundEvent{{ConfigRoot: "/accounts/a", JobID: "job-a", Status: ClaudeBackgroundBlocked}})
}

func TestClaudeBackgroundTrackerAtomicReadersNeverSeePartialState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	tracker := mustNewClaudeBackgroundTracker(t, path, "/accounts/a")
	assertBackgroundEvents(t, tracker, []ClaudeBackgroundJob{backgroundJob("job-a", ClaudeBackgroundWorking)}, nil)

	var wg sync.WaitGroup
	errCh := make(chan error, 1)
	done := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			data, err := os.ReadFile(path)
			if err != nil {
				select {
				case errCh <- err:
				default:
				}
				return
			}
			if _, err := parseClaudeBackgroundPersistence(data); err != nil {
				select {
				case errCh <- err:
				default:
				}
				return
			}
		}
	}()

	for index := 0; index < 100; index++ {
		status := ClaudeBackgroundWorking
		if index%2 == 0 {
			status = ClaudeBackgroundCompleted
		}
		if _, err := tracker.Observe([]ClaudeBackgroundJob{backgroundJob("job-a", status)}); err != nil {
			close(done)
			wg.Wait()
			t.Fatalf("Observe() error = %v", err)
		}
	}
	close(done)
	wg.Wait()
	select {
	case err := <-errCh:
		t.Fatalf("concurrent reader saw invalid persistence: %v", err)
	default:
	}
}

func TestClaudeBackgroundTrackerRejectsDuplicateOrInvalidDirectJobs(t *testing.T) {
	tracker := mustNewClaudeBackgroundTracker(t, filepath.Join(t.TempDir(), "state.json"), "/accounts/a")
	duplicate := []ClaudeBackgroundJob{
		backgroundJob("job-a", ClaudeBackgroundWorking),
		backgroundJob("job-a", ClaudeBackgroundCompleted),
	}
	if _, err := tracker.Observe(duplicate); err == nil {
		t.Fatal("Observe(duplicate jobs) error = nil")
	}
	if _, err := tracker.Observe([]ClaudeBackgroundJob{{ID: "job-a", Status: "surprised"}}); err == nil {
		t.Fatal("Observe(invalid status) error = nil")
	}
	if events, err := tracker.Observe([]ClaudeBackgroundJob{{ID: "job-a", Status: ClaudeBackgroundWorking, WaitingFor: "stale permission"}}); err != nil || len(events) != 0 {
		t.Fatalf("Observe(working with stale waiting metadata) = (%#v, %v), want no event", events, err)
	}
	events, err := tracker.Observe([]ClaudeBackgroundJob{{ID: "job-a", Status: ClaudeBackgroundCompleted, WaitingFor: "stale permission"}})
	want := []ClaudeBackgroundEvent{{ConfigRoot: "/accounts/a", JobID: "job-a", Status: ClaudeBackgroundCompleted}}
	if err != nil || !reflect.DeepEqual(events, want) {
		t.Fatalf("Observe(completed with stale waiting metadata) = (%#v, %v), want (%#v, nil)", events, err, want)
	}
}

func TestClaudeBackgroundTrackerRequiresExistingPersistenceParent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "state.json")
	if _, err := NewClaudeBackgroundTracker(path, "/accounts/a"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("NewClaudeBackgroundTracker() error = %v, want os.ErrNotExist", err)
	}
}

func backgroundJob(id string, status ClaudeBackgroundStatus) ClaudeBackgroundJob {
	return ClaudeBackgroundJob{ID: id, RawState: string(status), Status: status}
}

func longClaudeBackgroundJobID(prefix string, index int) string {
	head := fmt.Sprintf("%s%04d-", prefix, index)
	return head + strings.Repeat("x", maxClaudeBackgroundJobIDBytes-len(head))
}

func readClaudeBackgroundPersistenceForTest(t *testing.T, path string) claudeBackgroundPersistence {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := parseClaudeBackgroundPersistence(data)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func mustNewClaudeBackgroundTracker(t *testing.T, path, configRoot string) *ClaudeBackgroundTracker {
	t.Helper()
	tracker, err := NewClaudeBackgroundTracker(path, configRoot)
	if err != nil {
		t.Fatalf("NewClaudeBackgroundTracker() error = %v", err)
	}
	return tracker
}

func assertBackgroundEvents(t *testing.T, tracker *ClaudeBackgroundTracker, jobs []ClaudeBackgroundJob, want []ClaudeBackgroundEvent) {
	t.Helper()
	events, err := tracker.Observe(jobs)
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("Observe() = %#v, want %#v", events, want)
	}
}
