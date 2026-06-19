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
	"os"
	"path/filepath"
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

// RewriteScreenshotPath rewrites a bracketed paste's content when it is the path
// to an ephemeral screencaptureui temp screenshot: it copies the file to a
// stable location and returns that (space-free) path. Anything else — a normal
// file path, or a temp path whose file is already gone — is returned unchanged,
// so the filter is never worse than passing the drop straight through.
func RewriteScreenshotPath(content []byte) []byte {
	path := unescape(string(content))
	if !isEphemeralScreenshot(path) {
		return content
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return content
	}
	stable, err := copyToStable(path)
	if err != nil {
		return content
	}
	return []byte(stable)
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
	if !strings.Contains(path, "/TemporaryItems/") || !strings.Contains(path, "screencaptureui") {
		return false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
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
