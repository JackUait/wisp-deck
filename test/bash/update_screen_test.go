package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The update runs from the wrapper's menu loop, right after the menu tears down
// its alt screen. Before this work it printed one line ("⇡ Updating
// wisp-deck...") wherever the cursor happened to be — over the leftover splash
// art — and then let npm's raw output scroll past. These tests pin the
// replacement: a full-screen, themed progress view that owns the terminal for
// the length of the update and never leaks installer output onto it.

var updateScreenAnsi = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func stripEscapes(s string) string { return updateScreenAnsi.ReplaceAllString(s, "") }

// --- update_step_label ---

// The installer's own chatter is the only progress signal npm gives us, so the
// screen translates it into steps a user recognises. An unrecognised line must
// report failure rather than a label, so the caller keeps the step it already
// shows instead of flickering through every stray line of output.
func TestUpdateScreen_step_label_maps_installer_output(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{"install start", "Installing wisp-deck 2.24.0 to /Users/x/.local/share/wisp-deck...", "Installing files"},
		{"install done", "Installed wisp-deck 2.24.0", "Installing files"},
		{"binary download", "Downloading wisp-deck-tui 2.24.0...", "Downloading interface"},
		{"binary update", "Updating wisp-deck-tui (2.23.0 -> 2.24.0)...", "Downloading interface"},
		{"configuring", "Setting up wrapper script...", "Configuring"},
		{"finishing", "Setup complete!", "Finishing up"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snippet := updateSnippet(t, fmt.Sprintf("update_step_label %q", tt.line))
			out, code := runBashSnippet(t, snippet, nil)
			assertExitCode(t, code, 0)
			if got := strings.TrimSpace(out); got != tt.want {
				t.Errorf("update_step_label(%q) = %q, want %q", tt.line, got, tt.want)
			}
		})
	}
}

func TestUpdateScreen_step_label_rejects_unknown_lines(t *testing.T) {
	snippet := updateSnippet(t, `update_step_label "npm warn deprecated foo@1.0.0"`)
	out, code := runBashSnippet(t, snippet, nil)
	if code == 0 {
		t.Errorf("expected a non-zero exit for an unrecognised line, got %q", out)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected no label for an unrecognised line, got %q", out)
	}
}

// --- render_update_frame ---

// The frame is the splash's wordmark with a progress block under it: same art,
// same gradient, so the update reads as the same application rather than a
// terminal script that took over.
func TestUpdateScreen_frame_shows_version_and_step_below_the_art(t *testing.T) {
	snippet := updateSnippet(t, `render_update_frame 0 120 40 "" "2.24.0" "Downloading interface"`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	plain := stripEscapes(out)
	if !strings.Contains(plain, "v2.24.0") {
		t.Errorf("frame does not name the version it is installing: %q", plain)
	}
	if !strings.Contains(plain, "Downloading interface") {
		t.Errorf("frame does not show the current step: %q", plain)
	}
	// The wordmark is the splash art, unchanged.
	if !strings.Contains(plain, "88888P\"") {
		t.Errorf("frame is missing the Wisp Deck wordmark: %q", plain)
	}

	// Every cursor move is absolute, and the status block sits strictly below
	// the last art row — no overlap with the wordmark.
	rows := cursorRows(t, out)
	if len(rows) < 3 {
		t.Fatalf("expected absolute cursor positioning, got %q", out)
	}
	artRows, statusRows := rows[:len(rows)-3], rows[len(rows)-3:]
	lastArt := artRows[len(artRows)-1]
	for _, r := range statusRows {
		if r <= lastArt {
			t.Errorf("status row %d overlaps the art (last art row %d)", r, lastArt)
		}
	}
}

// The whole block — art plus status — stays inside the terminal and centred, so
// a short window does not push the progress line off the bottom.
func TestUpdateScreen_frame_fits_a_short_terminal(t *testing.T) {
	snippet := updateSnippet(t, `render_update_frame 0 100 20 "" "2.24.0" "Configuring"`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	for _, r := range cursorRows(t, out) {
		if r < 1 || r > 20 {
			t.Errorf("frame writes to row %d, outside a 20-row terminal", r)
		}
	}
}

// A palette override (the user's theme preset) colours the update the same way
// it colours the splash.
func TestUpdateScreen_frame_honours_the_palette_override(t *testing.T) {
	snippet := updateSnippet(t, `render_update_frame 0 120 40 "51 87 123" "2.24.0" "Configuring"`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if !strings.Contains(out, "38;5;51m") {
		t.Errorf("frame ignored the palette override: %q", out)
	}
}

// --- run_wisp_deck_update ---

// The installer is asked for a non-interactive update. Without the flag the new
// installer runs its first-time-setup questions ("Select AI Tools") in the
// middle of an update the user triggered from the menu.
func TestUpdateScreen_run_update_asks_npx_for_an_update(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "npx-args")
	binDir := mockCommand(t, dir, "npx", fmt.Sprintf(`echo "$@" > %q`, argsFile))
	env := buildEnv(t, []string{binDir}, "HOME="+dir)

	snippet := updateSnippet(t, `run_wisp_deck_update`)
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("expected npx to be invoked: %v", err)
	}
	assertContains(t, string(data), "wisp-deck@latest")
	assertContains(t, string(data), "--update")
}

// --update only exists in installers from this version on. An install whose
// lib/ is newer than the published package it pulls — a dev machine running off
// live symlinks, or any resolution that lands on an older tarball — gets
// "Unknown flag: --update" and exit 1. That must degrade to the previous
// behaviour (a flagless, interactive install with the terminal handed back),
// not to a hard "Update failed" for a version that is perfectly installable.
func TestUpdateScreen_run_update_retries_without_the_flag_on_an_old_installer(t *testing.T) {
	dir := t.TempDir()
	callsFile := filepath.Join(dir, "npx-calls")
	binDir := mockCommand(t, dir, "npx", fmt.Sprintf(`
echo "$@" >> %q
for arg in "$@"; do
  if [ "$arg" = "--update" ]; then
    echo "Unknown flag: --update" >&2
    exit 1
  fi
done
echo "Installed wisp-deck 2.24.0"
`, callsFile))
	env := buildEnv(t, []string{binDir}, "HOME="+dir, "WISP_DECK_UPDATE_NO_WAIT=1")

	out, code := runBashSnippet(t, updateSnippet(t, `run_wisp_deck_update`), env)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(callsFile)
	if err != nil {
		t.Fatalf("expected npx to be invoked: %v", err)
	}
	calls := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(calls) != 2 {
		t.Fatalf("expected two npx invocations (flagged, then flagless), got %q", calls)
	}
	if !strings.Contains(calls[0], "--update") {
		t.Errorf("first invocation should carry --update, got %q", calls[0])
	}
	if strings.Contains(calls[1], "--update") {
		t.Errorf("retry should drop --update, got %q", calls[1])
	}
	if strings.Contains(stripEscapes(out), "Update failed") {
		t.Errorf("a recoverable flag rejection was reported as a failed update: %q", out)
	}
}

// Any other failure is a real failure — the retry must not swallow it and try
// again, or a broken registry turns into two identical stalls.
func TestUpdateScreen_run_update_does_not_retry_other_failures(t *testing.T) {
	dir := t.TempDir()
	callsFile := filepath.Join(dir, "npx-calls")
	binDir := mockCommand(t, dir, "npx", fmt.Sprintf(
		`echo "$@" >> %q; echo "ENOTFOUND registry.npmjs.org" >&2; exit 1`, callsFile))
	env := buildEnv(t, []string{binDir}, "HOME="+dir, "WISP_DECK_UPDATE_NO_WAIT=1")

	_, code := runBashSnippet(t, updateSnippet(t, `run_wisp_deck_update`), env)
	if code == 0 {
		t.Error("expected a non-zero exit for a genuine failure")
	}
	data, _ := os.ReadFile(callsFile)
	if got := len(strings.Split(strings.TrimSpace(string(data)), "\n")); got != 1 {
		t.Errorf("expected exactly one npx invocation, got %d", got)
	}
}

// Installer output belongs in a log, not on the screen the user is watching.
// npm's progress bars and warnings scrolling over the art is exactly the mess
// this screen replaces.
func TestUpdateScreen_run_update_keeps_installer_output_off_the_screen(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "npx", `echo "npm warn NOISE"; echo "Installed wisp-deck 2.24.0"`)
	env := buildEnv(t, []string{binDir}, "HOME="+dir)

	snippet := updateSnippet(t, `run_wisp_deck_update`)
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	if strings.Contains(out, "NOISE") {
		t.Errorf("raw installer output leaked onto the update screen: %q", out)
	}
	logPath := filepath.Join(dir, ".local", "share", "wisp-deck", "update.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("expected the installer output in %s: %v", logPath, err)
	}
	assertContains(t, string(data), "NOISE")
}

// A failed update must say so and point at the log — silently returning to the
// menu leaves the user believing they are on the new version.
func TestUpdateScreen_run_update_reports_failure_with_the_log(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "npx", `echo "ENOTFOUND registry.npmjs.org" >&2; exit 1`)
	env := buildEnv(t, []string{binDir}, "HOME="+dir, "WISP_DECK_UPDATE_NO_WAIT=1")

	snippet := updateSnippet(t, `run_wisp_deck_update`)
	out, code := runBashSnippet(t, snippet, env)
	if code == 0 {
		t.Errorf("expected a non-zero exit when the installer fails")
	}
	plain := stripEscapes(out)
	if !strings.Contains(plain, "Update failed") {
		t.Errorf("failure was not reported: %q", plain)
	}
	assertContains(t, plain, "update.log")
}

// The screen owns the terminal for its duration and hands it back clean:
// cursor hidden while it animates, shown again when it is done, and the
// scrollback cleared so the menu it returns to is not drawn over the art.
func TestUpdateScreen_run_update_restores_the_terminal(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "npx", `true`)
	env := buildEnv(t, []string{binDir}, "HOME="+dir)

	snippet := updateSnippet(t, `run_wisp_deck_update`)
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	hide := strings.LastIndex(out, "\x1b[?25l")
	show := strings.LastIndex(out, "\x1b[?25h")
	if hide < 0 {
		t.Errorf("update screen did not hide the cursor: %q", out)
	}
	if show < hide {
		t.Errorf("update screen did not restore the cursor after hiding it: %q", out)
	}
	if !strings.Contains(out, "\x1b[2J") {
		t.Errorf("update screen did not clear the terminal it took over: %q", out)
	}
}

// cursorRows returns the row of every absolute cursor-position escape, in order.
func cursorRows(t *testing.T, s string) []int {
	t.Helper()
	re := regexp.MustCompile(`\x1b\[(\d+);(\d+)H`)
	var rows []int
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		var r int
		fmt.Sscanf(m[1], "%d", &r)
		rows = append(rows, r)
	}
	return rows
}
