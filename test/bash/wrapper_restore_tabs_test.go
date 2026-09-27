package bash_test

import (
	"regexp"
	"strings"
	"testing"
)

// queueLine builds a 10-field restore-queue line. windows are "index sid layout"
// triples joined the way the snapshot joins them (US inside, RS between).
func queueLine(sid, layout, active, first string, windows ...[3]string) string {
	var parts []string
	for _, w := range windows {
		parts = append(parts, w[0]+"\x1f"+w[1]+"\x1f"+w[2])
	}
	return "12345|PROJ|claude|" + sid + "|" + layout + "|||" +
		strings.Join(parts, "\x1e") + "|" + active + "|" + first + "\n"
}

func indexOf(t *testing.T, lines []string, re string) int {
	t.Helper()
	r := regexp.MustCompile(re)
	for i, l := range lines {
		if r.MatchString(l) {
			return i
		}
	}
	t.Fatalf("no call matched %q:\n%s", re, strings.Join(lines, "\n"))
	return -1
}

func TestWrapperRestoreTabs_recreates_each_window_then_selects_the_active_one(t *testing.T) {
	run := runWrapperWithStubs(t, wrapperStubOptions{queue: queueLine("sid-main", "layP", "2", "0",
		[3]string{"1", "sid-a", "layA"},
		[3]string{"2", "sid-b", "layB"},
	)})
	lines := run.calls(t, `^tmux select-window -t `)

	created := matching(lines, "tab_view_new_window ")
	if len(created) != 2 {
		t.Fatalf("want 2 tab_view_new_window calls, got %q", created)
	}
	wantA := regexp.MustCompile(`^tab_view_new_window \[[^]]*tmux\] \[[^]]*/lib\] \[(dev-proj-\d+)\] \[sid-a\] \[1\]$`)
	wantB := regexp.MustCompile(`^tab_view_new_window \[[^]]*tmux\] \[[^]]*/lib\] \[(dev-proj-\d+)\] \[sid-b\] \[2\]$`)
	m := wantA.FindStringSubmatch(created[0])
	if m == nil || !wantB.MatchString(created[1]) {
		t.Fatalf("windows must be created in ascending order with sid+index:\n%s", strings.Join(created, "\n"))
	}
	sess := m[1]

	sel := indexOf(t, lines, `^tmux select-window -t =`+sess+`:2$`)
	if sel < indexOf(t, lines, `^tab_view_new_window .*\[sid-b\] \[2\]$`) {
		t.Fatalf("the active window must be selected after every window exists:\n%s", strings.Join(lines, "\n"))
	}
	if got := matching(lines, "tmux move-window"); len(got) != 0 {
		t.Fatalf("first window index 0 must not move: %q", got)
	}

	lines = run.calls(t, `^restore_layout_watch .*\[layB\]`)
	watches := strings.Join(matching(lines, "restore_layout_watch "), "\n")
	assertContains(t, watches, "["+sess+"] [layP] [] [] [] [0]")
	assertContains(t, watches, "["+sess+"] [layA] [] [] [] [1]")
	assertContains(t, watches, "["+sess+"] [layB] [] [] [] [2]")
}

func TestWrapperRestoreTabs_moves_the_first_window_to_its_recorded_index(t *testing.T) {
	run := runWrapperWithStubs(t, wrapperStubOptions{queue: queueLine("sid-main", "layP", "3", "2",
		[3]string{"3", "sid-c", ""},
	)})
	lines := run.calls(t, `^tmux select-window -t `)
	m := regexp.MustCompile(`^tab_view_new_window .*\[(dev-proj-\d+)\] \[sid-c\] \[3\]$`).
		FindStringSubmatch(strings.Join(matching(lines, "tab_view_new_window "), "\n"))
	if m == nil {
		t.Fatalf("window 3 was not recreated:\n%s", strings.Join(lines, "\n"))
	}
	sess := m[1]
	move := indexOf(t, lines, `^tmux move-window -s =`+sess+`:0 -t =`+sess+`:2$`)
	if move > indexOf(t, lines, `^tmux select-window -t =`+sess+`:3$`) {
		t.Fatalf("the move must land before the active window is selected:\n%s", strings.Join(lines, "\n"))
	}

	lines = run.calls(t, `^restore_layout_watch .*\[layP\]`)
	watches := strings.Join(matching(lines, "restore_layout_watch "), "\n")
	assertContains(t, watches, "["+sess+"] [layP] [] [] [] [2]")
	// An extra window without a captured layout keeps its default split.
	assertNotContains(t, watches, "[3]")
}

func TestWrapperRestoreTabs_stamps_the_primary_windows_conversation(t *testing.T) {
	run := runWrapperWithStubs(t, wrapperStubOptions{queue: queueLine("sid-main", "", "0", "0")})
	lines := run.calls(t, `^tab_order_record `)
	batch := matching(lines, "tmux new-session ")
	if len(batch) != 1 {
		t.Fatalf("want one new-session batch, got %q", batch)
	}
	assertContains(t, batch[0], "; set-option -w @wd_claude_session sid-main ;")
}

func TestWrapperRestoreTabs_old_queue_line_adds_no_windows(t *testing.T) {
	run := runWrapperWithStubs(t, wrapperStubOptions{queue: "12345|PROJ|claude|sid-main|layP|acct|key\n"})
	lines := run.calls(t, `^restore_layout_watch .*\[layP\]`)
	for _, prefix := range []string{"tab_view_new_window", "tmux select-window", "tmux move-window"} {
		if got := matching(lines, prefix); len(got) != 0 {
			t.Fatalf("an old 7-field entry must not trigger %s: %q", prefix, got)
		}
	}
	assertContains(t, strings.Join(matching(lines, "restore_layout_watch "), "\n"), "[layP] [] [] [] [0]")
}

func TestWrapperRestoreTabs_fresh_launch_stamps_no_conversation(t *testing.T) {
	run := runWrapperWithStubs(t, wrapperStubOptions{withProject: true})
	lines := run.calls(t, `^tab_order_record `)
	assertNotContains(t, strings.Join(lines, "\n"), "@wd_claude_session")
}
