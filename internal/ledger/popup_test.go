package ledger

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

type processCall struct {
	name string
	args []string
}

type recordingProcessRunner struct {
	mu    sync.Mutex
	calls []processCall
	run   func(context.Context, string, ...string) ([]byte, error)
}

func (r *recordingProcessRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, processCall{name: name, args: append([]string(nil), args...)})
	r.mu.Unlock()
	if r.run != nil {
		return r.run(ctx, name, args...)
	}
	return nil, nil
}

func (r *recordingProcessRunner) snapshotCalls() []processCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]processCall(nil), r.calls...)
}

func popupEnv(args []string, name string) (string, bool) {
	prefix := name + "="
	for index := 0; index+1 < len(args); index++ {
		if args[index] == "-e" && strings.HasPrefix(args[index+1], prefix) {
			return strings.TrimPrefix(args[index+1], prefix), true
		}
	}
	return "", false
}

func TestPopupKeepsMetadataOutOfShellProgram(t *testing.T) {
	runner := &recordingProcessRunner{}
	popup := NewExecPopup(runner, t.TempDir())
	request := OpenRequest{
		ProjectDir:   "/tmp/repo; touch owned",
		Path:         "src/a file'$(false).go",
		Tool:         "open code; false",
		BackdropFile: "/tmp/back drop",
		Tracked:      true,
	}

	if _, err := popup.Open(context.Background(), request); err != nil {
		t.Fatal(err)
	}

	calls := runner.snapshotCalls()
	if len(calls) != 1 || calls[0].name != "tmux" {
		t.Fatalf("process calls = %#v", calls)
	}
	args := calls[0].args
	program := args[len(args)-1]
	for _, metadata := range []string{request.ProjectDir, request.Path, request.Tool, request.BackdropFile} {
		if strings.Contains(program, metadata) {
			t.Fatalf("user metadata %q was interpolated into shell program %q", metadata, program)
		}
	}
	for name, want := range map[string]string{
		"WISP_LEDGER_PROJECT":  request.ProjectDir,
		"WISP_LEDGER_PATH":     request.Path,
		"WISP_LEDGER_TOOL":     request.Tool,
		"WISP_LEDGER_BACKDROP": request.BackdropFile,
		"WISP_LEDGER_TRACKED":  "1",
	} {
		if got, ok := popupEnv(args, name); !ok || got != want {
			t.Errorf("popup env %s = %q, %v; want %q", name, got, ok, want)
		}
	}
}

func TestPopupImageRequestCarriesPreviewFlagsAndGraphicsTTY(t *testing.T) {
	runner := &recordingProcessRunner{run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "display-message" {
			return []byte("/dev/ttys009\n"), nil
		}
		return nil, nil
	}}
	popup := NewExecPopup(runner, t.TempDir())

	if _, err := popup.Open(context.Background(), OpenRequest{
		ProjectDir: "/repo", Path: "art/shot.png", Tool: "claude",
		Image: true, Status: "added",
	}); err != nil {
		t.Fatal(err)
	}

	var display processCall
	for _, call := range runner.snapshotCalls() {
		if len(call.args) > 0 && call.args[0] == "display-popup" {
			display = call
		}
	}
	for name, want := range map[string]string{
		"WISP_LEDGER_IMAGE":   "1",
		"WISP_LEDGER_STATUS":  "added",
		"WISP_LEDGER_GFX_TTY": "/dev/ttys009",
	} {
		if got, ok := popupEnv(display.args, name); !ok || got != want {
			t.Errorf("popup env %s = %q, %v; want %q", name, got, ok, want)
		}
	}
	program := display.args[len(display.args)-1]
	for _, flag := range []string{"--image", "--status", "--gfx-tty", "--path"} {
		if !strings.Contains(program, flag) {
			t.Errorf("image popup program missing %s: %q", flag, program)
		}
	}
}

func TestPopupReadsDiscardDecisionAndCleansTemporaryFile(t *testing.T) {
	var decisionPath string
	runner := &recordingProcessRunner{run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "display-popup" {
			decisionPath, _ = popupEnv(args, "WISP_LEDGER_DECISION")
			if err := os.WriteFile(decisionPath, []byte("discard"), 0o600); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}}
	popup := NewExecPopup(runner, t.TempDir())

	result, err := popup.Open(context.Background(), OpenRequest{ProjectDir: "/repo", Path: "a.go", Tracked: true})

	if err != nil {
		t.Fatal(err)
	}
	if !result.Discard {
		t.Fatal("discard decision was not returned")
	}
	if decisionPath == "" {
		t.Fatal("popup did not provide a decision file")
	}
	if _, err := os.Stat(decisionPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("decision file was not cleaned: %v", err)
	}
}

func TestPopupCancellationCleansDecisionFile(t *testing.T) {
	var decisionPath string
	runner := &recordingProcessRunner{run: func(ctx context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "display-popup" {
			decisionPath, _ = popupEnv(args, "WISP_LEDGER_DECISION")
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return nil, nil
	}}
	popup := NewExecPopup(runner, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := popup.Open(ctx, OpenRequest{ProjectDir: "/repo", Path: "a.go"})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("popup error = %v, want context canceled", err)
	}
	if decisionPath != "" {
		if _, statErr := os.Stat(decisionPath); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("cancelled decision file was not cleaned: %v", statErr)
		}
	}
}

func TestPopupBackdropCacheAtomicallyReplacesOneSnapshot(t *testing.T) {
	target := filepath.Join(t.TempDir(), "ledger-backdrop")
	capture := "first"
	runner := &recordingProcessRunner{run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch args[0] {
		case "display-message":
			return []byte("80 24\n"), nil
		case "list-panes":
			return []byte("%1 0 0\n%2 40 0\n"), nil
		case "capture-pane":
			return []byte(capture + " " + args[len(args)-1] + "\n"), nil
		default:
			return nil, errors.New("unexpected tmux command")
		}
	}}
	cache := NewFileBackdropCache(runner, target)

	if _, ok := cache.Latest(); ok {
		t.Fatal("empty cache reported a snapshot")
	}
	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	firstPath, ok := cache.Latest()
	if !ok || firstPath != target {
		t.Fatalf("first cache path = %q, %v", firstPath, ok)
	}
	first, err := os.ReadFile(firstPath)
	if err != nil || !strings.Contains(string(first), "first %1") {
		t.Fatalf("first backdrop = %q, %v", first, err)
	}

	capture = "second"
	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	secondPath, _ := cache.Latest()
	if secondPath != firstPath {
		t.Fatalf("cache created an unbounded path: first=%q second=%q", firstPath, secondPath)
	}
	second, err := os.ReadFile(secondPath)
	if err != nil || !strings.Contains(string(second), "second %2") || strings.Contains(string(second), "first") {
		t.Fatalf("second backdrop = %q, %v", second, err)
	}
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("cache left temporary files: %v", entries)
	}
	if names := []string{entries[0].Name()}; !slices.Equal(names, []string{filepath.Base(target)}) {
		t.Fatalf("cache left temporary files: %v", entries)
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Close did not remove backdrop: %v", err)
	}
}
