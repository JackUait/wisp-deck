package tui

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func TestParseCloneProgress_placesEveryGitPhaseOnOneRisingScale(t *testing.T) {
	tests := []struct {
		name string
		line string
		want float64
		ok   bool
	}{
		{"counting start", "remote: Counting objects:   0% (1/12408)", 0.00, true},
		{"counting half", "remote: Counting objects:  50% (6204/12408)", 0.025, true},
		{"compressing done", "remote: Compressing objects: 100% (500/500), done.", 0.10, true},
		{"receiving mid", "Receiving objects:  50% (6204/12408)", 0.45, true},
		{"receiving with rate", "Receiving objects:  45% (555/1234), 1.20 MiB | 2.00 MiB/s", 0.415, true},
		{"resolving done", "Resolving deltas: 100% (8338/8338), done.", 0.95, true},
		{"updating files", "Updating files:  50% (30/60)", 0.975, true},
		{"checking out files", "Checking out files:  50% (30/60)", 0.975, true},
		{"enumerating has no percentage", "remote: Enumerating objects: 12408, done.", 0, false},
		{"banner", "Cloning into 'ct5'...", 0, false},
		{"total line", "remote: Total 12408 (delta 3), reused 12408 (delta 3), pack-reused 0", 0, false},
		{"fatal", "fatal: repository 'https://github.com/x/y.git/' not found", 0, false},
		{"empty", "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseCloneProgress(tt.line)
			if ok != tt.ok {
				t.Fatalf("parseCloneProgress(%q) ok = %v, want %v", tt.line, ok, tt.ok)
			}
			if ok && math.Abs(got-tt.want) > 0.0005 {
				t.Errorf("parseCloneProgress(%q) = %.4f, want %.4f", tt.line, got, tt.want)
			}
		})
	}
}

// A phase boundary must never step backwards: the bar is one scale, so the end
// of each phase is the start of the next.
func TestParseCloneProgress_neverStepsBackwardsBetweenPhases(t *testing.T) {
	sequence := []string{
		"remote: Counting objects:   0% (1/100)",
		"remote: Counting objects: 100% (100/100), done.",
		"remote: Compressing objects:   0% (1/50)",
		"remote: Compressing objects: 100% (50/50), done.",
		"Receiving objects:   0% (1/100)",
		"Receiving objects: 100% (100/100), done.",
		"Resolving deltas:   0% (0/30)",
		"Resolving deltas: 100% (30/30), done.",
		"Updating files: 100% (60/60), done.",
	}
	prev := -1.0
	for _, line := range sequence {
		got, ok := parseCloneProgress(line)
		if !ok {
			t.Fatalf("parseCloneProgress(%q) should report progress", line)
		}
		if got < prev {
			t.Errorf("progress went backwards at %q: %.3f after %.3f", line, got, prev)
		}
		prev = got
	}
	if prev != 1 {
		t.Errorf("the last phase should end the bar at 1, got %.3f", prev)
	}
}

func TestCloneProgress_holdsItsHighWaterMark(t *testing.T) {
	var p cloneProgress
	p.set(0.5)
	p.set(0.2)
	if got := p.fraction(); got != 0.5 {
		t.Errorf("a late lower reading must not rewind the bar: got %.2f, want 0.50", got)
	}
	p.set(1.5)
	if got := p.fraction(); got != 1 {
		t.Errorf("fraction must clamp to 1, got %.2f", got)
	}
}

func TestCloneProgress_isSafeToWriteWhileTheModelReads(t *testing.T) {
	var p cloneProgress
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			p.set(float64(i) / 500)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			_ = p.fraction()
		}
	}()
	wg.Wait()
}

// initGitRepo builds a throwaway repo with one commit to clone from.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"add", "README.md"},
		{"commit", "-qm", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = src
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return src
}

func TestRenderCloneStatus_fitsAnOrdinarySlugAndTheBarInTheBrowserCard(t *testing.T) {
	plain := lipgloss.NewStyle()
	for _, slug := range []string{"owner/my-repo", "dodopizza/tvboards"} {
		line := stripAnsi(renderCloneStatus(plain, slug, 0.45, browserCardInnerWidth))
		if !strings.Contains(line, "Cloning "+slug) {
			t.Errorf("an ordinary slug must not be truncated in the 46-cell slot, got %q", line)
		}
		if !strings.Contains(line, "45%") {
			t.Errorf("the status line must carry the percentage, got %q", line)
		}
		if w := cellWidth(line); w > browserCardInnerWidth {
			t.Errorf("status line overflows the card: %d cells > %d in %q", w, browserCardInnerWidth, line)
		}
	}
}

// A slot too narrow for both keeps the bar whole and truncates the label: the
// bar is the part the user is watching.
func TestRenderCloneStatus_truncatesTheLabelNotTheBar(t *testing.T) {
	plain := lipgloss.NewStyle()
	line := stripAnsi(renderCloneStatus(plain, "a-very-long-org-name/a-very-long-repo-name", 1, browserCardInnerWidth))
	if !strings.Contains(line, strings.Repeat("\u2588", cloneBarWidth(browserCardInnerWidth))) {
		t.Errorf("the full bar must survive a narrow slot, got %q", line)
	}
	if !strings.Contains(line, "100%") {
		t.Errorf("the percentage must survive a narrow slot, got %q", line)
	}
	if w := cellWidth(line); w > browserCardInnerWidth {
		t.Errorf("status line overflows the card: %d cells > %d in %q", w, browserCardInnerWidth, line)
	}
}

func TestDefaultGitClone_reportsRealProgressAndReachesTheEnd(t *testing.T) {
	src := initGitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")

	var mu sync.Mutex
	var seen []float64
	// file:// rather than a bare path: a plain local clone hardlinks the object
	// store and reports no phases at all, which would test nothing.
	err := defaultGitClone("file://"+src, dest, func(f float64) {
		mu.Lock()
		seen = append(seen, f)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("clone of a local repo should succeed: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "README.md")); statErr != nil {
		t.Fatalf("clone should have produced the worktree: %v", statErr)
	}
	if len(seen) == 0 {
		t.Fatal("a real git clone must report progress, not just finish silently")
	}
	mu.Lock()
	defer mu.Unlock()
	prev := -1.0
	for _, f := range seen {
		if f < 0 || f > 1 {
			t.Fatalf("progress must stay inside the bar, got %v in %v", f, seen)
		}
		prev = f
	}
	if prev <= 0 {
		t.Errorf("the last reading should be well into the bar, got %v", seen)
	}
}

func TestDefaultGitClone_keepsGitsOwnErrorText(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "clone")
	err := defaultGitClone(filepath.Join(t.TempDir(), "no-such-repo"), dest, func(float64) {})
	if err == nil {
		t.Fatal("cloning a nonexistent repo should fail")
	}
	if !strings.Contains(err.Error(), "repository") {
		t.Errorf("the error must carry git's own message, not just an exit status, got: %v", err)
	}
}

// A line the scanner cannot tokenize stops the read loop, and git then blocks
// writing into a pipe nobody drains — a clone that hangs forever behind a
// frozen form. The bar's own lines are short; the sideband's are not ours.
func TestDefaultGitClone_doesNotWedgeOnStderrItCannotTokenize(t *testing.T) {
	binDir := t.TempDir()
	script := "#!/bin/sh\nhead -c 1000000 /dev/zero | tr '\\0' x >&2\necho 'fatal: boom' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(binDir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	done := make(chan error, 1)
	go func() { done <- defaultGitClone("file:///nope", filepath.Join(t.TempDir(), "clone"), nil) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a git that exits 1 must surface an error")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("defaultGitClone wedged: git is blocked writing stderr nobody is reading")
	}
}
