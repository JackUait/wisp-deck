package attention

import (
	"errors"
	"path/filepath"
	"sync"
)

// WorkingDirectoryName is the generation-local file naming the directory the
// supervised session is currently working in. It is a sidecar rather than a
// field of the attention state because that record is a fixed five-field
// versioned protocol every consumer pins — and because the two answer different
// questions: attention is a semantic phase, this is a location.
const WorkingDirectoryName = "cwd"

// WorkingDirectoryWriter publishes the supervised session's working directory
// beside its generation's attention state. The wisp session follows its agent
// into a git worktree by reading this file, so it is written by rename (a
// half-written path names a directory nobody is in) and is fenced by living
// inside the generation directory: a rotated generation is stale, never
// recreated.
type WorkingDirectoryWriter struct {
	mu   sync.Mutex
	path string
	last string
}

// NewWorkingDirectoryWriter targets the sidecar belonging to stateFile's
// generation.
func NewWorkingDirectoryWriter(stateFile string) *WorkingDirectoryWriter {
	if stateFile == "" {
		return &WorkingDirectoryWriter{}
	}
	return &WorkingDirectoryWriter{
		path: filepath.Join(filepath.Dir(stateFile), WorkingDirectoryName),
	}
}

// Publish records a move. An empty directory is the shape a transient registry
// miss takes, so it leaves the last known answer standing rather than blanking
// it — a reader treats absence as "no answer yet", and blanking would look like
// a move back to a checkout the agent never left.
func (w *WorkingDirectoryWriter) Publish(dir string) error {
	if w == nil || w.path == "" {
		return errors.New("attention working directory path is empty")
	}
	if dir == "" {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if dir == w.last {
		return nil
	}
	if err := atomicReplace(w.path, []byte(dir+"\n")); err != nil {
		return err
	}
	w.last = dir
	return nil
}
