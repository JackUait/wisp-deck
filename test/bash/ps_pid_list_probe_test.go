package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// macOS `ps` charges a flat penalty for a SECOND `-p` argument: one pid costs
// 3.4ms, two cost 88ms, and the penalty does not grow with the list -- it even
// fires on the same pid twice. On a loaded box (2002 processes) those two pids
// cost 7-13s. A steady-state tick that pays that never drains: the keep-awake
// reap runs at 2Hz in every session, so the probes stacked up (9 `ps` resident
// at once), one statusline render cost 12-21s, and opening a session took
// 10-15s. Batching per-holder probes into one pid list is what introduced it, so
// the fix is the whole table plus a filter, and this guard keeps the list out.
//
// The trigger is the pid LIST, not the machine, so this tests the shape.
func TestPsProbes_never_pass_a_pid_list(t *testing.T) {
	files := []string{
		"lib/keep-awake.sh",
		"lib/statusline.sh",
		// The wrapper's own fallbacks mirror the functions above, so a list is
		// likeliest to be reintroduced here.
		"templates/statusline-wrapper.sh",
	}
	for _, rel := range files {
		raw, err := os.ReadFile(filepath.Join("..", "..", rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		text := string(raw)
		// A variable comma-joined anywhere in the file is a pid list under some
		// other name, so `-p "$csv"` is caught as well as `-p "${pids// /,}"`.
		joined := map[string]bool{}
		for _, line := range strings.Split(text, "\n") {
			if i := strings.Index(line, "// /,"); i > 0 {
				name := strings.TrimSpace(line[:strings.Index(line, "=")+1])
				joined[strings.TrimSuffix(name, "=")] = true
			}
		}
		for i, line := range strings.Split(text, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if !strings.Contains(trimmed, "ps ") || !strings.Contains(trimmed, "-p ") {
				continue
			}
			arg := trimmed[strings.Index(trimmed, "-p ")+3:]
			if j := strings.IndexAny(arg, " |)"); j > 0 {
				arg = arg[:j]
			}
			bad := strings.Contains(arg, ",") || strings.Contains(trimmed, "// /,")
			for name := range joined {
				if name != "" && strings.Contains(arg, name) {
					bad = true
				}
			}
			if bad {
				t.Errorf("%s:%d passes a pid LIST to ps; read the whole table with -A and filter instead:\n\t%s",
					rel, i+1, trimmed)
			}
		}
	}
}
