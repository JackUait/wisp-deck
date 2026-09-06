package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// keep_awake_enabled runs on EVERY watcher tick in every open session --
// twice a second, 17 sessions -- and it is the first thing the tick does even
// when the feature is off, which is the default. Grepping a three-line settings
// file for that costs a process each time.
func TestKeepAwakeEnabled_spawns_no_process(t *testing.T) {
	dir := t.TempDir()
	spawnLog := filepath.Join(dir, "spawn.log")
	binDir := mockCommand(t, dir, "grep", `echo "grep" >> "$KA_SPAWN_LOG"; exec /usr/bin/grep "$@"`)
	env := buildEnv(t, []string{binDir}, "KA_SPAWN_LOG="+spawnLog)

	settings := filepath.Join(dir, "settings")
	writeTempFile(t, dir, "settings", "theme=auto\nkeep_awake=on\ntab_bar=large\n")

	_, code := runBashFunc(t, "lib/keep-awake.sh", "keep_awake_enabled",
		[]string{settings}, env)
	assertExitCode(t, code, 0)

	if raw, err := os.ReadFile(spawnLog); err == nil && len(strings.TrimSpace(string(raw))) != 0 {
		t.Errorf("reading one settings flag spawned a process:\n%s", raw)
	}
}

// The regex it replaces was anchored on both ends and tolerated only trailing
// whitespace. Every one of those distinctions decides whether the machine is
// held awake, so they are pinned exactly.
func TestKeepAwakeEnabled_matches_exactly_what_the_regex_did(t *testing.T) {
	cases := []struct {
		name     string
		contents string
		want     int
	}{
		{"plain on", "keep_awake=on\n", 0},
		{"trailing spaces", "keep_awake=on   \n", 0},
		{"trailing tab", "keep_awake=on\t\n", 0},
		{"among other keys", "theme=auto\nkeep_awake=on\ntab_bar=large\n", 0},
		{"final line unterminated", "theme=auto\nkeep_awake=on", 0},
		{"off", "keep_awake=off\n", 1},
		{"absent", "theme=auto\n", 1},
		{"empty file", "", 1},
		{"value has a suffix", "keep_awake=onward\n", 1},
		{"leading space", " keep_awake=on\n", 1},
		{"commented out", "#keep_awake=on\n", 1},
		{"key has a prefix", "xkeep_awake=on\n", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			settings := filepath.Join(dir, "settings")
			if err := os.WriteFile(settings, []byte(c.contents), 0o600); err != nil {
				t.Fatalf("write settings: %v", err)
			}
			_, code := runBashFunc(t, "lib/keep-awake.sh", "keep_awake_enabled",
				[]string{settings}, buildEnv(t, nil))
			if code != c.want {
				t.Errorf("exit %d, want %d for %q", code, c.want, c.contents)
			}
		})
	}
}

// A missing settings file must read as "off", not as an error that enables it.
func TestKeepAwakeEnabled_absent_file_is_off(t *testing.T) {
	dir := t.TempDir()
	_, code := runBashFunc(t, "lib/keep-awake.sh", "keep_awake_enabled",
		[]string{filepath.Join(dir, "nope")}, buildEnv(t, nil))
	if code == 0 {
		t.Error("a missing settings file enabled keep-awake")
	}
}
