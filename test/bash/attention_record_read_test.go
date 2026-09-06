package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The watcher tick reads four small attention records (the descriptor three
// times -- twice inside the snapshot's deliberate torn-read guard -- and the
// state once), twice a second, in every open session. Each read piped `wc -c`
// through `tr` purely to strip whitespace from the byte count, which bash does
// with a builtin. That `tr` alone was 4 processes a tick, ~136 a second across
// 17 sessions.
func TestAttentionWatcherReadRecord_does_not_shell_out_to_strip_a_number(t *testing.T) {
	dir := t.TempDir()
	spawnLog := filepath.Join(dir, "spawn.log")
	binDir := mockCommand(t, dir, "tr", `echo "tr" >> "$AW_SPAWN_LOG"; exec /usr/bin/tr "$@"`)
	env := buildEnv(t, []string{binDir}, "AW_SPAWN_LOG="+spawnLog)

	record := filepath.Join(dir, "state")
	if err := os.WriteFile(record, []byte("1\tgeneration.x\t7\tbusy\t-\n"), 0o600); err != nil {
		t.Fatalf("write record: %v", err)
	}

	_, code := runBashFunc(t, "lib/tab-title-watcher.sh", "_attention_watcher_read_record",
		[]string{record}, env)
	assertExitCode(t, code, 0)

	if raw, err := os.ReadFile(spawnLog); err == nil && len(strings.TrimSpace(string(raw))) != 0 {
		t.Errorf("reading one record shelled out to tr:\n%s", raw)
	}
}

// The byte guard is a hard bound on an on-disk record, so every edge it
// rejected before must still be rejected.
func TestAttentionWatcherReadRecord_keeps_its_size_and_shape_guards(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    int
	}{
		{"one clean line", "1\tgeneration.x\t7\tbusy\t-\n", 0},
		// A strict record must be newline-terminated; a truncated write is
		// exactly what that rejects.
		{"no trailing newline", "1\tgeneration.x\t7\tbusy\t-", 1},
		{"empty file", "", 1},
		{"two lines", "first\nsecond\n", 1},
		{"carriage return", "1\tgeneration.x\r\n", 1},
		{"over 4096 bytes", strings.Repeat("x", 4097) + "\n", 1},
		{"exactly 4096 bytes", strings.Repeat("x", 4095) + "\n", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			record := filepath.Join(dir, "state")
			if err := os.WriteFile(record, []byte(c.content), 0o600); err != nil {
				t.Fatalf("write record: %v", err)
			}
			_, code := runBashFunc(t, "lib/tab-title-watcher.sh", "_attention_watcher_read_record",
				[]string{record}, buildEnv(t, nil))
			if code != c.want {
				t.Errorf("exit %d, want %d", code, c.want)
			}
		})
	}
}
