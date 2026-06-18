package bash_test

import (
	"strings"
	"testing"
)

// format_file now shows the file BASENAME only (no parent path), truncating
// with an ellipsis when it exceeds the available width.

func TestFormatFile_strips_path_to_basename(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "format_file",
		[]string{"design/changeset.html", "30"}, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "changeset.html" {
		t.Errorf("got %q, want %q", got, "changeset.html")
	}
}

func TestFormatFile_strips_deep_path_to_basename(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "format_file",
		[]string{"internal/tui/compact-view.go", "30"}, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "compact-view.go" {
		t.Errorf("got %q, want %q", got, "compact-view.go")
	}
}

func TestFormatFile_short_name_unchanged(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "format_file",
		[]string{"x.go", "10"}, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "x.go" {
		t.Errorf("got %q, want %q", got, "x.go")
	}
}

func TestFormatFile_truncates_long_basename_with_ellipsis(t *testing.T) {
	// max=8 -> keep 7 chars + ellipsis
	out, code := runBashFunc(t, "lib/compact-view.sh", "format_file",
		[]string{"a/b/verylongfilename.go", "8"}, nil)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	if got != "verylon…" {
		t.Errorf("got %q, want %q", got, "verylon…")
	}
}

// sum_numstat totals the added/deleted columns of `git --numstat` output,
// treating binary markers ("-") as zero. Echoes "<added> <deleted>".

func TestSumNumstat_totals_columns(t *testing.T) {
	in := "142\t38\tinternal/tui/compact-view.go\n54\t29\tlib/tmux-session.sh\n"
	out, code := runBashFuncWithStdin(t, "lib/compact-view.sh", "sum_numstat",
		nil, nil, in)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "196 67" {
		t.Errorf("got %q, want %q", got, "196 67")
	}
}

func TestSumNumstat_treats_binary_as_zero(t *testing.T) {
	in := "-\t-\tassets/logo.png\n5\t3\tlib/x.sh\n"
	out, code := runBashFuncWithStdin(t, "lib/compact-view.sh", "sum_numstat",
		nil, nil, in)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "5 3" {
		t.Errorf("got %q, want %q", got, "5 3")
	}
}

func TestSumNumstat_empty_is_zero(t *testing.T) {
	out, code := runBashFuncWithStdin(t, "lib/compact-view.sh", "sum_numstat",
		nil, nil, "")
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "0 0" {
		t.Errorf("got %q, want %q", got, "0 0")
	}
}
