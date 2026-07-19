package ledger

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeRunner struct {
	mu      sync.Mutex
	results map[string]fakeRunResult
	calls   []string
}

type fakeRunResult struct {
	out []byte
	err error
}

type fakeInputRunner struct {
	*fakeRunner
	muInput sync.Mutex
	inputs  [][]byte
	args    [][]string
	output  []byte
}

func (r *fakeInputRunner) RunInput(_ context.Context, _ string, input []byte, args ...string) ([]byte, error) {
	r.muInput.Lock()
	defer r.muInput.Unlock()
	r.inputs = append(r.inputs, append([]byte(nil), input...))
	r.args = append(r.args, append([]string(nil), args...))
	return append([]byte(nil), r.output...), nil
}

func (r *fakeRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	r.mu.Lock()
	r.calls = append(r.calls, key)
	result, ok := r.results[key]
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unexpected git command: %s", key)
	}
	return result.out, result.err
}

func sourceRunner(staged, unstaged, untracked string) *fakeRunner {
	return &fakeRunner{results: map[string]fakeRunResult{
		"diff --cached --numstat -z --find-renames": {
			out: []byte(staged),
		},
		"diff --numstat -z --find-renames": {
			out: []byte(unstaged),
		},
		"ls-files --others --exclude-standard -z": {
			out: []byte(untracked),
		},
		"symbolic-ref --short -q HEAD": {
			out: []byte("feature/native-ledger\n"),
		},
		"rev-list --left-right --count HEAD...@{u}": {
			out: []byte("3\t2\n"),
		},
	}}
}

func TestSourceLoadsAllGroupsIntoOneSnapshot(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("one\ntwo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "image.png"), []byte{0, 1, 2, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	runner := sourceRunner(
		"4\t2\tstaged.go\x00",
		"1\t1\tmodified.go\x00",
		"new.txt\x00image.png\x00",
	)
	source := NewSource(runner, WithWorkers(2))

	snapshot, err := source.Load(context.Background(), repo, 42)

	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Generation != 42 {
		t.Fatalf("generation = %d, want 42", snapshot.Generation)
	}
	if got, want := snapshot.Metadata.TotalFiles, 4; got != want {
		t.Fatalf("total files = %d, want %d", got, want)
	}
	if got, want := snapshot.Metadata.Added, 7; got != want {
		t.Fatalf("added = %d, want %d", got, want)
	}
	if got, want := snapshot.Metadata.Deleted, 3; got != want {
		t.Fatalf("deleted = %d, want %d", got, want)
	}
	if snapshot.Metadata.Branch != "feature/native-ledger" || snapshot.Metadata.Ahead != 3 || snapshot.Metadata.Behind != 2 {
		t.Fatalf("branch metadata = %#v", snapshot.Metadata)
	}
	paths := map[string]Row{}
	for _, row := range snapshot.Rows {
		if row.Kind == RowFile {
			paths[row.Path] = row
		}
	}
	if len(paths) != 4 {
		t.Fatalf("file rows = %#v", paths)
	}
	var groups []Group
	for _, row := range snapshot.Rows {
		if row.Kind == RowGroup {
			groups = append(groups, row.Group)
		}
	}
	if got, want := fmt.Sprint(groups), fmt.Sprint([]Group{GroupStaged, GroupModified, GroupNew}); got != want {
		t.Fatalf("group row identities = %s, want %s", got, want)
	}
	if got := paths["new.txt"].Added; got != 2 {
		t.Fatalf("new.txt additions = %d, want 2", got)
	}
	image := paths["image.png"]
	if !image.Binary || image.NewBytes != 4 {
		t.Fatalf("image row = %#v, want binary with 4 new bytes", image)
	}
}

type blockingRunner struct {
	started chan struct{}
}

func (r blockingRunner) Run(ctx context.Context, _ string, _ ...string) ([]byte, error) {
	select {
	case r.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestSourceCancellationStopsLoad(t *testing.T) {
	runner := blockingRunner{started: make(chan struct{}, 5)}
	source := NewSource(runner, WithWorkers(4))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := source.Load(ctx, t.TempDir(), 7)
		done <- err
	}()

	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("Git runner never started")
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Load error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Load did not stop after cancellation")
	}
}

func TestSourceBoundsUntrackedInspectionConcurrency(t *testing.T) {
	const workers = 3
	var paths strings.Builder
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&paths, "file-%d.txt%c", i, byte(0))
	}
	runner := sourceRunner("", "", paths.String())
	started := make(chan struct{}, 12)
	release := make(chan struct{})
	var active atomic.Int32
	var maximum atomic.Int32
	inspector := InspectFunc(func(ctx context.Context, _ string, path string) (Change, error) {
		current := active.Add(1)
		for {
			old := maximum.Load()
			if current <= old || maximum.CompareAndSwap(old, current) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return Change{}, ctx.Err()
		}
		active.Add(-1)
		return Change{Group: GroupNew, Path: path, Added: 1}, nil
	})
	source := NewSource(runner, WithWorkers(workers), WithInspector(inspector))
	done := make(chan error, 1)
	go func() {
		_, err := source.Load(context.Background(), t.TempDir(), 1)
		done <- err
	}()

	for i := 0; i < workers; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("only %d inspectors started", i)
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := maximum.Load(); got != workers {
		t.Fatalf("maximum inspectors = %d, want %d", got, workers)
	}
}

func TestSourceFileInspectorMatchesLedgerCounting(t *testing.T) {
	repo := t.TempDir()
	tests := []struct {
		name string
		data []byte
		want Change
	}{
		{
			name: "trailing-newline.txt",
			data: []byte("one\ntwo\n"),
			want: Change{Group: GroupNew, Path: "trailing-newline.txt", Added: 2},
		},
		{
			name: "missing-newline.txt",
			data: []byte("one\ntwo"),
			want: Change{Group: GroupNew, Path: "missing-newline.txt", Added: 2},
		},
		{
			name: "empty.txt",
			data: nil,
			want: Change{Group: GroupNew, Path: "empty.txt"},
		},
		{
			name: "binary.png",
			data: []byte{1, 0, 2, 3},
			want: Change{Group: GroupNew, Path: "binary.png", Binary: true, NewBytes: 4},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(repo, tt.name), tt.data, 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := inspectWorktreeFile(context.Background(), repo, tt.name)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("change = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestSourceBatchesTrackedBinarySizes(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "work.png"), []byte{0, 1, 2, 3, 4, 5, 6, 7}, 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeInputRunner{
		fakeRunner: sourceRunner(
			"-\t-\tstage.png\x00",
			"-\t-\twork.png\x00",
			"",
		),
		output: []byte("10\x0014\x0020\x00"),
	}
	source := NewSource(runner)

	snapshot, err := source.Load(context.Background(), repo, 1)

	if err != nil {
		t.Fatal(err)
	}
	if len(runner.inputs) != 1 {
		t.Fatalf("cat-file batches = %d, want 1", len(runner.inputs))
	}
	wantInput := []byte("HEAD:stage.png\x00:stage.png\x00:work.png\x00")
	if string(runner.inputs[0]) != string(wantInput) {
		t.Fatalf("cat-file input = %q, want %q", runner.inputs[0], wantInput)
	}
	if got := strings.Join(runner.args[0], " "); got != "cat-file --batch-check=%(objectsize) -Z" {
		t.Fatalf("cat-file args = %q", got)
	}
	rows := map[string]Row{}
	for _, row := range snapshot.Rows {
		if row.Kind == RowFile {
			rows[row.Path] = row
		}
	}
	if got := rows["stage.png"]; got.OldBytes != 10 || got.NewBytes != 14 {
		t.Fatalf("staged image sizes = %#v, want 10 -> 14", got)
	}
	if got := rows["work.png"]; got.OldBytes != 20 || got.NewBytes != 8 {
		t.Fatalf("modified image sizes = %#v, want 20 -> 8", got)
	}
}
