package bash_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The clean-tree ledger used to print a bare " no changes" in the pane's top-left
// corner — a lone grey label floating over an otherwise empty pane. It now draws
// a small, muted wisp, centered, with a caption beneath it. This is the shell
// fallback renderer; it mirrors the Go ledger's placeholder (internal/tui).

// Half-block pixels pack two rows per cell, so the 14x12 pixel wisp occupies 14
// columns and 6 rows.
const (
	mascotCols = 14
	mascotRows = 6
)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

// emptyStateLines returns the plain-text form of the whole placeholder, plus
// just the lines carrying mascot pixels (whole or half blocks).
func emptyStateLines(t *testing.T, width, rows string) (all []string, art []string) {
	t.Helper()
	out, code := runBashFunc(t, "lib/compact-view.sh", "ledger_empty_state",
		[]string{width, rows}, nil)
	assertExitCode(t, code, 0)
	all = strings.Split(strings.TrimRight(stripANSI(out), "\n"), "\n")
	for _, line := range all {
		if strings.ContainsAny(line, "█▀▄") {
			art = append(art, line)
		}
	}
	return all, art
}

func TestLedgerEmptyState_draws_a_mascot_and_caption(t *testing.T) {
	all, art := emptyStateLines(t, "60", "30")
	if len(art) != mascotRows {
		t.Fatalf("expected a %d-row mascot, got %d:\n%s",
			mascotRows, len(art), strings.Join(all, "\n"))
	}
	if !strings.Contains(strings.Join(all, "\n"), "working tree clean") {
		t.Errorf("expected a caption under the mascot:\n%s", strings.Join(all, "\n"))
	}
	if strings.Contains(strings.Join(all, "\n"), "no changes") {
		t.Errorf("the bare \"no changes\" label must be gone:\n%s", strings.Join(all, "\n"))
	}
}

// The mascot is centered horizontally, not flush against the pane's left edge.
func TestLedgerEmptyState_centers_the_mascot_horizontally(t *testing.T) {
	const width = 60
	all, art := emptyStateLines(t, "60", "30")
	minLead := width
	for _, line := range art {
		lead := len([]rune(line)) - len([]rune(strings.TrimLeft(line, " ")))
		if lead < minLead {
			minLead = lead
		}
		if got := len([]rune(strings.TrimRight(line, " "))); got > width {
			t.Errorf("art line overflows pane width %d (%d cols): %q", width, got, line)
		}
	}
	if want := (width - mascotCols) / 2; minLead != want {
		t.Errorf("mascot left edge at column %d, want %d (centered):\n%s",
			minLead, want, strings.Join(all, "\n"))
	}
	// The caption is centered too.
	for _, line := range all {
		if !strings.Contains(line, "working tree clean") {
			continue
		}
		lead := len(line) - len(strings.TrimLeft(line, " "))
		if want := (width - len("working tree clean")) / 2; lead != want {
			t.Errorf("caption at column %d, want %d", lead, want)
		}
	}
}

// The placeholder is centered vertically inside the body viewport and never
// taller than it — an overflow would push the bottom bar off the pane.
func TestLedgerEmptyState_centers_vertically_within_the_viewport(t *testing.T) {
	all, _ := emptyStateLines(t, "60", "30")
	if len(all) > 30 {
		t.Fatalf("placeholder is %d rows, viewport is 30", len(all))
	}
	blanks := 0
	for _, line := range all {
		if strings.TrimSpace(line) != "" {
			break
		}
		blanks++
	}
	if blanks < 3 {
		t.Errorf("expected the art pushed down toward the middle, got %d leading blank rows", blanks)
	}
}

// The mascot is painted in the session's theme, so an OpenCode pane's wisp is
// violet rather than the claude orange — the shape itself is shared.
func TestLedgerEmptyState_paints_the_mascot_in_the_theme(t *testing.T) {
	orange, _ := runBashFunc(t, "lib/compact-view.sh", "ledger_empty_state",
		[]string{"60", "30", "orange"}, nil)
	purple, _ := runBashFunc(t, "lib/compact-view.sh", "ledger_empty_state",
		[]string{"60", "30", "purple"}, nil)

	// The MUTED stops (SleepDim), never the full-saturation body colors the
	// splash mascot uses — the placeholder should recede, not draw the eye.
	if !strings.Contains(orange, "38;5;130m") {
		t.Errorf("orange mascot missing its muted body hue (130):\n%q", orange)
	}
	if !strings.Contains(purple, "38;5;60m") {
		t.Errorf("purple mascot missing its muted body hue (60):\n%q", purple)
	}
	for _, loud := range []string{"209", "220", "231"} {
		if strings.Contains(orange, "38;5;"+loud+"m") {
			t.Errorf("mascot uses the loud color %s; expected the sleep palette:\n%q", loud, orange)
		}
	}
	if stripANSI(orange) != stripANSI(purple) {
		t.Error("theme changed the mascot shape; it should only change its colors")
	}
}

// End to end: a clean working tree renders the mascot placeholder in the pane,
// under the ZSH the pane actually runs — not the old " no changes" label.
func TestCompactView_clean_tree_renders_the_mascot_placeholder(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not available")
	}
	module := filepath.Join(projectRoot(t), "lib", "compact-view.sh")

	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	writeTempFile(t, dir, "a.txt", "one\n")
	git("add", "a.txt")
	git("commit", "-q", "-m", "init") // clean tree -> the placeholder

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, zsh, "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=0.1", "TERM=xterm")
	out, _ := cmd.CombinedOutput()

	if !strings.ContainsAny(string(out), "█▀▄") {
		t.Errorf("clean tree should draw the mascot:\n%q", string(out))
	}
	if !strings.Contains(string(out), "working tree clean") {
		t.Errorf("clean tree should caption the mascot:\n%q", string(out))
	}
	if strings.Contains(string(out), "no changes") {
		t.Errorf("the bare \"no changes\" label must be gone:\n%q", string(out))
	}
}

// A pane too narrow or too short for the art degrades to the caption alone
// rather than drawing a wrapped, mangled mascot.
func TestLedgerEmptyState_falls_back_to_the_caption_when_it_cannot_fit(t *testing.T) {
	for _, tc := range []struct{ name, width, rows string }{
		{"narrow", "12", "30"},
		{"short", "60", "6"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			all, art := emptyStateLines(t, tc.width, tc.rows)
			if len(art) != 0 {
				t.Errorf("expected no mascot in a %s pane:\n%s", tc.name, strings.Join(all, "\n"))
			}
			if !strings.Contains(strings.Join(all, "\n"), "working tree clean") {
				t.Errorf("expected the caption to survive:\n%s", strings.Join(all, "\n"))
			}
		})
	}
}
