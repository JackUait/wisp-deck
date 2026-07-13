package attention

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	recoveryAllocationProbeBytes    = 16 * 1024 * 1024
	recoveryAllocationProbeMaxBytes = 4 * 1024 * 1024
)

func TestParseStateAcceptsCanonicalRecords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want State
	}{
		{
			name: "ready",
			text: "1\tg-1\t0\tready\t-\n",
			want: State{Generation: "g-1", Phase: PhaseReady, Reason: ReasonNone},
		},
		{
			name: "working",
			text: "1\tgeneration_2\t6\tworking\t-\n",
			want: State{Generation: "generation_2", Sequence: 6, Phase: PhaseWorking, Reason: ReasonNone},
		},
		{
			name: "question",
			text: "1\tg-3\t7\tattention\tquestion\n",
			want: State{Generation: "g-3", Sequence: 7, Phase: PhaseAttention, Reason: ReasonQuestion},
		},
		{
			name: "permission",
			text: "1\tg-4\t8\tattention\tpermission\n",
			want: State{Generation: "g-4", Sequence: 8, Phase: PhaseAttention, Reason: ReasonPermission},
		},
		{
			name: "done",
			text: "1\tg-5\t9\tattention\tdone\n",
			want: State{Generation: "g-5", Sequence: 9, Phase: PhaseAttention, Reason: ReasonDone},
		},
		{
			name: "error",
			text: "1\tg-6\t10\tattention\terror\n",
			want: State{Generation: "g-6", Sequence: 10, Phase: PhaseAttention, Reason: ReasonError},
		},
		{
			name: "unknown",
			text: "1\tg-7\t11\tunknown\t-\n",
			want: State{Generation: "g-7", Sequence: 11, Phase: PhaseUnknown, Reason: ReasonNone},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseState([]byte(tt.text))
			if err != nil {
				t.Fatalf("ParseState() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("ParseState() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseStateRejectsMalformedRecords(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"empty":                    "",
		"missing newline":          "1\tg\t1\tready\t-",
		"extra newline":            "1\tg\t1\tready\t-\n\n",
		"unsupported version":      "2\tg\t1\tready\t-\n",
		"too few fields":           "1\tg\t1\tready\n",
		"too many fields":          "1\tg\t1\tready\t-\textra\n",
		"empty generation":         "1\t\t1\tready\t-\n",
		"carriage return":          "1\tg\r\t1\tready\t-\n",
		"negative sequence":        "1\tg\t-1\tready\t-\n",
		"non-numeric sequence":     "1\tg\tone\tready\t-\n",
		"unknown phase":            "1\tg\t1\tidle\t-\n",
		"reason outside attention": "1\tg\t1\tworking\tdone\n",
		"no attention reason":      "1\tg\t1\tattention\t-\n",
		"unknown attention reason": "1\tg\t1\tattention\tretry\n",
	}

	for name, text := range tests {
		name, text := name, text
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseState([]byte(text)); err == nil {
				t.Fatalf("ParseState(%q) succeeded, want error", text)
			}
		})
	}

	t.Run("oversized", func(t *testing.T) {
		t.Parallel()
		text := "1\t" + strings.Repeat("g", MaxStateRecordBytes) + "\t1\tready\t-\n"
		if _, err := ParseState([]byte(text)); err == nil {
			t.Fatal("ParseState() accepted oversized record")
		}
	})
}

func TestStateMarshalTextRoundTrips(t *testing.T) {
	t.Parallel()

	want := State{
		Generation: "opaque generation",
		Sequence:   42,
		Phase:      PhaseAttention,
		Reason:     ReasonPermission,
	}
	b, err := want.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText() error = %v", err)
	}
	if string(b) != "1\topaque generation\t42\tattention\tpermission\n" {
		t.Fatalf("MarshalText() = %q", b)
	}
	got, err := ParseState(b)
	if err != nil {
		t.Fatalf("ParseState(MarshalText()) error = %v", err)
	}
	if got != want {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}
}

func TestStateIdentityIsInMemoryOnly(t *testing.T) {
	t.Parallel()

	state := State{
		Generation: "g-identity",
		Sequence:   3,
		Phase:      PhaseAttention,
		Reason:     ReasonQuestion,
		Identity:   "question-with-private-adapter-data",
	}
	record, err := state.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText() error = %v", err)
	}
	if got, want := string(record), "1\tg-identity\t3\tattention\tquestion\n"; got != want {
		t.Fatalf("MarshalText() = %q, want %q", got, want)
	}
	parsed, err := ParseState(record)
	if err != nil {
		t.Fatalf("ParseState() error = %v", err)
	}
	if parsed.Identity != "" {
		t.Fatalf("ParseState() Identity = %q, want empty", parsed.Identity)
	}
}

func TestStateMarshalTextRejectsInvalidState(t *testing.T) {
	t.Parallel()

	invalid := []State{
		{},
		{Generation: "bad\tgeneration", Phase: PhaseReady, Reason: ReasonNone},
		{Generation: "bad\ngeneration", Phase: PhaseReady, Reason: ReasonNone},
		{Generation: "g", Phase: Phase("idle"), Reason: ReasonNone},
		{Generation: "g", Phase: PhaseReady, Reason: ReasonDone},
		{Generation: "g", Phase: PhaseAttention, Reason: ReasonNone},
	}
	for _, state := range invalid {
		if _, err := state.MarshalText(); err == nil {
			t.Errorf("MarshalText(%#v) succeeded, want error", state)
		}
	}
}

func TestAtomicWriterPublishesModeAndDeduplicates(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	w, err := NewAtomicWriter(path, "generation-a")
	if err != nil {
		t.Fatalf("NewAtomicWriter() error = %v", err)
	}
	if got := w.Current(); got != (State{Generation: "generation-a", Phase: PhaseUnknown, Reason: ReasonNone}) {
		t.Fatalf("initial Current() = %#v", got)
	}

	mustPublish(t, w, PhaseReady, ReasonNone, "")
	assertCurrentState(t, w, State{Generation: "generation-a", Sequence: 1, Phase: PhaseReady, Reason: ReasonNone})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("state mode = %o, want 600", got)
	}

	mustPublish(t, w, PhaseReady, ReasonNone, "ignored-outside-attention")
	assertCurrentState(t, w, State{Generation: "generation-a", Sequence: 1, Phase: PhaseReady, Reason: ReasonNone})

	mustPublish(t, w, PhaseAttention, ReasonQuestion, "question-1")
	assertCurrentState(t, w, State{Generation: "generation-a", Sequence: 2, Phase: PhaseAttention, Reason: ReasonQuestion, Identity: "question-1"})
	mustPublish(t, w, PhaseAttention, ReasonQuestion, "question-1")
	assertCurrentState(t, w, State{Generation: "generation-a", Sequence: 2, Phase: PhaseAttention, Reason: ReasonQuestion, Identity: "question-1"})
	mustPublish(t, w, PhaseAttention, ReasonQuestion, "question-2")
	assertCurrentState(t, w, State{Generation: "generation-a", Sequence: 3, Phase: PhaseAttention, Reason: ReasonQuestion, Identity: "question-2"})

	mustPublish(t, w, PhaseWorking, ReasonNone, "question-2")
	assertCurrentState(t, w, State{Generation: "generation-a", Sequence: 4, Phase: PhaseWorking, Reason: ReasonNone})
}

func TestAtomicWriterResumesMatchingGeneration(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	if err := os.WriteFile(path, []byte("1\tgeneration-a\t17\tattention\tquestion\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	w, err := NewAtomicWriter(path, "generation-a")
	if err != nil {
		t.Fatalf("NewAtomicWriter() error = %v", err)
	}
	assertCurrentState(t, w, State{Generation: "generation-a", Sequence: 17, Phase: PhaseAttention, Reason: ReasonQuestion})

	// The first identity after a restart attaches to the recovered semantic
	// state without generating a duplicate alert. A later identity is new.
	mustPublish(t, w, PhaseAttention, ReasonQuestion, "question-17")
	assertCurrentState(t, w, State{Generation: "generation-a", Sequence: 17, Phase: PhaseAttention, Reason: ReasonQuestion, Identity: "question-17"})
	mustPublish(t, w, PhaseAttention, ReasonQuestion, "question-18")
	assertCurrentState(t, w, State{Generation: "generation-a", Sequence: 18, Phase: PhaseAttention, Reason: ReasonQuestion, Identity: "question-18"})
}

func TestAtomicWriterIgnoresOtherGenerationRecord(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	if err := os.WriteFile(path, []byte("1\told\t99\tattention\tdone\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := NewAtomicWriter(path, "new")
	if err != nil {
		t.Fatalf("NewAtomicWriter() error = %v", err)
	}
	if got, want := w.Current(), (State{Generation: "new", Phase: PhaseUnknown, Reason: ReasonNone}); got != want {
		t.Fatalf("Current() = %#v, want %#v", got, want)
	}
	mustPublish(t, w, PhaseReady, ReasonNone, "")
	assertCurrentState(t, w, State{Generation: "new", Sequence: 1, Phase: PhaseReady, Reason: ReasonNone})
}

func TestAtomicWriterSilentlyIgnoresInvalidRecoveryFiles(t *testing.T) {
	assertInitialUnknown := func(t *testing.T, path string) {
		t.Helper()
		writer, err := NewAtomicWriter(path, "generation-a")
		if err != nil {
			t.Fatalf("NewAtomicWriter() error = %v, want silent recovery", err)
		}
		want := State{Generation: "generation-a", Phase: PhaseUnknown, Reason: ReasonNone}
		if got := writer.Current(); got != want {
			t.Fatalf("Current() = %#v, want %#v", got, want)
		}
	}

	t.Run("oversized sparse file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state")
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(MaxStateRecordBytes + 1); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		assertInitialUnknown(t, path)
	})

	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target")
		link := filepath.Join(dir, "state")
		if err := os.WriteFile(target, []byte("1\tgeneration-a\t17\tattention\tquestion\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		assertInitialUnknown(t, link)
	})

	t.Run("directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state")
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		assertInitialUnknown(t, path)
	})

	t.Run("fifo does not block", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state")
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			t.Fatal(err)
		}

		type result struct {
			writer *AtomicWriter
			err    error
		}
		resultCh := make(chan result, 1)
		go func() {
			writer, err := NewAtomicWriter(path, "generation-a")
			resultCh <- result{writer: writer, err: err}
		}()

		select {
		case got := <-resultCh:
			if got.err != nil {
				t.Fatalf("NewAtomicWriter(FIFO) error = %v, want silent recovery", got.err)
			}
			want := State{Generation: "generation-a", Phase: PhaseUnknown, Reason: ReasonNone}
			if current := got.writer.Current(); current != want {
				t.Fatalf("Current() = %#v, want %#v", current, want)
			}
		case <-time.After(2 * time.Second):
			releaseDone := make(chan struct{})
			go func() {
				_ = os.WriteFile(path, []byte("1\tgeneration-a\t17\tattention\tquestion\n"), 0o600)
				close(releaseDone)
			}()
			select {
			case <-resultCh:
			case <-time.After(2 * time.Second):
			}
			select {
			case <-releaseDone:
			case <-time.After(2 * time.Second):
			}
			t.Fatal("NewAtomicWriter blocked opening a FIFO")
		}
	})
}

func TestAtomicWriterDoesNotSilenceOpenErrorAfterPathReplacement(t *testing.T) {
	dir := t.TempDir()
	regularWitness := filepath.Join(dir, "regular")
	if err := os.WriteFile(regularWitness, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	regularInfo, err := os.Stat(regularWitness)
	if err != nil {
		t.Fatal(err)
	}
	nonRegularInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}

	openAttempted := false
	openFlags := 0
	ops := boundedRegularFileOps{
		lstat: func(string) (os.FileInfo, error) {
			if openAttempted {
				return nonRegularInfo, nil
			}
			return regularInfo, nil
		},
		open: func(_ string, flags int, _ uint32) (int, error) {
			openAttempted = true
			openFlags = flags
			return -1, syscall.EACCES
		},
	}
	readRecovery := func(path, label string, maxBytes int64) ([]byte, error) {
		return readBoundedRegularFileWithOps(path, label, maxBytes, ops)
	}

	_, err = newAtomicWriter(filepath.Join(dir, "state"), "generation-a", readRecovery)
	if !errors.Is(err, syscall.EACCES) {
		t.Fatalf("NewAtomicWriter(open EACCES followed by path replacement) error = %v, want EACCES", err)
	}
	if openFlags&syscall.O_CLOEXEC == 0 {
		t.Fatal("recovery open flags omit O_CLOEXEC")
	}
}

func TestRecoveryReadersRejectOversizedSparseFileBeforeAllocating(t *testing.T) {
	if mode := os.Getenv("WISP_DECK_RECOVERY_ALLOCATION_PROBE"); mode != "" {
		path := os.Getenv("WISP_DECK_RECOVERY_ALLOCATION_PATH")
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)

		switch mode {
		case "attention-state":
			writer, err := NewAtomicWriter(path, "generation-a")
			if err != nil {
				t.Fatalf("NewAtomicWriter(oversized sparse file) error = %v, want silent recovery", err)
			}
			want := State{Generation: "generation-a", Phase: PhaseUnknown, Reason: ReasonNone}
			if got := writer.Current(); got != want {
				t.Fatalf("Current() = %#v, want %#v", got, want)
			}
		case "claude-background":
			if _, err := NewClaudeBackgroundTracker(path, "/accounts/a"); err == nil {
				t.Fatal("NewClaudeBackgroundTracker(oversized sparse file) error = nil")
			}
		default:
			t.Fatalf("unknown recovery allocation probe mode %q", mode)
		}

		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		allocated := after.TotalAlloc - before.TotalAlloc
		if allocated > recoveryAllocationProbeMaxBytes {
			t.Fatalf("recovery allocated %d bytes, want at most %d before size rejection", allocated, recoveryAllocationProbeMaxBytes)
		}
		return
	}

	for _, mode := range []string{"attention-state", "claude-background"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "recovery")
			file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			if err := file.Truncate(recoveryAllocationProbeBytes); err != nil {
				_ = file.Close()
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}

			command := exec.Command(
				os.Args[0],
				"-test.run=^TestRecoveryReadersRejectOversizedSparseFileBeforeAllocating$",
				"-test.count=1",
			)
			command.Env = append(
				os.Environ(),
				"WISP_DECK_RECOVERY_ALLOCATION_PROBE="+mode,
				"WISP_DECK_RECOVERY_ALLOCATION_PATH="+path,
			)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("recovery allocation subprocess: %v\n%s", err, output)
			}
		})
	}
}

func TestAtomicWriterNeverCreatesMissingParent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	parent := filepath.Join(root, "gone")
	path := filepath.Join(parent, "state")
	if _, err := NewAtomicWriter(path, "generation-a"); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("NewAtomicWriter() error = %v, want ErrStaleGeneration", err)
	}
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Fatalf("missing parent was created: %v", err)
	}

	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	w, err := NewAtomicWriter(path, "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(parent); err != nil {
		t.Fatal(err)
	}
	if err := w.Publish(PhaseReady, ReasonNone, ""); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("Publish() error = %v, want ErrStaleGeneration", err)
	}
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Fatalf("Publish recreated removed parent: %v", err)
	}

	dedupeParent := filepath.Join(root, "dedupe-gone")
	if err := os.Mkdir(dedupeParent, 0o700); err != nil {
		t.Fatal(err)
	}
	dedupePath := filepath.Join(dedupeParent, "state")
	dedupeWriter, err := NewAtomicWriter(dedupePath, "generation-b")
	if err != nil {
		t.Fatal(err)
	}
	mustPublish(t, dedupeWriter, PhaseReady, ReasonNone, "")
	if err := os.RemoveAll(dedupeParent); err != nil {
		t.Fatal(err)
	}
	if err := dedupeWriter.Publish(PhaseReady, ReasonNone, ""); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("deduplicated Publish() error = %v, want ErrStaleGeneration", err)
	}
	if _, err := os.Stat(dedupeParent); !os.IsNotExist(err) {
		t.Fatalf("deduplicated Publish recreated removed parent: %v", err)
	}

	recoveredParent := filepath.Join(root, "recovered-gone")
	if err := os.Mkdir(recoveredParent, 0o700); err != nil {
		t.Fatal(err)
	}
	recoveredPath := filepath.Join(recoveredParent, "state")
	if err := os.WriteFile(recoveredPath, []byte("1\tgeneration-c\t7\tattention\tquestion\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recoveredWriter, err := NewAtomicWriter(recoveredPath, "generation-c")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(recoveredParent); err != nil {
		t.Fatal(err)
	}
	if err := recoveredWriter.Publish(PhaseAttention, ReasonQuestion, "question-7"); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("recovered-identity Publish() error = %v, want ErrStaleGeneration", err)
	}
	if _, err := os.Stat(recoveredParent); !os.IsNotExist(err) {
		t.Fatalf("recovered-identity Publish recreated removed parent: %v", err)
	}
}

func TestAtomicWriterConcurrentReadersNeverSeePartialRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	w, err := NewAtomicWriter(path, "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	mustPublish(t, w, PhaseReady, ReasonNone, "")

	var readers sync.WaitGroup
	errCh := make(chan error, 8)
	stop := make(chan struct{})
	for range 8 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				b, err := os.ReadFile(path)
				if err != nil {
					errCh <- err
					return
				}
				if _, err := ParseState(b); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}

	for i := 0; i < 200; i++ {
		if i%2 == 0 {
			mustPublish(t, w, PhaseWorking, ReasonNone, "")
		} else {
			mustPublish(t, w, PhaseAttention, ReasonQuestion, "question-"+strconv.Itoa(i))
		}
	}
	close(stop)
	readers.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent reader observed invalid state: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, ".attention-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files leaked: %v", matches)
	}
}

func mustPublish(t *testing.T, w *AtomicWriter, phase Phase, reason Reason, identity string) {
	t.Helper()
	if err := w.Publish(phase, reason, identity); err != nil {
		t.Fatalf("Publish(%q, %q, %q) error = %v", phase, reason, identity, err)
	}
}

func assertCurrentState(t *testing.T, w *AtomicWriter, want State) {
	t.Helper()
	if got := w.Current(); got != want {
		t.Fatalf("Current() = %#v, want %#v", got, want)
	}
	b, err := os.ReadFile(w.path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	got, err := ParseState(b)
	if err != nil {
		t.Fatalf("ParseState(on disk) error = %v", err)
	}
	onDiskWant := want
	onDiskWant.Identity = ""
	if got != onDiskWant {
		t.Fatalf("on-disk state = %#v, want %#v", got, onDiskWant)
	}
}
