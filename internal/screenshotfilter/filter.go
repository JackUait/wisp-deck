// Package screenshotfilter rewrites a dragged macOS screenshot's path mid-stream
// so the literal drag-and-drop into the AI pane works.
//
// When a screenshot's floating thumbnail is dragged into the terminal, the drop
// delivers (as a bracketed paste) the path to the screencaptureui *temp* file in
// .../TemporaryItems/NSIRD_screencaptureui_*/. macOS deletes that temp file the
// moment the thumbnail finalizes, so by the time the AI tool reads the path the
// file is gone and nothing attaches. This filter sits in the AI tool's input
// stream, spots such a paste, copies the file to a stable location while it
// still exists, and rewrites the pasted path to the stable copy. Everything else
// passes through untouched, so it is transparent when no screenshot is dropped.
package screenshotfilter

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
	// maxPaste guards against buffering an unterminated paste forever; past this
	// the buffer is flushed through as-is.
	maxPaste = 1 << 16
)

// Filter is a streaming byte filter for terminal input. Feed it bytes via
// Process; it forwards everything unchanged except a bracketed paste, whose
// inner content it passes through Rewrite.
type Filter struct {
	inPaste bool
	buf     []byte
	// Rewrite maps a bracketed paste's inner content to its replacement.
	Rewrite func([]byte) []byte
}

// New returns a Filter that rewrites ephemeral screenshot paths.
func New() *Filter { return &Filter{Rewrite: RewriteScreenshotPath} }

// Process consumes input bytes and returns the bytes to forward downstream.
// Bytes may be held back (when a marker is split across reads, or while a paste
// body is still arriving) and emitted on a later call.
func (f *Filter) Process(p []byte) []byte {
	f.buf = append(f.buf, p...)
	var out []byte
	for {
		if !f.inPaste {
			if i := bytes.Index(f.buf, []byte(pasteStart)); i >= 0 {
				out = append(out, f.buf[:i]...)
				f.buf = append([]byte(nil), f.buf[i+len(pasteStart):]...)
				f.inPaste = true
				continue
			}
			// No full start marker: emit everything except a possible trailing
			// partial of the marker (it may complete on the next read).
			keep := partialSuffix(f.buf, []byte(pasteStart))
			out = append(out, f.buf[:len(f.buf)-keep]...)
			f.buf = append([]byte(nil), f.buf[len(f.buf)-keep:]...)
			return out
		}
		if j := bytes.Index(f.buf, []byte(pasteEnd)); j >= 0 {
			out = append(out, []byte(pasteStart)...)
			out = append(out, f.Rewrite(f.buf[:j])...)
			out = append(out, []byte(pasteEnd)...)
			f.buf = append([]byte(nil), f.buf[j+len(pasteEnd):]...)
			f.inPaste = false
			continue
		}
		// End marker not seen yet; keep buffering unless it grows pathological.
		if len(f.buf) > maxPaste {
			out = append(out, []byte(pasteStart)...)
			out = append(out, f.buf...)
			f.buf = nil
			f.inPaste = false
		}
		return out
	}
}

// partialSuffix returns the length of the longest suffix of b that is a proper
// prefix of needle, so a marker split across reads isn't emitted prematurely.
func partialSuffix(b, needle []byte) int {
	max := len(needle) - 1
	if max > len(b) {
		max = len(b)
	}
	for n := max; n > 0; n-- {
		if bytes.Equal(b[len(b)-n:], needle[:n]) {
			return n
		}
	}
	return 0
}

// RewriteScreenshotPath rewrites a bracketed paste's content into a path Claude
// will attach as an image. Two drop shapes reach here:
//   - Finder/Desktop drags deliver a percent-encoded file:// URL. Claude attaches a
//     plain filesystem path but NEVER a file:// URL, so we decode the URL first.
//   - Floating-thumbnail drags deliver a plain (often backslash-escaped) path.
//
// In both cases an ephemeral screencaptureui temp file is copied to a stable
// location (macOS deletes it moments after the drop); a persistent file is handed
// over as its plain path. A dropped video (which Claude/OpenCode cannot attach as an
// image) is copied into the stable store so it stays available for the retention
// window, and the paste is rewritten to that clean plain path for the agent to read.
// Anything we can't resolve to a real local image or video — a normal non-media
// paste, or a temp path whose file is already gone — is returned unchanged, so the
// filter is never worse than passing the drop straight through.
func RewriteScreenshotPath(content []byte) []byte {
	path, ok := fileURLToPath(string(content))
	if !ok {
		path = unescape(string(content))
	}
	if payload := resolveLocalVideo(path); payload != nil {
		return payload
	}
	return resolveLocalImage(path, content)
}

// resolveLocalImage returns the bytes to hand Claude for a dragged image at `path`,
// or `orig` unchanged when it can't resolve a real, on-disk image file.
func resolveLocalImage(path string, orig []byte) []byte {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || !isImagePath(path) {
		return orig
	}
	if isEphemeralScreenshot(path) {
		if stable, err := copyToStable(path); err == nil {
			return []byte(stable)
		}
		return orig
	}
	return []byte(path)
}

// fileURLToPath converts a dragged file:// URL (Finder/Desktop drags deliver these,
// percent-encoded) to a local filesystem path. Returns ok=false when content is not
// a file:// URL, so the caller falls back to plain-path handling.
func fileURLToPath(content string) (string, bool) {
	s := strings.TrimSpace(content)
	if !strings.HasPrefix(s, "file://") {
		return "", false
	}
	u, err := url.Parse(s)
	if err != nil || u.Path == "" {
		return "", false
	}
	return u.Path, true
}

// unescape undoes shell backslash-escaping (Ghostty escapes spaces as "\ ").
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isEphemeralScreenshot(path string) bool {
	return strings.Contains(path, "/TemporaryItems/") &&
		strings.Contains(path, "screencaptureui") && isImagePath(path)
}

func isImagePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

func isVideoPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mov", ".mp4", ".m4v", ".webm", ".mkv", ".avi":
		return true
	}
	return false
}

// resolveLocalVideo handles a dropped video. Claude/OpenCode can't attach a video as
// an image, but the agent can read the file (and split frames itself) when handed a
// clean path. So the video is copied into the stable store — guaranteeing it stays
// available for the retention window even if the original is ephemeral or later moved
// — and that space-free path is returned. Returns nil when `path` is not an existing,
// non-empty video file, so the caller falls back to image handling / passthrough.
func resolveLocalVideo(path string) []byte {
	if !isVideoPath(path) {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return nil
	}
	stable, err := stashVideo(path)
	if err != nil {
		return nil
	}
	return []byte(stable)
}

// videoRetentionHours is how long a stored video is guaranteed to remain available.
// The dropped path is handed to the agent as text and may not be processed until later
// in a long session, so the copy must outlive the original. Overridable via
// GT_VIDEO_RETENTION_HOURS.
const videoRetentionHours = 10

func videoRetention() time.Duration {
	if v := os.Getenv("GT_VIDEO_RETENTION_HOURS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Hour
		}
	}
	return videoRetentionHours * time.Hour
}

// stashVideo copies src into the stable dir under a space-free name (so the rewritten
// path needs no shell escaping) and returns that path. It first sweeps stored videos
// past the retention window, bounding disk use while keeping recent drops available.
func stashVideo(src string) (string, error) {
	dir := StableDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	sweepExpiredVideos(dir)
	dest := filepath.Join(dir, fmt.Sprintf("gt-vid-%d%s",
		time.Now().UnixNano(), strings.ToLower(filepath.Ext(src))))
	if err := copyFile(src, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// sweepExpiredVideos removes stored videos (gt-vid-*) whose modtime is past the
// retention window. Only video copies are touched — screenshot stashes are left alone.
func sweepExpiredVideos(dir string) {
	cutoff := time.Now().Add(-videoRetention())
	matches, err := filepath.Glob(filepath.Join(dir, "gt-vid-*"))
	if err != nil {
		return
	}
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && !info.IsDir() && info.ModTime().Before(cutoff) {
			os.Remove(m)
		}
	}
}

// copyFile streams src to dest (videos can be large, so it avoids reading the whole
// file into memory).
func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// StableDir is where ephemeral screenshots are copied. Matches the bash side's
// gt_stable_screenshot_dir; overridable via GT_SCREENSHOT_STASH_DIR.
func StableDir() string {
	if d := os.Getenv("GT_SCREENSHOT_STASH_DIR"); d != "" {
		return d
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".local", "share")
	}
	return filepath.Join(base, "ghost-tab", "screenshots")
}

func copyToStable(src string) (string, error) {
	dir := StableDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	// Space-free name so the rewritten path needs no shell escaping downstream.
	dest := filepath.Join(dir, fmt.Sprintf("gt-shot-%d%s",
		time.Now().UnixNano(), strings.ToLower(filepath.Ext(src))))
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return "", err
	}
	return dest, nil
}
