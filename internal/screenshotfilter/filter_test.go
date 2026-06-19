package screenshotfilter

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A bracketed paste whose content is an ephemeral screencaptureui temp screenshot
// must be copied to the stable dir and the path rewritten to the stable copy.
func TestRewriteScreenshotPath_ephemeral_copies_and_rewrites(t *testing.T) {
	root := t.TempDir()
	tempDir := filepath.Join(root, "TemporaryItems", "NSIRD_screencaptureui_ABC")
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(tempDir, "Screenshot 2026-06-19 at 2.12.29 PM.png")
	if err := os.WriteFile(src, []byte("PNGDATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	stash := filepath.Join(root, "stash")
	t.Setenv("GT_SCREENSHOT_STASH_DIR", stash)

	// Ghostty escapes spaces as "\ "; the filter must unescape to find the file.
	escaped := strings.ReplaceAll(src, " ", `\ `)
	out := string(RewriteScreenshotPath([]byte(escaped)))

	if !strings.HasPrefix(out, stash) {
		t.Fatalf("rewritten path %q should live under stash %q", out, stash)
	}
	if strings.Contains(out, " ") {
		t.Errorf("stable path should be space-free (no escaping needed): %q", out)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("stable copy unreadable: %v", err)
	}
	if string(data) != "PNGDATA" {
		t.Errorf("stable copy content = %q, want PNGDATA", data)
	}
	if !strings.HasSuffix(strings.ToLower(out), ".png") {
		t.Errorf("stable path must keep image extension: %q", out)
	}
}

// A normal (non-ephemeral) path must pass through unchanged — the filter only
// touches the doomed temp-file case.
func TestRewriteScreenshotPath_nonephemeral_passthrough(t *testing.T) {
	in := `/Users/x/Desktop/Screenshot.png`
	out := string(RewriteScreenshotPath([]byte(in)))
	if out != in {
		t.Errorf("non-ephemeral path changed: got %q want %q", out, in)
	}
}

// An ephemeral-looking path whose file is already gone must pass through
// unchanged (no worse than today).
func TestRewriteScreenshotPath_missing_file_passthrough(t *testing.T) {
	in := `/var/folders/x/T/TemporaryItems/NSIRD_screencaptureui_X/gone.png`
	out := string(RewriteScreenshotPath([]byte(in)))
	if out != in {
		t.Errorf("missing-file path changed: got %q want %q", out, in)
	}
}

func upper(b []byte) []byte { return bytes.ToUpper(b) }

// Normal keystrokes pass through untouched.
func TestFilter_passthrough(t *testing.T) {
	f := &Filter{Rewrite: upper}
	if got := f.Process([]byte("hello")); string(got) != "hello" {
		t.Errorf("got %q want hello", got)
	}
}

// A whole bracketed paste is rewritten via Rewrite, markers preserved.
func TestFilter_rewrites_bracketed_paste(t *testing.T) {
	f := &Filter{Rewrite: upper}
	got := f.Process([]byte("\x1b[200~hi there\x1b[201~"))
	want := "\x1b[200~HI THERE\x1b[201~"
	if string(got) != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// The start marker split across two reads must not corrupt the stream.
func TestFilter_split_start_marker(t *testing.T) {
	f := &Filter{Rewrite: upper}
	var out []byte
	out = append(out, f.Process([]byte("ab\x1b[20"))...) // partial start marker held back
	out = append(out, f.Process([]byte("0~hi\x1b[201~"))...)
	want := "ab\x1b[200~HI\x1b[201~"
	if string(out) != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// The end marker split across two reads must still rewrite correctly.
func TestFilter_split_end_marker(t *testing.T) {
	f := &Filter{Rewrite: upper}
	var out []byte
	out = append(out, f.Process([]byte("\x1b[200~hi\x1b[20"))...)
	out = append(out, f.Process([]byte("1~rest"))...)
	want := "\x1b[200~HI\x1b[201~rest"
	if string(out) != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// Text before and after a paste is preserved.
func TestFilter_text_around_paste(t *testing.T) {
	f := &Filter{Rewrite: upper}
	got := f.Process([]byte("x\x1b[200~hi\x1b[201~y"))
	want := "x\x1b[200~HI\x1b[201~y"
	if string(got) != want {
		t.Errorf("got %q want %q", got, want)
	}
}
