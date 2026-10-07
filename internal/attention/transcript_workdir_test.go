package attention

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// checkout makes a directory git would call a checkout root: a main checkout
// holds a .git directory, a linked worktree a .git file.
func checkout(t *testing.T, dir string, linked bool) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if linked {
		if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	} else if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func transcriptLine(cwd string, sidechain bool) string {
	side := "false"
	if sidechain {
		side = "true"
	}
	return `{"type":"user","isSidechain":` + side + `,"cwd":"` + cwd + `","message":{"role":"user","content":"x"}}` + "\n"
}

func appendTranscript(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

type transcriptFixture struct {
	configDir, main, worktree, transcript string
	status                                ClaudeRegistryStatus
}

func newTranscriptFixture(t *testing.T) transcriptFixture {
	t.Helper()
	root := t.TempDir()
	main := checkout(t, filepath.Join(root, "repo"), false)
	worktree := checkout(t, filepath.Join(main, ".claude", "worktrees", "feature"), true)
	configDir := filepath.Join(root, "config")
	return transcriptFixture{
		configDir:  configDir,
		main:       main,
		worktree:   worktree,
		transcript: filepath.Join(configDir, "projects", claudeProjectDirName(main), "sid-1.jsonl"),
		status:     ClaudeRegistryStatus{SessionID: "sid-1", Cwd: main},
	}
}

// Claude Code moves its working directory when a Bash `cd` lands in another
// checkout, and records that only in the transcript: the registry record keeps
// the launch directory, so following the record alone left the tab behind.
func TestTranscriptWorkdirFollowsACdIntoAWorktree(t *testing.T) {
	t.Parallel()
	fx := newTranscriptFixture(t)
	tracker := &TranscriptWorkdir{ConfigDir: fx.configDir}

	appendTranscript(t, fx.transcript, transcriptLine(fx.main, false))
	if got := tracker.Resolve(fx.status); got != fx.main {
		t.Fatalf("before the cd: %q, want %q", got, fx.main)
	}
	appendTranscript(t, fx.transcript, transcriptLine(fx.worktree, false))
	if got := tracker.Resolve(fx.status); got != fx.worktree {
		t.Fatalf("after the cd: %q, want %q", got, fx.worktree)
	}
	appendTranscript(t, fx.transcript, transcriptLine(fx.main, false))
	if got := tracker.Resolve(fx.status); got != fx.main {
		t.Fatalf("after leaving: %q, want %q", got, fx.main)
	}
}

// The tab tracks checkouts, not directories: a cd into a subdirectory is
// reported as the checkout that holds it.
func TestTranscriptWorkdirReportsTheCheckoutRootOfASubdirectory(t *testing.T) {
	t.Parallel()
	fx := newTranscriptFixture(t)
	sub := filepath.Join(fx.worktree, "src", "frontend")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	tracker := &TranscriptWorkdir{ConfigDir: fx.configDir}

	appendTranscript(t, fx.transcript, transcriptLine(sub, false))
	if got := tracker.Resolve(fx.status); got != fx.worktree {
		t.Fatalf("got %q, want the worktree root %q", got, fx.worktree)
	}
	mainSub := filepath.Join(fx.main, "lib")
	if err := os.MkdirAll(mainSub, 0o755); err != nil {
		t.Fatal(err)
	}
	appendTranscript(t, fx.transcript, transcriptLine(mainSub, false))
	if got := tracker.Resolve(fx.status); got != fx.main {
		t.Fatalf("got %q, want the main root %q", got, fx.main)
	}
}

// A subagent's worktree must never drag the tab.
func TestTranscriptWorkdirIgnoresSidechainEntries(t *testing.T) {
	t.Parallel()
	fx := newTranscriptFixture(t)
	tracker := &TranscriptWorkdir{ConfigDir: fx.configDir}

	appendTranscript(t, fx.transcript, transcriptLine(fx.main, false)+transcriptLine(fx.worktree, true))
	if got := tracker.Resolve(fx.status); got != fx.main {
		t.Fatalf("got %q, want %q", got, fx.main)
	}
}

func TestTranscriptWorkdirFallsBackToTheRecord(t *testing.T) {
	t.Parallel()
	fx := newTranscriptFixture(t)
	tracker := &TranscriptWorkdir{ConfigDir: fx.configDir}

	status := fx.status
	status.Cwd = fx.worktree
	if got := tracker.Resolve(status); got != fx.worktree {
		t.Fatalf("no transcript: %q, want the record's %q", got, fx.worktree)
	}
	if got := tracker.Resolve(ClaudeRegistryStatus{Cwd: fx.main}); got != fx.main {
		t.Fatalf("no session id: %q, want the record's %q", got, fx.main)
	}
}

// Claude writes a line in more than one write; half a line is not an entry yet.
func TestTranscriptWorkdirWaitsForACompleteLine(t *testing.T) {
	t.Parallel()
	fx := newTranscriptFixture(t)
	tracker := &TranscriptWorkdir{ConfigDir: fx.configDir}

	appendTranscript(t, fx.transcript, transcriptLine(fx.main, false))
	line := transcriptLine(fx.worktree, false)
	appendTranscript(t, fx.transcript, line[:20])
	if got := tracker.Resolve(fx.status); got != fx.main {
		t.Fatalf("half a line: %q, want %q", got, fx.main)
	}
	appendTranscript(t, fx.transcript, line[20:])
	if got := tracker.Resolve(fx.status); got != fx.worktree {
		t.Fatalf("whole line: %q, want %q", got, fx.worktree)
	}
}

// /clear and /resume hand the session another transcript, which may live under
// another project's directory.
func TestTranscriptWorkdirFollowsTheConversationToItsTranscript(t *testing.T) {
	t.Parallel()
	fx := newTranscriptFixture(t)
	tracker := &TranscriptWorkdir{ConfigDir: fx.configDir}

	appendTranscript(t, fx.transcript, transcriptLine(fx.worktree, false))
	if got := tracker.Resolve(fx.status); got != fx.worktree {
		t.Fatalf("first conversation: %q", got)
	}
	other := filepath.Join(fx.configDir, "projects", "-somewhere-else", "sid-2.jsonl")
	appendTranscript(t, other, transcriptLine(fx.main, false))
	status := fx.status
	status.SessionID = "sid-2"
	if got := tracker.Resolve(status); got != fx.main {
		t.Fatalf("resumed conversation: %q, want %q", got, fx.main)
	}
}

// A resumed conversation can be tens of megabytes. The first read takes the
// tail, and an unchanged transcript is not read again.
func TestTranscriptWorkdirReadsOnlyWhatIsNew(t *testing.T) {
	t.Parallel()
	fx := newTranscriptFixture(t)
	reads := 0
	tracker := &TranscriptWorkdir{ConfigDir: fx.configDir, onRead: func(n int) { reads += n }}

	filler := `{"type":"assistant","message":{"content":"` + strings.Repeat("y", 1000) + `"}}` + "\n"
	appendTranscript(t, fx.transcript, transcriptLine(fx.main, false)+strings.Repeat(filler, 4*transcriptTailBytes/1000)+transcriptLine(fx.worktree, false))
	if got := tracker.Resolve(fx.status); got != fx.worktree {
		t.Fatalf("got %q, want %q", got, fx.worktree)
	}
	if reads > transcriptTailBytes {
		t.Fatalf("first read took %d bytes, want at most the %d-byte tail", reads, transcriptTailBytes)
	}
	reads = 0
	for range 5 {
		tracker.Resolve(fx.status)
	}
	if reads != 0 {
		t.Fatalf("unchanged transcript read %d bytes", reads)
	}
	appendTranscript(t, fx.transcript, transcriptLine(fx.main, false))
	if got := tracker.Resolve(fx.status); got != fx.main {
		t.Fatalf("got %q, want %q", got, fx.main)
	}
	if want := len(transcriptLine(fx.main, false)); reads != want {
		t.Fatalf("append read %d bytes, want only the new %d", reads, want)
	}
}
