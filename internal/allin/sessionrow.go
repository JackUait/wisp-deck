package allin

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SessionHeader is the header Claude Code puts on every API request, naming
// the conversation the request belongs to.
const SessionHeader = "X-Claude-Code-Session-Id"

var (
	sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,128}$`)
	// The row becomes ANTHROPIC_MODEL on the next launch, so only characters a
	// picker id uses are stored. The brackets carry OneMillionMarker.
	rowPattern = regexp.MustCompile(`^[A-Za-z0-9._:/\[\]-]{1,256}$`)
)

// SessionRows remembers the row each conversation last ran on.
//
// Claude Code saves every /model pick to the shared settings.json and resumes a
// conversation on that value, not on the row the conversation used. Every
// All-In pane shares that file, so a relaunched pane would land on whatever
// row another pane picked last, and spend another subscription.
type SessionRows struct {
	Dir string
}

// SessionRowsDir is the store beside the accounts list. With no list it is
// "", which stores nothing: a relative dir would land in the pane's cwd.
func SessionRowsDir(accountsList string) string {
	if accountsList == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(accountsList), "allin-session-rows")
}

// Record saves model as the session's row. The write is a rename, so a reader
// never sees half a value.
func (s SessionRows) Record(session, model string) error {
	if s.Dir == "" || !sessionIDPattern.MatchString(session) {
		return errors.New("allin: not a session id")
	}
	if !rowPattern.MatchString(model) || model == FastModel {
		return errors.New("allin: not a model id")
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, ".row-*")
	if err != nil {
		return err
	}
	_, writeErr := tmp.WriteString(model + "\n")
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(tmp.Name())
		return errors.Join(writeErr, closeErr)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(s.Dir, session)); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// Lookup returns the session's row, or "" when there is none to trust.
func (s SessionRows) Lookup(session string) string {
	if s.Dir == "" || !sessionIDPattern.MatchString(session) {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(s.Dir, session))
	if err != nil {
		return ""
	}
	model := strings.TrimSpace(string(data))
	if !rowPattern.MatchString(model) || model == FastModel {
		return ""
	}
	return model
}

// StartingRowFor is the row background calls use before the first turn: the
// session's own row when it has one, else the shared settings model, which
// is what a new conversation opens on.
func StartingRowFor(rows SessionRows, session, settingsPath string) Target {
	if model := rows.Lookup(session); model != "" {
		return Route(model)
	}
	return StartingRow(settingsPath)
}
