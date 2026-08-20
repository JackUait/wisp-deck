package tui

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

// cloneProgressSpans map each phase of `git clone --progress` onto its slice of
// one rising bar, in the order git runs them. A bar that restarted at every
// phase would read as broken, so each span begins where the previous one ends.
// The widths are rough shares of a real clone's wall clock: receiving the pack
// dominates, the remote-side counting and compressing are quick.
var cloneProgressSpans = []struct {
	phase      string
	start, end float64
}{
	{"Counting objects", 0.00, 0.05},
	{"Compressing objects", 0.05, 0.10},
	{"Receiving objects", 0.10, 0.80},
	{"Resolving deltas", 0.80, 0.95},
	// git 2.27 renamed the checkout phase; both names map to the same span.
	{"Checking out files", 0.95, 1.00},
	{"Updating files", 0.95, 1.00},
}

// cloneProgressLine matches a phase line's percentage. The `remote: ` prefix is
// optional because the remote-side phases carry it and the local ones do not,
// and the trailing text (counts, transfer rate, `done.`) is deliberately
// unanchored so a rate suffix does not stop the line from parsing.
var cloneProgressLine = regexp.MustCompile(`^(?:remote: )?([A-Za-z][A-Za-z ]*[A-Za-z]):\s+(\d{1,3})%`)

// parseCloneProgress reads one line of git's progress output and returns how
// far through the whole clone it is. ok is false for every line that carries no
// percentage — banners, `Enumerating objects`, sideband chatter, errors.
func parseCloneProgress(line string) (float64, bool) {
	m := cloneProgressLine.FindStringSubmatch(line)
	if m == nil {
		return 0, false
	}
	pct, err := strconv.Atoi(m[2])
	if err != nil {
		return 0, false
	}
	if pct > 100 {
		pct = 100
	}
	for _, span := range cloneProgressSpans {
		if span.phase == m[1] {
			return span.start + (span.end-span.start)*float64(pct)/100, true
		}
	}
	return 0, false
}

// cloneProgress is the clone goroutine's hand-off to the render tick: the
// streaming reader writes it, the model reads it on every cloneTickMsg. It only
// ever moves forward, so a phase git skips (or a line that arrives out of
// order) can never rewind a bar the user has already watched fill.
type cloneProgress struct {
	mu  sync.Mutex
	pct float64
}

func (p *cloneProgress) set(f float64) {
	if f > 1 {
		f = 1
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if f > p.pct {
		p.pct = f
	}
}

func (p *cloneProgress) fraction() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pct
}

// splitCloneProgress splits on \r as well as \n: git rewrites its progress line
// in place with a carriage return, so a plain line scanner sees nothing at all
// until the clone exits.
func splitCloneProgress(data []byte, atEOF bool) (int, []byte, error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// cloneErrorTailLines is how many non-progress lines are kept to report a
// failure. Streaming replaced a CombinedOutput that returned git's whole
// message, and `clone failed: exit status 128` tells the user nothing.
const cloneErrorTailLines = 12

// defaultGitClone runs a real `git clone`, reporting progress as it goes.
func defaultGitClone(url, dest string, onProgress func(float64)) error {
	cmd := exec.Command("git", "clone", "--progress", "--", url, dest)
	// git translates its phase names, and parseCloneProgress matches the C ones:
	// without this a non-English user's bar would sit at 0% for the whole clone.
	cmd.Env = append(os.Environ(), "LC_ALL=C")

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	var tail []string
	scanner := bufio.NewScanner(stderr)
	scanner.Split(splitCloneProgress)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if f, ok := parseCloneProgress(line); ok {
			if onProgress != nil {
				onProgress(f)
			}
			continue
		}
		tail = append(tail, line)
		if len(tail) > cloneErrorTailLines {
			tail = tail[1:]
		}
	}

	// A line the scanner cannot tokenize stops the loop early, and git then
	// blocks writing into a pipe nobody drains — a clone that hangs forever
	// behind a frozen form. After a clean EOF this is a no-op.
	_, _ = io.Copy(io.Discard, stderr)

	if err := cmd.Wait(); err != nil {
		if msg := strings.Join(tail, "\n"); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
}

// cloneBarWidth sizes the bar for the slot it is drawn into. The label gets
// first claim on the space — the browser card's slot is 46 cells, and a bar any
// wider than a quarter of it starts eating ordinary owner/repo slugs.
// renderProgressBar appends 5 more cells of " NNN%".
func cloneBarWidth(width int) int {
	w := width / 4
	if w > 24 {
		w = 24
	}
	if w < 10 {
		w = 10
	}
	return w
}

// renderCloneStatus draws the in-flight clone as a label plus a real progress
// bar. The label is what gets truncated when the slot is narrow: chopping the
// line as a whole would eat the bar's right edge, which is the part the user is
// watching.
func renderCloneStatus(labelStyle lipgloss.Style, slug string, pct float64, width int) string {
	bar := cloneBarWidth(width)
	labelWidth := width - bar - 6
	if labelWidth < 4 {
		labelWidth = 4
	}
	return labelStyle.Render(TruncateMiddle("Cloning "+slug, labelWidth)) + " " + renderProgressBar(pct, bar)
}
