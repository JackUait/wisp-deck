package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Claude Code repaints the statusline constantly while the user works, and this
// wrapper reached ccstatusline through `npx`. npx re-resolves the npm graph
// every time, for an answer that never changes: measured 5724ms against 2521ms
// for the identical global binary the resolution finds. Prefer the binary.
func TestCCStatuslineCmd_prefers_the_binary_on_PATH(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "ccstatusline", `exit 0`)
	mockCommand(t, dir, "npx", `exit 0`)
	env := buildEnv(t, []string{binDir})

	out, code := runBashFunc(t, "lib/statusline.sh", "gt_ccstatusline_cmd", nil, env)
	assertExitCode(t, code, 0)

	got := strings.TrimSpace(out)
	want := filepath.Join(binDir, "ccstatusline")
	if got != want {
		t.Errorf("resolved %q, want the binary at %q", got, want)
	}
	if strings.Contains(got, "npx") {
		t.Errorf("resolved through npx despite ccstatusline being on PATH: %q", got)
	}
}

// A machine that has ccstatusline only as an npm package must still render.
func TestCCStatuslineCmd_falls_back_to_npx_when_absent(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "npx", `exit 0`)
	env := buildEnv(t, []string{binDir})
	// An empty PATH entry dir with only npx: ccstatusline is unreachable.
	env = append(env, "PATH="+binDir)

	out, code := runBashFunc(t, "lib/statusline.sh", "gt_ccstatusline_cmd", nil, env)
	assertExitCode(t, code, 0)

	if got := strings.TrimSpace(out); got != "npx ccstatusline" {
		t.Errorf("resolved %q, want the npx fallback", got)
	}
}

// The wrapper must actually use the resolver rather than calling npx inline.
func TestStatuslineWrapper_reaches_ccstatusline_through_the_resolver(t *testing.T) {
	wrapper := filepath.Join(projectRoot(t), "templates", "statusline-wrapper.sh")
	data, err := os.ReadFile(wrapper)
	if err != nil {
		t.Fatalf("read statusline wrapper: %v", err)
	}
	source := string(data)
	for number, line := range strings.Split(source, "\n") {
		code := line
		if hash := strings.Index(code, "#"); hash >= 0 {
			code = code[:hash]
		}
		// A pipeline INTO npx is the defect. Naming npx as the resolver's own
		// fallback string is not: a machine without the binary still renders.
		if strings.Contains(code, "| npx") {
			t.Errorf("statusline-wrapper.sh:%d pipes into npx; it must go through gt_ccstatusline_cmd:\n  %s",
				number+1, strings.TrimSpace(line))
		}
	}
	if !strings.Contains(source, "gt_ccstatusline_cmd") {
		t.Error("statusline wrapper does not use the ccstatusline resolver")
	}
}
