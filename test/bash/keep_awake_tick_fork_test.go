package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// keep_awake_tick runs on EVERY watcher tick in every open session -- twice a
// second -- and its last act in the common case (feature off, nothing held) is
// to check for this session's holder file. Building that path through
// `$(keep_awake_holders_dir ...)` forks a subshell to do it: no exec, so no
// PATH shim can see it, but a fork all the same. Measured at 1000 iterations:
// 0.681s of child CPU with the substitution, 0.000s with the literal path.
//
// A bash function cannot hand back a string without a subshell, so calling the
// helper in a value context IS the fork. This redefines the helper after the
// module is sourced: if the tick still routes through it, the marker appears.
func TestKeepAwakeTick_builds_the_holder_path_without_forking(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "called")
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("create config dir: %v", err)
	}

	script := fmt.Sprintf(`
source ../../lib/keep-awake.sh
keep_awake_holders_dir() { echo "called" >> %q; echo "$1/keep-awake.d"; }
keep_awake_tick %q "sess-1" 4242 "idle"
`, marker, configDir)

	_, code := runBashSnippet(t, script, buildEnv(t, nil))
	assertExitCode(t, code, 0)

	if raw, err := os.ReadFile(marker); err == nil && len(strings.TrimSpace(string(raw))) != 0 {
		t.Errorf("the tick forked a subshell to build the holder path %d time(s)",
			len(strings.Split(strings.TrimSpace(string(raw)), "\n")))
	}
}

// The literal path the tick uses must stay the one the helper produces, or the
// tick checks for a holder somewhere the rest of the module never writes.
func TestKeepAwakeTick_holder_path_matches_the_helper(t *testing.T) {
	dir := t.TempDir()
	out, code := runBashFunc(t, "lib/keep-awake.sh", "keep_awake_holders_dir",
		[]string{dir}, nil)
	assertExitCode(t, code, 0)

	want := dir + "/keep-awake.d"
	if strings.TrimSpace(out) != want {
		t.Fatalf("keep_awake_holders_dir = %q, want %q", strings.TrimSpace(out), want)
	}

	source, err := os.ReadFile("../../lib/keep-awake.sh")
	if err != nil {
		t.Fatalf("read keep-awake.sh: %v", err)
	}
	if !strings.Contains(string(source), `"$config_dir/keep-awake.d/$session"`) {
		t.Error("the tick no longer uses the literal holders path this pins to the helper")
	}
}
