package attention

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	registryRootStart  = "Mon Jul 13 09:00:00 2026"
	registryChildStart = "Mon Jul 13 09:00:01 2026"
)

func TestPSSnapshotterRunsLocaleStableUTCSnapshot(t *testing.T) {
	t.Parallel()

	var calls int
	var gotName string
	var gotArgs, gotEnv []string
	runner := CommandOutputFunc(func(_ context.Context, name string, args, env []string) ([]byte, error) {
		calls++
		gotName = name
		gotArgs = append([]string(nil), args...)
		gotEnv = append([]string(nil), env...)
		return []byte("  100     1 Mon Jul 13 09:00:00 2026\n"), nil
	})

	got, err := (PSSnapshotter{Run: runner}).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("runner calls = %d, want 1", calls)
	}
	if gotName != "/bin/ps" {
		t.Fatalf("command = %q, want /bin/ps", gotName)
	}
	if want := []string{"-axo", "pid=,ppid=,lstart="}; !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("args = %#v, want %#v", gotArgs, want)
	}
	if want := []string{"LC_ALL=C", "TZ=UTC"}; !reflect.DeepEqual(gotEnv, want) {
		t.Fatalf("env = %#v, want %#v", gotEnv, want)
	}
	if string(got) != "  100     1 Mon Jul 13 09:00:00 2026\n" {
		t.Fatalf("Snapshot() = %q", got)
	}
}

func TestPSSnapshotterPropagatesRunnerError(t *testing.T) {
	t.Parallel()

	want := errors.New("ps unavailable")
	runner := CommandOutputFunc(func(context.Context, string, []string, []string) ([]byte, error) {
		return nil, want
	})
	_, err := (PSSnapshotter{Run: runner}).Snapshot(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("Snapshot() error = %v, want wrapped %v", err, want)
	}
}

func TestClaudeRegistryMapperAcceptsCurrentRegistrySchema(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	writeRegistryRecord(t, configDir, 101, `{
		"pid": 101,
		"sessionId": "session-current",
		"cwd": "/tmp/project",
		"startedAt": 1783926000000,
		"procStart": "Mon Jul 13 09:00:01 2026",
		"version": "2.1.173",
		"kind": "interactive",
		"status": "idle",
		"updatedAt": 1783926000123
	}`)

	mapper := registryMapper(configDir, psSnapshot(
		psProcess(100, 1, registryRootStart),
		psProcess(101, 100, registryChildStart),
	))
	got, found, err := mapper.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if !found {
		t.Fatal("Poll() found = false, want true")
	}
	want := ClaudeRegistryStatus{
		PID:            101,
		Status:         "idle",
		StatusIdentity: "1783926000123",
	}
	if got != want {
		t.Fatalf("Poll() = %#v, want %#v", got, want)
	}
}

func TestClaudeRegistryMapperAcceptsLaunchRootAfterShellExec(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	writeRegistryRecord(t, configDir, 100, `{"pid":100,"kind":"interactive","procStart":"`+registryRootStart+`","status":"busy","updatedAt":41}`)
	mapper := registryMapper(configDir, psSnapshot(
		psProcess(100, 1, registryRootStart),
	))

	got, found, err := mapper.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if !found || got.PID != 100 || got.Status != "busy" {
		t.Fatalf("Poll() = (%#v, %v), want exec-replaced root PID 100 busy", got, found)
	}
}

func TestClaudeRegistryMapperMapsKnownStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     string
		waitingFor string
	}{
		{name: "idle", status: "idle"},
		{name: "busy", status: "busy"},
		{name: "waiting without reason", status: "waiting"},
		{name: "waiting with reason", status: "waiting", waitingFor: "permission prompt"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			configDir := t.TempDir()
			extra := ""
			if tt.waitingFor != "" {
				extra = `,"waitingFor":"` + tt.waitingFor + `"`
			}
			writeRegistryRecord(t, configDir, 101, `{"pid":101,"kind":"interactive","procStart":"`+registryChildStart+`","status":"`+tt.status+`","updatedAt":42`+extra+`}`)

			mapper := registryMapper(configDir, psSnapshot(
				psProcess(100, 1, registryRootStart),
				psProcess(101, 100, registryChildStart),
			))
			got, found, err := mapper.Poll(context.Background())
			if err != nil {
				t.Fatalf("Poll() error = %v", err)
			}
			if !found {
				t.Fatal("Poll() found = false, want true")
			}
			want := ClaudeRegistryStatus{PID: 101, Status: tt.status, StatusIdentity: "42", WaitingFor: tt.waitingFor}
			if got != want {
				t.Fatalf("Poll() = %#v, want %#v", got, want)
			}
		})
	}
}

func TestClaudeRegistryMapperFindsSessionThroughLaunchChain(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	const claudeStart = "Mon Jul 13 09:00:04 2026"
	writeRegistryRecord(t, configDir, 400, `{"pid":400,"kind":"interactive","procStart":"`+claudeStart+`","status":"busy","updatedAt":43}`)
	mapper := registryMapper(configDir, psSnapshot(
		psProcess(100, 1, registryRootStart),
		psProcess(200, 100, "Mon Jul 13 09:00:02 2026"), // shell
		psProcess(300, 200, "Mon Jul 13 09:00:03 2026"), // screenshot filter
		psProcess(400, 300, claudeStart),                // Claude
	))

	got, found, err := mapper.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if !found || got.PID != 400 || got.Status != "busy" {
		t.Fatalf("Poll() = (%#v, %v), want PID 400 busy", got, found)
	}
}

func TestClaudeRegistryMapperKeepsInteractiveForegroundAuthoritativeOverSubagent(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	const subagentStart = "Mon Jul 13 09:00:02 2026"
	const interactiveStart = "Mon Jul 13 09:00:03 2026"
	writeRegistryRecord(t, configDir, 101, `{"pid":101,"kind":"subagent","procStart":"`+subagentStart+`","status":"idle","updatedAt":90}`)
	writeRegistryRecord(t, configDir, 102, `{"pid":102,"kind":"interactive","procStart":"`+interactiveStart+`","status":"busy","updatedAt":80}`)
	mapper := registryMapper(configDir, psSnapshot(
		psProcess(100, 1, registryRootStart),
		psProcess(101, 100, subagentStart),
		psProcess(102, 101, interactiveStart),
	))

	got, found, err := mapper.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	want := ClaudeRegistryStatus{
		PID:            102,
		Status:         "busy",
		StatusIdentity: "80",
	}
	if !found || got != want {
		t.Fatalf("Poll() = (%#v, %v), want interactive foreground %#v; subagent idle must not complete the turn", got, found, want)
	}
}

func TestClaudeRegistryMapperRejectsSymlinkedSessionRecord(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	sessionsDir := filepath.Join(configDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(configDir, "attacker-controlled.json")
	record := `{"pid":101,"kind":"interactive","procStart":"` + registryChildStart + `","status":"idle","updatedAt":91}`
	if err := os.WriteFile(target, []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(sessionsDir, "101.json")); err != nil {
		t.Fatal(err)
	}
	mapper := registryMapper(configDir, psSnapshot(
		psProcess(100, 1, registryRootStart),
		psProcess(101, 100, registryChildStart),
	))

	got, found, err := mapper.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if found {
		t.Fatalf("Poll() = (%#v, true), want symlinked registry record rejected", got)
	}
}

func TestReadClaudeRegistryFileAcceptsRegularFileAtSizeLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record.json")
	want := make([]byte, maxClaudeRegistryRecordBytes)
	want[0] = '{'
	want[len(want)-1] = '}'
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readClaudeRegistryFile(path)
	if err != nil {
		t.Fatalf("readClaudeRegistryFile: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("read %d bytes, want exact %d-byte regular file", len(got), len(want))
	}
}

func TestReadClaudeRegistryFileRejectsOversizedSparseFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record.json")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxClaudeRegistryRecordBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := readClaudeRegistryFile(path); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("readClaudeRegistryFile error = %v, want pre-read size-limit rejection", err)
	}
}

func TestReadClaudeRegistryFileRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	link := filepath.Join(dir, "record.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if _, err := readClaudeRegistryFile(link); err == nil {
		t.Fatal("readClaudeRegistryFile followed a symlink")
	}
}

func TestReadClaudeRegistryFileRejectsFIFOBeforeBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record.json")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	type readResult struct {
		data []byte
		err  error
	}
	done := make(chan readResult, 1)
	go func() {
		data, err := readClaudeRegistryFile(path)
		done <- readResult{data: data, err: err}
	}()

	select {
	case result := <-done:
		if result.err == nil {
			t.Fatalf("readClaudeRegistryFile read FIFO data %q, want rejection", result.data)
		}
	case <-time.After(time.Second):
		// Unblock an unsafe os.ReadFile implementation so the failed regression
		// cannot leave a goroutine behind in the package test process.
		if fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = syscall.Close(fd)
		}
		t.Fatal("readClaudeRegistryFile blocked opening a FIFO")
	}
}

func TestClaudeRegistryMapperPreservesUTCStartSpacing(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	const paddedStart = "Mon Jul  6 09:00:01 2026"
	writeRegistryRecord(t, configDir, 101, `{"pid":101,"kind":"interactive","procStart":"`+paddedStart+`","status":"idle","updatedAt":44}`)
	mapper := registryMapper(configDir, psSnapshot(
		psProcess(100, 1, "Mon Jul  6 09:00:00 2026"),
		psProcess(101, 100, paddedStart),
	))

	got, found, err := mapper.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if !found || got.PID != 101 {
		t.Fatalf("Poll() = (%#v, %v), want PID 101", got, found)
	}
}

func TestClaudeRegistryMapperRejectsUntrustedRecords(t *testing.T) {
	t.Parallel()

	valid := `{"pid":101,"kind":"interactive","procStart":"` + registryChildStart + `","status":"idle","updatedAt":45}`
	tests := []struct {
		name      string
		recordPID int
		record    string
		processes []string
	}{
		{name: "filename pid differs from record", recordPID: 101, record: strings.Replace(valid, `"pid":101`, `"pid":102`, 1)},
		{name: "pid reused", recordPID: 101, record: strings.Replace(valid, registryChildStart, "Mon Jul 13 08:59:59 2026", 1)},
		{name: "background kind", recordPID: 101, record: strings.Replace(valid, `"interactive"`, `"bg"`, 1)},
		{name: "daemon kind", recordPID: 101, record: strings.Replace(valid, `"interactive"`, `"daemon"`, 1)},
		{name: "corrupt json", recordPID: 101, record: `{"pid":101`},
		{name: "trailing json", recordPID: 101, record: valid + `{}`},
		{name: "duplicate pid", recordPID: 101, record: strings.Replace(valid, `"pid":101`, `"pid":101,"pid":101`, 1)},
		{name: "missing pid", recordPID: 101, record: strings.Replace(valid, `"pid":101,`, ``, 1)},
		{name: "string pid", recordPID: 101, record: strings.Replace(valid, `"pid":101`, `"pid":"101"`, 1)},
		{name: "missing kind", recordPID: 101, record: strings.Replace(valid, `"kind":"interactive",`, ``, 1)},
		{name: "missing process start", recordPID: 101, record: strings.Replace(valid, `"procStart":"`+registryChildStart+`",`, ``, 1)},
		{name: "numeric process start", recordPID: 101, record: strings.Replace(valid, `"procStart":"`+registryChildStart+`"`, `"procStart":123`, 1)},
		{name: "missing status", recordPID: 101, record: strings.Replace(valid, `"status":"idle",`, ``, 1)},
		{name: "unknown status", recordPID: 101, record: strings.Replace(valid, `"status":"idle"`, `"status":"shell"`, 1)},
		{name: "missing update marker", recordPID: 101, record: strings.Replace(valid, `,"updatedAt":45`, ``, 1)},
		{name: "string update marker", recordPID: 101, record: strings.Replace(valid, `"updatedAt":45`, `"updatedAt":"45"`, 1)},
		{name: "negative update marker", recordPID: 101, record: strings.Replace(valid, `"updatedAt":45`, `"updatedAt":-1`, 1)},
		{name: "fractional update marker", recordPID: 101, record: strings.Replace(valid, `"updatedAt":45`, `"updatedAt":45.1`, 1)},
		{name: "exponent update marker", recordPID: 101, record: strings.Replace(valid, `"updatedAt":45`, `"updatedAt":45e0`, 1)},
		{name: "non-string waiting reason", recordPID: 101, record: strings.Replace(valid, `}`, `,"waitingFor":7}`, 1)},
		{
			name:      "not a descendant",
			recordPID: 201,
			record:    strings.Replace(strings.Replace(valid, `"pid":101`, `"pid":201`, 1), registryChildStart, "Mon Jul 13 09:00:02 2026", 1),
			processes: []string{psProcess(100, 1, registryRootStart), psProcess(101, 100, registryChildStart), psProcess(201, 1, "Mon Jul 13 09:00:02 2026")},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			configDir := t.TempDir()
			writeRegistryRecord(t, configDir, tt.recordPID, tt.record)
			processes := tt.processes
			if processes == nil {
				processes = []string{psProcess(100, 1, registryRootStart), psProcess(101, 100, registryChildStart)}
			}
			mapper := registryMapper(configDir, psSnapshot(processes...))

			got, found, err := mapper.Poll(context.Background())
			if err != nil {
				t.Fatalf("Poll() error = %v", err)
			}
			if found {
				t.Fatalf("Poll() = (%#v, true), want no match", got)
			}
		})
	}
}

func TestClaudeRegistryMapperChoosesUniqueShallowestSession(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	writeRegistryRecord(t, configDir, 101, `{"pid":101,"kind":"interactive","procStart":"Mon Jul 13 09:00:01 2026","status":"idle","updatedAt":46}`)
	writeRegistryRecord(t, configDir, 103, `{"pid":103,"kind":"interactive","procStart":"Mon Jul 13 09:00:03 2026","status":"waiting","updatedAt":47}`)
	mapper := registryMapper(configDir, psSnapshot(
		psProcess(100, 1, registryRootStart),
		psProcess(101, 100, "Mon Jul 13 09:00:01 2026"),
		psProcess(102, 101, "Mon Jul 13 09:00:02 2026"),
		psProcess(103, 102, "Mon Jul 13 09:00:03 2026"),
	))

	got, found, err := mapper.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if !found || got.PID != 101 || got.Status != "idle" {
		t.Fatalf("Poll() = (%#v, %v), want shallow PID 101", got, found)
	}
}

func TestClaudeRegistryMapperRejectsEqualDepthAmbiguity(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	writeRegistryRecord(t, configDir, 101, `{"pid":101,"kind":"interactive","procStart":"Mon Jul 13 09:00:01 2026","status":"idle","updatedAt":48}`)
	writeRegistryRecord(t, configDir, 102, `{"pid":102,"kind":"interactive","procStart":"Mon Jul 13 09:00:02 2026","status":"busy","updatedAt":49}`)
	mapper := registryMapper(configDir, psSnapshot(
		psProcess(100, 1, registryRootStart),
		psProcess(101, 100, "Mon Jul 13 09:00:01 2026"),
		psProcess(102, 100, "Mon Jul 13 09:00:02 2026"),
	))

	got, found, err := mapper.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if found {
		t.Fatalf("Poll() = (%#v, true), want ambiguity to fail closed", got)
	}
}

func TestClaudeRegistryMapperObservesProcessDisappearance(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	writeRegistryRecord(t, configDir, 101, `{"pid":101,"kind":"interactive","procStart":"`+registryChildStart+`","status":"busy","updatedAt":50}`)
	snapshots := [][]byte{
		[]byte(psSnapshot(psProcess(100, 1, registryRootStart), psProcess(101, 100, registryChildStart))),
		[]byte(psSnapshot(psProcess(100, 1, registryRootStart))),
	}
	var calls int
	mapper := registryMapper(configDir, "")
	mapper.Snapshot = ProcessSnapshotFunc(func(context.Context) ([]byte, error) {
		got := snapshots[calls]
		calls++
		return got, nil
	})

	if _, found, err := mapper.Poll(context.Background()); err != nil || !found {
		t.Fatalf("first Poll() found = %v, error = %v; want match", found, err)
	}
	if got, found, err := mapper.Poll(context.Background()); err != nil || found {
		t.Fatalf("second Poll() = (%#v, %v, %v), want no match", got, found, err)
	}
	if calls != 2 {
		t.Fatalf("snapshot calls = %d, want one per poll", calls)
	}
}

func TestClaudeRegistryMapperReadsOnlyExactLaunchTreeFiles(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	var snapshotCalls int
	var paths []string
	mapper := ClaudeRegistryMapper{
		ConfigDir:     configDir,
		LaunchRootPID: 100,
		Snapshot: ProcessSnapshotFunc(func(context.Context) ([]byte, error) {
			snapshotCalls++
			return []byte(psSnapshot(
				psProcess(100, 1, registryRootStart),
				psProcess(102, 100, "Mon Jul 13 09:00:02 2026"),
				psProcess(101, 100, registryChildStart),
				psProcess(999, 1, "Mon Jul 13 09:00:09 2026"),
			)), nil
		}),
		ReadFile: ReadFileFunc(func(path string) ([]byte, error) {
			paths = append(paths, path)
			return nil, os.ErrNotExist
		}),
	}
	if _, found, err := mapper.Poll(context.Background()); err != nil || found {
		t.Fatalf("Poll() found = %v, error = %v; want no match", found, err)
	}
	if snapshotCalls != 1 {
		t.Fatalf("snapshot calls = %d, want 1", snapshotCalls)
	}
	sort.Strings(paths)
	want := []string{
		filepath.Join(configDir, "sessions", "100.json"),
		filepath.Join(configDir, "sessions", "101.json"),
		filepath.Join(configDir, "sessions", "102.json"),
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("ReadFile paths = %#v, want %#v", paths, want)
	}
}

func TestClaudeRegistryMapperFailsClosedOnMalformedSnapshot(t *testing.T) {
	t.Parallel()

	mapper := registryMapper(t.TempDir(), "100 1 definitely-not-an-lstart\n")
	if got, found, err := mapper.Poll(context.Background()); err == nil || found {
		t.Fatalf("Poll() = (%#v, %v, %v), want snapshot parse error", got, found, err)
	}
}

func registryMapper(configDir, snapshot string) ClaudeRegistryMapper {
	return ClaudeRegistryMapper{
		ConfigDir:     configDir,
		LaunchRootPID: 100,
		Snapshot: ProcessSnapshotFunc(func(context.Context) ([]byte, error) {
			return []byte(snapshot), nil
		}),
	}
}

func writeRegistryRecord(t *testing.T, configDir string, pid int, record string) {
	t.Helper()
	dir := filepath.Join(configDir, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(pid)+".json"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
}

func psSnapshot(processes ...string) string {
	return strings.Join(processes, "")
}

func psProcess(pid, ppid int, start string) string {
	return " " + strconv.Itoa(pid) + " " + strconv.Itoa(ppid) + " " + start + "\n"
}
