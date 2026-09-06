package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The watcher tick reads three settings keys twice a second in every open
// session, and each key cost a `grep | head -1 | cut | tr` pipeline -- four
// processes per key, twelve per tick, ~400 processes a second machine-wide on
// a 17-session deck. Bash can read a small key=value file with no process at
// all.
func TestReadSettingsValue_spawns_no_process(t *testing.T) {
	dir := t.TempDir()
	spawnLog := filepath.Join(dir, "spawn.log")
	binDir := mockCommand(t, dir, "grep", `echo "grep" >> "$WD_SPAWN_LOG"; exec /usr/bin/grep "$@"`)
	mockCommand(t, dir, "head", `echo "head" >> "$WD_SPAWN_LOG"; exec /usr/bin/head "$@"`)
	mockCommand(t, dir, "cut", `echo "cut" >> "$WD_SPAWN_LOG"; exec /usr/bin/cut "$@"`)
	mockCommand(t, dir, "tr", `echo "tr" >> "$WD_SPAWN_LOG"; exec /usr/bin/tr "$@"`)
	env := buildEnv(t, []string{binDir}, "WD_SPAWN_LOG="+spawnLog)

	settings := filepath.Join(dir, "settings")
	writeTempFile(t, dir, "settings", "theme=auto\ntab_title=full\ntab_bar=large\n")

	out, code := runBashFunc(t, "lib/tab-title-watcher.sh", "read_settings_value",
		[]string{settings, "tab_bar"}, env)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "large" {
		t.Fatalf("read %q, want \"large\"", got)
	}

	if raw, err := os.ReadFile(spawnLog); err == nil && len(strings.TrimSpace(string(raw))) != 0 {
		t.Errorf("reading one settings key spawned processes:\n%s", raw)
	}
}

// The pipeline it replaces took the FIRST match (head -1) and stripped all
// whitespace from the value. Both have to survive.
func TestReadSettingsValue_keeps_first_match_and_strips_whitespace(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings")
	writeTempFile(t, dir, "settings", "theme=  ocean  \ntheme=later\nempty=\n")
	env := buildEnv(t, nil)

	out, code := runBashFunc(t, "lib/tab-title-watcher.sh", "read_settings_value",
		[]string{settings, "theme"}, env)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "ocean" {
		t.Errorf("read %q, want \"ocean\" (first match, whitespace stripped)", got)
	}

	out, code = runBashFunc(t, "lib/tab-title-watcher.sh", "read_settings_value",
		[]string{settings, "empty"}, env)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "" {
		t.Errorf("read %q for an empty value, want \"\"", got)
	}
}

// A settings file whose last line has no trailing newline must still be read;
// grep saw that line and a naive `while read` loop would drop it.
func TestReadSettingsValue_reads_a_final_line_without_a_newline(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings")
	if err := os.WriteFile(settings, []byte("theme=auto\ntab_bar=compact"), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	out, code := runBashFunc(t, "lib/tab-title-watcher.sh", "read_settings_value",
		[]string{settings, "tab_bar"}, buildEnv(t, nil))
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "compact" {
		t.Errorf("read %q from an unterminated final line, want \"compact\"", got)
	}
}
