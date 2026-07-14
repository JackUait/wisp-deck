package ledger

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Popup opens a full-window file preview outside the ledger event loop.
type Popup interface {
	Open(context.Context, OpenRequest) (OpenResult, error)
}

// BackdropCache owns the latest prepared screen capture for popup margins.
type BackdropCache interface {
	Latest() (string, bool)
	Refresh(context.Context) error
	Close() error
}

// OpenRequest contains structured popup metadata. User-controlled fields are
// passed to tmux as environment entries, never interpolated into its program.
type OpenRequest struct {
	ProjectDir   string
	Path         string
	Tool         string
	Pane         string
	BackdropFile string
	GfxTTY       string
	Tracked      bool
	Image        bool
	Status       string
}

// OpenResult reports actions confirmed inside the popup.
type OpenResult struct {
	Discard bool
}

// ProcessRunner executes external commands without a shell at the Go boundary.
type ProcessRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

// ExecProcessRunner runs a process with an argv vector.
type ExecProcessRunner struct{}

// Run executes name directly and returns combined output on failure.
func (ExecProcessRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return out, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return out, ctxErr
	}
	return out, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
}

// ExecPopup launches the existing diff-view command in a tmux popup.
type ExecPopup struct {
	runner  ProcessRunner
	tempDir string
}

// NewExecPopup creates a popup adapter. tempDir owns transient decision files.
func NewExecPopup(runner ProcessRunner, tempDir string) *ExecPopup {
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	return &ExecPopup{runner: runner, tempDir: tempDir}
}

const ledgerPopupScript = `
set -- diff-view --ai-tool "$WISP_LEDGER_TOOL" --title "$WISP_LEDGER_PATH" --discard-file "$WISP_LEDGER_DECISION"
[ -n "$WISP_LEDGER_BACKDROP" ] && set -- "$@" --backdrop-file "$WISP_LEDGER_BACKDROP"
if [ "$WISP_LEDGER_IMAGE" = 1 ]; then
  set -- "$@" --image --status "$WISP_LEDGER_STATUS" --path "$WISP_LEDGER_PROJECT/$WISP_LEDGER_PATH"
  [ -n "$WISP_LEDGER_GFX_TTY" ] && set -- "$@" --gfx-tty "$WISP_LEDGER_GFX_TTY"
  cat -- "$WISP_LEDGER_PROJECT/$WISP_LEDGER_PATH" | wisp-deck-tui "$@"
else
  if [ "$WISP_LEDGER_TRACKED" = 1 ]; then
    git -C "$WISP_LEDGER_PROJECT" --no-pager diff HEAD -U999999 --color=never -- "$WISP_LEDGER_PATH"
  else
    git -C "$WISP_LEDGER_PROJECT" --no-pager diff --no-index -U999999 --color=never -- /dev/null "$WISP_LEDGER_PATH"
  fi | awk 'f;/@@/{f=1}' | wisp-deck-tui "$@"
fi
`

// Open blocks until tmux closes, but callers run it in a Tea command. Metadata
// travels through display-popup -e entries; the shell program remains fixed.
func (p *ExecPopup) Open(ctx context.Context, request OpenRequest) (OpenResult, error) {
	if p == nil || p.runner == nil {
		return OpenResult{}, fmt.Errorf("open popup: no process runner configured")
	}
	decision, err := os.CreateTemp(p.tempDir, "wisp-ledger-discard-*")
	if err != nil {
		return OpenResult{}, fmt.Errorf("create popup decision: %w", err)
	}
	decisionPath := decision.Name()
	if err := decision.Close(); err != nil {
		_ = os.Remove(decisionPath)
		return OpenResult{}, fmt.Errorf("close popup decision: %w", err)
	}
	defer os.Remove(decisionPath)

	tool := request.Tool
	if tool == "" {
		tool = "claude"
	}
	status := request.Status
	if status == "" {
		status = "modified"
	}
	gfxTTY := request.GfxTTY
	if request.Image && gfxTTY == "" {
		_, _ = p.runner.Run(ctx, "tmux", "set", "-g", "allow-passthrough", "all")
		pane := request.Pane
		if pane == "" {
			pane = os.Getenv("TMUX_PANE")
		}
		out, queryErr := p.runner.Run(ctx, "tmux", "display-message", "-p", "-t", pane, "#{client_tty}")
		if queryErr == nil {
			gfxTTY = strings.TrimSpace(string(out))
		}
	}

	bit := func(value bool) string {
		if value {
			return "1"
		}
		return "0"
	}
	environment := []string{
		"WISP_LEDGER_PROJECT=" + request.ProjectDir,
		"WISP_LEDGER_PATH=" + request.Path,
		"WISP_LEDGER_TOOL=" + tool,
		"WISP_LEDGER_BACKDROP=" + request.BackdropFile,
		"WISP_LEDGER_DECISION=" + decisionPath,
		"WISP_LEDGER_GFX_TTY=" + gfxTTY,
		"WISP_LEDGER_TRACKED=" + bit(request.Tracked),
		"WISP_LEDGER_IMAGE=" + bit(request.Image),
		"WISP_LEDGER_STATUS=" + status,
	}
	args := []string{"display-popup", "-E", "-B", "-w", "100%", "-h", "100%"}
	for _, value := range environment {
		args = append(args, "-e", value)
	}
	args = append(args, ledgerPopupScript)
	if _, err := p.runner.Run(ctx, "tmux", args...); err != nil {
		return OpenResult{}, fmt.Errorf("open tmux popup: %w", err)
	}
	decisionRaw, err := os.ReadFile(decisionPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return OpenResult{}, fmt.Errorf("read popup decision: %w", err)
	}
	return OpenResult{Discard: strings.TrimSpace(string(decisionRaw)) == "discard"}, nil
}

// FileBackdropCache atomically maintains one serialized tmux screen capture.
type FileBackdropCache struct {
	runner    ProcessRunner
	target    string
	refreshMu sync.Mutex
	mu        sync.RWMutex
	ready     bool
}

// NewFileBackdropCache creates a bounded single-file backdrop cache.
func NewFileBackdropCache(runner ProcessRunner, target string) *FileBackdropCache {
	return &FileBackdropCache{runner: runner, target: target}
}

// Latest returns the stable cache path after the first successful refresh.
func (c *FileBackdropCache) Latest() (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.target, c.ready
}

// Refresh captures every pane and atomically replaces the stable cache file.
func (c *FileBackdropCache) Refresh(ctx context.Context) error {
	if c == nil || c.runner == nil || c.target == "" {
		return fmt.Errorf("refresh backdrop: cache is not configured")
	}
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	dimensions, err := c.runner.Run(ctx, "tmux", "display-message", "-p", "-t", os.Getenv("TMUX_PANE"), "#{client_width} #{client_height}")
	if err != nil {
		return fmt.Errorf("capture backdrop dimensions: %w", err)
	}
	panes, err := c.runner.Run(ctx, "tmux", "list-panes", "-t", os.Getenv("TMUX_PANE"), "-F", "#{pane_id} #{pane_left} #{pane_top}")
	if err != nil {
		return fmt.Errorf("list backdrop panes: %w", err)
	}
	var backdrop strings.Builder
	backdrop.WriteString(strings.TrimSpace(string(dimensions)))
	backdrop.WriteByte('\n')
	for _, line := range strings.Split(strings.TrimSpace(string(panes)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		captured, captureErr := c.runner.Run(ctx, "tmux", "capture-pane", "-p", "-t", fields[0])
		if captureErr != nil {
			return fmt.Errorf("capture backdrop pane %s: %w", fields[0], captureErr)
		}
		fmt.Fprintf(&backdrop, "PANE %s %s\n", fields[1], fields[2])
		backdrop.Write(captured)
		if len(captured) > 0 && captured[len(captured)-1] != '\n' {
			backdrop.WriteByte('\n')
		}
		backdrop.WriteString("ENDPANE\n")
	}

	directory := filepath.Dir(c.target)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create backdrop directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, filepath.Base(c.target)+".*")
	if err != nil {
		return fmt.Errorf("create backdrop snapshot: %w", err)
	}
	temporaryPath := temporary.Name()
	keep := false
	defer func() {
		_ = temporary.Close()
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err := temporary.WriteString(backdrop.String()); err != nil {
		return fmt.Errorf("write backdrop snapshot: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close backdrop snapshot: %w", err)
	}
	if err := os.Rename(temporaryPath, c.target); err != nil {
		return fmt.Errorf("replace backdrop snapshot: %w", err)
	}
	keep = true
	c.mu.Lock()
	c.ready = true
	c.mu.Unlock()
	return nil
}

// Close removes the cache's owned snapshot.
func (c *FileBackdropCache) Close() error {
	if c == nil || c.target == "" {
		return nil
	}
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	err := os.Remove(c.target)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	c.mu.Lock()
	c.ready = false
	c.mu.Unlock()
	return err
}
