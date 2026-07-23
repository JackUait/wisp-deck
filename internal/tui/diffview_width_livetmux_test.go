package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// measureScript prints each line of $1 into the pane it runs in, asks the
// terminal where the cursor ended up (CSI 6n), and writes the resulting cell
// count to $2. That answer comes from tmux itself, so it is the ground truth
// the whole width model is fitted to.
const measureScript = `
in="$1"; out="$2"
: > "$out"
stty raw -echo </dev/tty
while IFS= read -r line <&3; do
  printf '\r\033[K%s\033[6n' "$line" > /dev/tty
  IFS= read -r -d R -t 5 reply < /dev/tty
  col="${reply##*;}"
  printf '%s\n' "$((col - 1))" >> "$out"
done 3< "$in"
stty sane </dev/tty
printf 'DONE\n' >> "$out"
`

// TestCellWidth_matches_a_live_tmux re-derives the recorded corpus from a real
// tmux and fails on any drift. It is opt-in because it needs a tmux binary and
// spends a few seconds driving one:
//
//	WISP_DECK_TMUX_WIDTH_E2E=1 go test ./internal/tui/ -run TestCellWidth_matches_a_live_tmux
//
// Run it after a tmux upgrade, after bumping go-runewidth or uniseg, or when
// adding a script to testdata/tmux_widths.json — it is what says whether the
// recording still describes the terminal wisp-deck actually draws into.
func TestCellWidth_matches_a_live_tmux(t *testing.T) {
	if os.Getenv("WISP_DECK_TMUX_WIDTH_E2E") != "1" {
		t.Skip("set WISP_DECK_TMUX_WIDTH_E2E=1 to check cellWidth against a live tmux")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	cases := loadWidthCorpus(t)

	dir := t.TempDir()
	inPath := filepath.Join(dir, "in.txt")
	outPath := filepath.Join(dir, "out.txt")
	scriptPath := filepath.Join(dir, "measure.sh")
	var in strings.Builder
	for _, c := range cases {
		in.WriteString(c.S)
		in.WriteByte('\n')
	}
	if err := os.WriteFile(inPath, []byte(in.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte(measureScript), 0o755); err != nil {
		t.Fatal(err)
	}

	socket := "wisp-width-" + strconv.Itoa(os.Getpid())
	tmux := func(args ...string) *exec.Cmd {
		return exec.Command("tmux", append([]string{"-L", socket}, args...)...)
	}
	t.Cleanup(func() { _ = tmux("kill-server").Run() })
	// Wide enough that no corpus line wraps, which would corrupt the reading.
	if out, err := tmux("new-session", "-d", "-x", "900", "-y", "10",
		"bash "+scriptPath+" "+inPath+" "+outPath+"; sleep 300").CombinedOutput(); err != nil {
		t.Fatalf("start tmux: %v: %s", err, out)
	}

	deadline := time.Now().Add(90 * time.Second)
	var measured []string
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(outPath)
		if err == nil {
			measured = strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
			if len(measured) > 0 && measured[len(measured)-1] == "DONE" {
				measured = measured[:len(measured)-1]
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if len(measured) != len(cases) {
		t.Fatalf("measured %d of %d lines; tmux probe did not finish", len(measured), len(cases))
	}

	drift, slack := 0, 0
	for i, c := range cases {
		want, err := strconv.Atoi(strings.TrimSpace(measured[i]))
		if err != nil {
			t.Fatalf("line %d: unreadable measurement %q", i, measured[i])
		}
		if want != c.W {
			drift++
			t.Errorf("recording is stale: testdata says %d, this tmux paints %d: %q", c.W, want, c.S)
		}
		got := cellWidth(c.S)
		switch {
		case got < want:
			t.Errorf("cellWidth = %d but tmux needs %d — this overflows the column: %q", got, want, c.S)
		case got > want:
			slack++
		}
	}
	if slack > knownSlack {
		t.Errorf("%d entries read wider than tmux, allowed %d", slack, knownSlack)
	}
	if drift > 0 {
		t.Logf("%d recorded widths differ from this tmux (%s)", drift, tmuxVersion())
	}
}

func tmuxVersion() string {
	out, err := exec.Command("tmux", "-V").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
