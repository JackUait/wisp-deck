package attention

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

// transcriptTailBytes bounds the first read of a transcript. A resumed
// conversation can be tens of megabytes, and only its last cwd matters.
const transcriptTailBytes = 1 << 20

// transcriptGlobInterval spaces out the search for a transcript that is not
// under the record's own project directory. The search lists every project.
const transcriptGlobInterval = 10 * time.Second

// TranscriptWorkdir reports the checkout a Claude session is working in.
//
// The registry record's cwd is the launch directory plus EnterWorktree and
// ExitWorktree. A Bash `cd` into another worktree also moves Claude Code's
// working directory, but only the transcript records it: every main-thread
// entry carries the cwd the session had when it was written.
//
// It runs on the 4Hz supervisor tick, so an unchanged transcript costs one
// stat, and a grown one is read from where the last read stopped.
type TranscriptWorkdir struct {
	ConfigDir string

	sessionID string
	path      string
	nextGlob  time.Time
	offset    int64
	pending   []byte
	cwd       string
	rootOf    string
	root      string
	onRead    func(int)
}

type transcriptEntry struct {
	Cwd         string `json:"cwd"`
	IsSidechain bool   `json:"isSidechain"`
}

// Resolve returns the checkout root holding the session's current directory:
// the transcript's latest main-thread cwd, or the record's when the transcript
// has none yet.
func (t *TranscriptWorkdir) Resolve(status ClaudeRegistryStatus) string {
	dir := status.Cwd
	if cwd := t.transcriptCwd(status); cwd != "" {
		dir = cwd
	}
	return t.checkoutRoot(dir)
}

func (t *TranscriptWorkdir) transcriptCwd(status ClaudeRegistryStatus) string {
	if t.ConfigDir == "" || status.SessionID == "" {
		return ""
	}
	if status.SessionID != t.sessionID {
		// cwd carries over: compaction replaces the id but not the directory,
		// and the record's cwd may still be the launch directory.
		t.sessionID, t.path, t.nextGlob, t.offset, t.pending = status.SessionID, "", time.Time{}, 0, nil
	}
	if t.path == "" {
		t.path = t.locate(status)
		if t.path == "" {
			return t.cwd
		}
	}
	info, err := os.Stat(t.path)
	if err != nil {
		t.path = ""
		return t.cwd
	}
	size := info.Size()
	if size < t.offset {
		// Rewritten in place: start over from its tail.
		t.offset, t.pending = 0, nil
	}
	if size == t.offset {
		return t.cwd
	}
	start := t.offset
	if start == 0 && size > transcriptTailBytes {
		start = size - transcriptTailBytes
	}
	file, err := os.Open(t.path)
	if err != nil {
		return t.cwd
	}
	defer file.Close()
	chunk := make([]byte, size-start)
	n, err := file.ReadAt(chunk, start)
	if err != nil && err != io.EOF {
		return t.cwd
	}
	chunk = chunk[:n]
	if t.onRead != nil {
		t.onRead(n)
	}
	if t.offset == 0 && start > 0 {
		// The tail starts mid-line; that fragment is not an entry.
		cut := bytes.IndexByte(chunk, '\n')
		if cut < 0 {
			t.offset = start + int64(n)
			return t.cwd
		}
		chunk = chunk[cut+1:]
	}
	t.offset = start + int64(n)
	data := append(t.pending, chunk...)
	last := bytes.LastIndexByte(data, '\n')
	if last < 0 {
		t.pending = data
		return t.cwd
	}
	t.pending = append([]byte(nil), data[last+1:]...)
	for _, line := range bytes.Split(data[:last], []byte{'\n'}) {
		if !bytes.Contains(line, []byte(`"cwd"`)) {
			continue
		}
		var entry transcriptEntry
		if json.Unmarshal(line, &entry) != nil || entry.IsSidechain || !filepath.IsAbs(entry.Cwd) {
			continue
		}
		t.cwd = entry.Cwd
	}
	return t.cwd
}

// locate finds <config>/projects/<project>/<session>.jsonl. Claude names the
// project after the directory the conversation started in, which is the
// record's cwd for a fresh session but not for a resumed one.
func (t *TranscriptWorkdir) locate(status ClaudeRegistryStatus) string {
	projects := filepath.Join(t.ConfigDir, "projects")
	name := status.SessionID + ".jsonl"
	if status.Cwd != "" {
		direct := filepath.Join(projects, claudeProjectDirName(status.Cwd), name)
		if _, err := os.Stat(direct); err == nil {
			return direct
		}
	}
	now := time.Now()
	if now.Before(t.nextGlob) {
		return ""
	}
	t.nextGlob = now.Add(transcriptGlobInterval)
	matches, _ := filepath.Glob(filepath.Join(projects, "*", name))
	if len(matches) != 1 {
		return ""
	}
	return matches[0]
}

// claudeProjectDirName is Claude Code's name for a project directory: every
// character but an ASCII letter or digit becomes '-'.
func claudeProjectDirName(dir string) string {
	out := []byte(dir)
	for i, c := range out {
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9') {
			out[i] = '-'
		}
	}
	return string(out)
}

// checkoutRoot walks up to the nearest directory holding .git, so a cd into a
// subdirectory names its checkout. A directory that is gone is returned as it
// is; the follow refuses it.
func (t *TranscriptWorkdir) checkoutRoot(dir string) string {
	if dir == "" || dir == t.rootOf {
		return t.root
	}
	root := dir
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		for candidate := dir; ; {
			if _, err := os.Lstat(filepath.Join(candidate, ".git")); err == nil {
				root = candidate
				break
			}
			parent := filepath.Dir(candidate)
			if parent == candidate {
				break
			}
			candidate = parent
		}
	}
	t.rootOf, t.root = dir, root
	return root
}
