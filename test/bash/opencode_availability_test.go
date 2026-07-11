package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Launch-speed regression guard.
//
// resolve_opencode_cmd's npx branch shells out to `npx --no-install opencode-ai
// --version` to find out WHICH npx invocation can launch OpenCode. That probe
// spawns node and costs 6-13s on a warm cache (measured). wrapper.sh used to run
// it unconditionally, before the project picker painted — so every launch, for
// every user, on every tool, paid seconds of dead time to answer a question only
// an OpenCode launch ever asks.
//
// The fix splits the two questions the probe was conflating:
//
//	"CAN we run opencode?"    -> opencode_available: a PATH check, no subprocess.
//	"HOW do we run opencode?" -> resolve_opencode_cmd: the probe, on demand only.
//
// Availability never needed the probe: resolve_opencode_cmd returns non-empty
// exactly when `opencode` or `npx` is on PATH, and the probe only chooses
// between two npx strings. These tests pin that equivalence, and pin that the
// cheap path never spawns npx.

// spyNPX mocks npx so that ANY invocation records itself to a marker file.
// A test asserting the marker stays absent is asserting "no subprocess was
// spawned" — i.e. the seconds are actually gone, not merely moved.
func spyNPX(t *testing.T, dir string, probeExit int) (binDir, marker string) {
	t.Helper()
	marker = filepath.Join(dir, "npx-was-called")
	body := `echo "$@" >> "` + marker + `"
[ "$1" = "--no-install" ] && exit ` + itoa(probeExit) + `
exit 0`
	return mockCommand(t, dir, "npx", body), marker
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	return "1"
}

func assertNPXNotCalled(t *testing.T, marker string) {
	t.Helper()
	if data, err := os.ReadFile(marker); err == nil {
		t.Errorf("npx was spawned on the availability path (args: %q); "+
			"that probe costs 6-13s and must not run before the picker",
			strings.TrimSpace(string(data)))
	}
}

func TestOpencodeAvailable_true_for_direct_binary(t *testing.T) {
	dir := t.TempDir()
	binDir, marker := spyNPX(t, dir, 0)
	mockCommand(t, dir, "opencode", `echo opencode "$@"`)

	_, code := runBashFunc(t, "lib/ai-tools.sh", "opencode_available", nil, ocEnv(t, binDir))
	assertExitCode(t, code, 0)
	assertNPXNotCalled(t, marker)
}

// The case that was costing every launch 6-13s: no opencode binary, npx present.
// Availability is decidable from PATH alone — npx must not be spawned to learn it.
func TestOpencodeAvailable_true_when_only_npx_and_never_spawns_npx(t *testing.T) {
	dir := t.TempDir()
	binDir, marker := spyNPX(t, dir, 0)

	_, code := runBashFunc(t, "lib/ai-tools.sh", "opencode_available", nil, ocEnv(t, binDir))
	assertExitCode(t, code, 0)
	assertNPXNotCalled(t, marker)
}

func TestOpencodeAvailable_false_when_neither_present(t *testing.T) {
	dir := t.TempDir()
	emptyBin := filepath.Join(dir, "empty")
	if err := os.MkdirAll(emptyBin, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, code := runBashFunc(t, "lib/ai-tools.sh", "opencode_available", nil, ocEnv(t, emptyBin))
	if code == 0 {
		t.Error("opencode_available succeeded with neither opencode nor npx on PATH")
	}
}

// The equivalence that makes the cheap check safe to substitute: opencode_available
// agrees with "resolve_opencode_cmd is non-empty" in every PATH configuration, so
// the tool list the picker shows is unchanged.
func TestOpencodeAvailable_agrees_with_resolve_opencode_cmd(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string) string
		want  bool
	}{
		{"binary present", func(t *testing.T, dir string) string {
			b := mockCommand(t, dir, "opencode", `:`)
			mockCommand(t, dir, "npx", `exit 1`)
			return b
		}, true},
		{"npx with cached copy", func(t *testing.T, dir string) string {
			return mockCommand(t, dir, "npx", `[ "$1" = "--no-install" ] && exit 0; exit 0`)
		}, true},
		{"npx without cached copy", func(t *testing.T, dir string) string {
			return mockCommand(t, dir, "npx", `[ "$1" = "--no-install" ] && exit 1; exit 0`)
		}, true},
		{"neither", func(t *testing.T, dir string) string {
			empty := filepath.Join(dir, "empty")
			if err := os.MkdirAll(empty, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			return empty
		}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			binDir := tc.setup(t, dir)
			env := ocEnv(t, binDir)

			_, code := runBashFunc(t, "lib/ai-tools.sh", "opencode_available", nil, env)
			gotAvail := code == 0

			out, _ := runBashFunc(t, "lib/ai-tools.sh", "resolve_opencode_cmd", nil, env)
			gotResolve := strings.TrimSpace(out) != ""

			if gotAvail != gotResolve {
				t.Errorf("opencode_available=%v but resolve_opencode_cmd non-empty=%v; "+
					"the cheap check must not change which tools the picker lists",
					gotAvail, gotResolve)
			}
			if gotAvail != tc.want {
				t.Errorf("opencode_available=%v, want %v", gotAvail, tc.want)
			}
		})
	}
}

// wrapper.sh runs on the critical path between the Ghostty window opening and
// the project picker painting. The npx probe must not appear there: availability
// comes from opencode_available, and the command string is resolved only on the
// branch that actually launches OpenCode.
func TestWrapper_does_not_probe_npx_before_the_picker(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "wrapper.sh"))
	if err != nil {
		t.Fatalf("read wrapper.sh: %v", err)
	}
	// Comments discuss resolve_opencode_cmd at length (that is the point of the
	// fix); only executable lines count as calling it.
	var code []string
	for _, line := range strings.Split(string(data), "\n") {
		if trimmed := strings.TrimSpace(line); !strings.HasPrefix(trimmed, "#") {
			code = append(code, line)
		}
	}

	pickerLine, probeLine := -1, -1
	for i, line := range code {
		if pickerLine < 0 && strings.Contains(line, "select_project_interactive") {
			pickerLine = i
		}
		if probeLine < 0 && strings.Contains(line, "resolve_opencode_cmd") {
			probeLine = i
		}
	}
	if pickerLine < 0 {
		t.Fatal("wrapper.sh no longer calls select_project_interactive; update this guard")
	}
	if probeLine >= 0 && probeLine < pickerLine {
		t.Errorf("wrapper.sh calls resolve_opencode_cmd (line %q) before the picker; "+
			"that probe spawns npx and costs 6-13s of dead time before the main screen paints",
			strings.TrimSpace(code[probeLine]))
	}
	if !strings.Contains(strings.Join(code, "\n"), "opencode_available") {
		t.Error("wrapper.sh must decide OpenCode availability with opencode_available (no subprocess)")
	}
}

// With the probe deferred, a claude/codex session's relaunch context carries no
// opencode_cmd. A later mid-session switch TO opencode must still resolve a real
// command — _tool_cmd_for falls back to resolve_opencode_cmd, which knows about
// the npx branch (plain `command -v opencode` does not, and would return empty).
func TestToolCmdFor_opencode_resolves_npx_when_context_has_no_command(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "npx", `[ "$1" = "--no-install" ] && exit 0; exit 0`)

	out, code := runBashFunc(t, "lib/account-switch.sh", "_tool_cmd_for",
		[]string{"opencode"}, ocEnv(t, binDir))
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "npx --no-install opencode-ai" {
		t.Errorf("_tool_cmd_for opencode = %q, want %q (npx fallback, not empty)",
			got, "npx --no-install opencode-ai")
	}
}
