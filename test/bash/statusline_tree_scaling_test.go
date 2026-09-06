package bash_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// statuslineTreeMocks stands in for a real process tree: the root has `kids`
// direct children and nothing below them. pgrep answers a comma-separated
// parent list the way macOS pgrep does -- the union of those parents' children
// -- so a walk that batches a level and a walk that asks one PID at a time both
// get the right answer, and only the spawn COUNT tells them apart.
func statuslineTreeMocks(t *testing.T, dir string, root, kids int) (binDir, logPath string) {
	t.Helper()
	logPath = filepath.Join(dir, "spawn.log")
	binDir = mockCommand(t, dir, "pgrep", `
echo "pgrep $*" >> "$ST_LOG"
list=""
prev=""
for a in "$@"; do [ "$prev" = "-P" ] && list="$a"; prev="$a"; done
case ",$list," in
  *",$ST_ROOT,"*)
    i=1
    while [ "$i" -le "$ST_KIDS" ]; do echo $((ST_ROOT + i)); i=$((i + 1)); done
    ;;
esac
exit 0
`)
	mockCommand(t, dir, "ps", `
echo "ps $*" >> "$ST_LOG"
list=""
prev=""
for a in "$@"; do [ "$prev" = "-p" ] && list="$a"; prev="$a"; done
old="$IFS"; IFS=','
for p in $list; do IFS="$old"; echo "  1.5"; IFS=','; done
IFS="$old"
exit 0
`)
	mockCommand(t, dir, "tr", `echo "tr $*" >> "$ST_LOG"; exec /usr/bin/tr "$@"`)
	mockCommand(t, dir, "footprint", `echo "footprint $*" >> "$ST_LOG"; exit 1`)
	return binDir, logPath
}

func statuslineSpawnCount(t *testing.T, function string, kids int) int {
	t.Helper()
	const root = 4000
	dir := t.TempDir()
	binDir, logPath := statuslineTreeMocks(t, dir, root, kids)
	env := buildEnv(t, []string{binDir},
		"ST_LOG="+logPath,
		"ST_ROOT="+strconv.Itoa(root),
		"ST_KIDS="+strconv.Itoa(kids),
	)
	runBashFunc(t, "lib/statusline.sh", function, []string{strconv.Itoa(root)}, env)

	raw, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read spawn log: %v", err)
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "\n"))
}

// The statusline repaints on every token-usage update in every open session, and
// these walks describe the agent's whole process tree. Asking the kernel about
// one PID at a time made a render cost a process per process in the tree: a
// session running a test sweep reaches hundreds, and the walk alone was measured
// at 3.74 CPU-seconds a render. A tree is a handful of LEVELS, so the cost must
// be set by the depth, never by the population.
func TestStatuslineTreeWalks_spawn_count_does_not_grow_with_the_tree(t *testing.T) {
	for _, function := range []string{"get_tree_cpu_pct", "get_tree_footprint_kb", "get_tree_rss_kb"} {
		t.Run(function, func(t *testing.T) {
			small := statuslineSpawnCount(t, function, 1)
			large := statuslineSpawnCount(t, function, 40)

			if large > small {
				t.Errorf(
					"%s spawned %d processes for a 40-process tree against %d for a 1-process one;\n"+
						"a level is one batched query, so the count must not follow the population",
					function, large, small,
				)
			}
		})
	}
}
