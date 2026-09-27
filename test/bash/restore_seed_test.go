package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Sessions opened before tab-order tracking carry no @wd_seq and are not in
// tab-order. The first snapshot tick stamps them, installs the focus hook the
// live server never got from a new wrapper, and records them ahead of every
// tracked tab, in launch order.
func TestWriteSessionSnapshot_seeds_untracked_sessions_once(t *testing.T) {
	dir := t.TempDir()
	snap := filepath.Join(dir, "last-session")
	writeTempFile(t, dir, "tab-order", "50\n")
	bin := fakeTmux(t, dir, []fakeTmuxSession{
		{name: "dev-b-2", created: 1, attached: 1, env: wispEnv("B1", "b", "/p/b", "30", "")},
		{name: "dev-a-1", created: 1, attached: 1, env: wispEnv("B1", "a", "/p/a", "20", "")},
		{name: "dev-c-3", created: 1, attached: 1, env: wispEnv("B1", "c", "/p/c", "50", "")},
	}, []string{"dev-b-2|0|1||L|0", "dev-a-1|0|1||L|0", "dev-c-3|0|1||L|1"})
	writeSnapshot(t, bin, snap)

	calls, err := os.ReadFile(filepath.Join(dir, "fake-tmux", "calls"))
	if err != nil {
		t.Fatal(err)
	}
	c := string(calls)
	assertContains(t, c, "set-option -t =dev-b-2: @wd_seq 30")
	assertContains(t, c, "set-option -t =dev-a-1: @wd_seq 20")
	assertNotContains(t, c, "=dev-c-3: @wd_seq")
	assertContains(t, c, "set-hook -g client-focus-in[77]")
	got := strings.Join(readLines(t, filepath.Join(dir, "tab-order")), ",")
	if got != "20,30,50" {
		t.Errorf("tab-order = %q, want 20,30,50", got)
	}
}

func TestWriteSessionSnapshot_tracked_sessions_cost_no_seeding(t *testing.T) {
	dir := t.TempDir()
	snap := filepath.Join(dir, "last-session")
	bin := fakeTmux(t, dir, []fakeTmuxSession{
		{name: "dev-a-1", created: 1, attached: 1, env: wispEnv("B1", "a", "/p/a", "20", "")},
	}, []string{"dev-a-1|0|1||L|1"})
	writeSnapshot(t, bin, snap)
	calls, _ := os.ReadFile(filepath.Join(dir, "fake-tmux", "calls"))
	assertNotContains(t, string(calls), "set-option -t")
	assertNotContains(t, string(calls), "set-hook")
	if _, err := os.Stat(filepath.Join(dir, "tab-order")); !os.IsNotExist(err) {
		t.Errorf("tab-order written with nothing to seed: %v", err)
	}
}
