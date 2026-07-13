package attention

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
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
	assertCurrentState(t, w, State{Generation: "generation-a", Sequence: 2, Phase: PhaseAttention, Reason: ReasonQuestion})
	mustPublish(t, w, PhaseAttention, ReasonQuestion, "question-1")
	assertCurrentState(t, w, State{Generation: "generation-a", Sequence: 2, Phase: PhaseAttention, Reason: ReasonQuestion})
	mustPublish(t, w, PhaseAttention, ReasonQuestion, "question-2")
	assertCurrentState(t, w, State{Generation: "generation-a", Sequence: 3, Phase: PhaseAttention, Reason: ReasonQuestion})

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
	assertCurrentState(t, w, State{Generation: "generation-a", Sequence: 17, Phase: PhaseAttention, Reason: ReasonQuestion})
	mustPublish(t, w, PhaseAttention, ReasonQuestion, "question-18")
	assertCurrentState(t, w, State{Generation: "generation-a", Sequence: 18, Phase: PhaseAttention, Reason: ReasonQuestion})
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
	if got != want {
		t.Fatalf("on-disk state = %#v, want %#v", got, want)
	}
}
