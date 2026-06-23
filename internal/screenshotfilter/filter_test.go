package screenshotfilter

import (
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// A Finder/Desktop drag in Ghostty delivers a bracketed paste of a percent-encoded
// file:// URL, NOT a plain path. Claude Code attaches a plain filesystem path but
// never a file:// URL (proven empirically), so the filter must decode the URL to the
// real local path. The name carries a U+202F narrow no-break space (how macOS names
// screenshots) plus regular spaces, so this also proves %E2%80%AF and %20 both decode.
func TestRewriteScreenshotPath_fileurl_existing_decodes_to_plain_path(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Screenshot 2026-06-20 at 1.38.03 PM.png")
	if err := os.WriteFile(src, []byte("PNGDATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	urlStr := (&url.URL{Scheme: "file", Path: src}).String()
	if !strings.HasPrefix(urlStr, "file://") || !strings.Contains(urlStr, "%20") {
		t.Fatalf("test setup: expected a percent-encoded file:// URL, got %q", urlStr)
	}

	out := string(RewriteScreenshotPath([]byte(urlStr)))

	if strings.HasPrefix(out, "file://") {
		t.Fatalf("file:// URL must be decoded (Claude won't attach a URL): got %q", out)
	}
	if out != src {
		t.Errorf("decoded path = %q, want the real local path %q", out, src)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("decoded path must resolve to the real file: %v", err)
	}
}

// A file:// URL whose file does not exist must pass through unchanged (no worse than
// today): we only rewrite when we can resolve a real local image.
func TestRewriteScreenshotPath_fileurl_missing_passthrough(t *testing.T) {
	in := "file:///no/such/dir/Screenshot%20gone.png"
	out := string(RewriteScreenshotPath([]byte(in)))
	if out != in {
		t.Errorf("missing file:// must pass through unchanged: got %q want %q", out, in)
	}
}

// A file:// URL pointing at an ephemeral screencaptureui temp file (which macOS
// deletes moments after the drop) must be copied to the stable, space-free dir and
// the path rewritten to the stable copy — same contract as the plain-path ephemeral
// case, but reached via a file:// URL.
func TestRewriteScreenshotPath_fileurl_ephemeral_copies_to_stable(t *testing.T) {
	root := t.TempDir()
	tempDir := filepath.Join(root, "TemporaryItems", "NSIRD_screencaptureui_ABC")
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(tempDir, "Screenshot 2026-06-20 at 2.04.15 PM.png")
	if err := os.WriteFile(src, []byte("PNGDATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	stash := filepath.Join(root, "stash")
	t.Setenv("GT_SCREENSHOT_STASH_DIR", stash)

	urlStr := (&url.URL{Scheme: "file", Path: src}).String()
	out := string(RewriteScreenshotPath([]byte(urlStr)))

	if !strings.HasPrefix(out, stash) {
		t.Fatalf("ephemeral file:// should copy to stash %q; got %q", stash, out)
	}
	if strings.Contains(out, " ") {
		t.Errorf("stable path should be space-free: %q", out)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("stable copy unreadable: %v", err)
	}
	if string(data) != "PNGDATA" {
		t.Errorf("stable copy content = %q, want PNGDATA", data)
	}
}

// Claude/OpenCode can't attach a video as an image; instead the agent is handed a
// plain path to the stored file and splits frames itself. isVideoPath gates that.
func TestIsVideoPath(t *testing.T) {
	cases := map[string]bool{
		"/x/clip.mov": true, "/x/clip.MOV": true, "/x/a.mp4": true,
		"/x/a.m4v": true, "/x/a.webm": true, "/x/a.mkv": true,
		"/x/shot.png": false, "/x/doc.pdf": false, "/x/a.txt": false, "/x/noext": false,
	}
	for p, want := range cases {
		if got := isVideoPath(p); got != want {
			t.Errorf("isVideoPath(%q) = %v, want %v", p, got, want)
		}
	}
}

// A dropped video (delivered as a file:// URL, like a Finder drag) must be copied into
// the stable, space-free store and the paste rewritten to that plain path — no frame
// extraction. The original may be ephemeral or later moved, so the copy is what keeps
// the video available; the path handed over must be a clean, non-corrupted link (not a
// file:// URL, not space-escaped).
func TestRewriteScreenshotPath_video_stashes_and_returns_plain_path(t *testing.T) {
	root := t.TempDir()
	stash := filepath.Join(root, "stash")
	t.Setenv("GT_SCREENSHOT_STASH_DIR", stash)

	src := filepath.Join(root, "Screen Recording 2026-06-23 at 1.02.03 PM.mov")
	if err := os.WriteFile(src, []byte("MOVDATA"), 0o644); err != nil {
		t.Fatal(err)
	}

	urlStr := (&url.URL{Scheme: "file", Path: src}).String()
	out := string(RewriteScreenshotPath([]byte(urlStr)))

	if strings.HasPrefix(out, "file://") {
		t.Fatalf("video file:// URL must be decoded to a plain path, got %q", out)
	}
	if !strings.HasPrefix(out, stash) {
		t.Fatalf("video should be stored under stash %q, got %q", stash, out)
	}
	if strings.Contains(out, " ") {
		t.Errorf("stored video path should be space-free (clean link): %q", out)
	}
	if !strings.HasSuffix(strings.ToLower(out), ".mov") {
		t.Errorf("stored video must keep its extension: %q", out)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("stored video unreadable: %v", err)
	}
	if string(data) != "MOVDATA" {
		t.Errorf("stored video content = %q, want MOVDATA", data)
	}
}

// A plain-path video drop (floating-thumbnail / escaped spaces) is stored just the
// same as the file:// case.
func TestRewriteScreenshotPath_video_plain_path_stashes(t *testing.T) {
	root := t.TempDir()
	stash := filepath.Join(root, "stash")
	t.Setenv("GT_SCREENSHOT_STASH_DIR", stash)

	src := filepath.Join(root, "clip.mp4")
	if err := os.WriteFile(src, []byte("MP4DATA"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := string(RewriteScreenshotPath([]byte(src)))
	if !strings.HasPrefix(out, stash) {
		t.Fatalf("plain video path should be stored under stash %q, got %q", stash, out)
	}
	data, err := os.ReadFile(out)
	if err != nil || string(data) != "MP4DATA" {
		t.Fatalf("stored video content = %q (err %v), want MP4DATA", data, err)
	}
}

// A video path whose file does not exist must pass through unchanged — never worse
// than handing the drop straight to the agent.
func TestRewriteScreenshotPath_video_missing_passthrough(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GT_SCREENSHOT_STASH_DIR", filepath.Join(dir, "stash"))
	in := filepath.Join(dir, "gone.mov")
	out := string(RewriteScreenshotPath([]byte(in)))
	if out != in {
		t.Errorf("missing video must pass through unchanged: got %q want %q", out, in)
	}
}

// A zero-byte video file carries nothing to store, so it passes through unchanged
// rather than producing a useless empty copy.
func TestRewriteScreenshotPath_empty_video_passthrough(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GT_SCREENSHOT_STASH_DIR", filepath.Join(dir, "stash"))
	src := filepath.Join(dir, "empty.mov")
	if err := os.WriteFile(src, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	out := string(RewriteScreenshotPath([]byte(src)))
	if out != src {
		t.Errorf("empty video must pass through unchanged: got %q want %q", out, src)
	}
}

// Stored videos are guaranteed available for the retention window (10h by default) and
// no longer: storing a new video sweeps away earlier stored videos whose modtime is
// past the window, while recent ones survive. Other stash files (screenshots) are left
// untouched.
func TestStashVideo_sweeps_expired_videos(t *testing.T) {
	root := t.TempDir()
	stash := filepath.Join(root, "stash")
	if err := os.MkdirAll(stash, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GT_SCREENSHOT_STASH_DIR", stash)
	t.Setenv("GT_VIDEO_RETENTION_HOURS", "10")

	old := filepath.Join(stash, "gt-vid-1.mov")
	recent := filepath.Join(stash, "gt-vid-2.mov")
	shot := filepath.Join(stash, "gt-shot-3.png")
	for _, p := range []string{old, recent, shot} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	staleTime := time.Now().Add(-11 * time.Hour)
	if err := os.Chtimes(old, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(shot, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(root, "new.mov")
	if err := os.WriteFile(src, []byte("NEW"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := stashVideo(src); err != nil {
		t.Fatalf("stashVideo: %v", err)
	}

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("expired stored video should be swept, but it survives")
	}
	if _, err := os.Stat(recent); err != nil {
		t.Errorf("recent stored video must survive: %v", err)
	}
	if _, err := os.Stat(shot); err != nil {
		t.Errorf("non-video stash file (screenshot) must not be swept: %v", err)
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
