package tui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jackuait/wisp-deck/internal/claudeaccount"
)

// ClaudeLoginFunc builds the sign-in process for one login: configDir is ""
// for the Default login, email prefills the sign-in page when not "".
type ClaudeLoginFunc func(configDir, email string) *exec.Cmd

// subscriptionReloginDoneMsg arrives when the sign-in process has exited and
// the menu has the terminal back.
type subscriptionReloginDoneMsg struct {
	err    error
	detail string // last line the sign-in printed
}

// SetClaudeLogin wires the sign-in runner. Left unset (tests, builds that may
// not touch the host), the action reports it is unavailable.
func (m *MainMenuModel) SetClaudeLogin(fn ClaudeLoginFunc) { m.claudeLogin = fn }

// loginSlotKey is the key a login is pinned under in claude-account-emails.
func loginSlotKey(dir string) string {
	if dir == "" {
		return "default"
	}
	return dir
}

func (m *MainMenuModel) loginConfigDir(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(m.claudeAccountsDir, dir)
}

// loginStateFile is the .claude.json that records the login's account.
func (m *MainMenuModel) loginStateFile(dir string) string {
	if dir == "" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".claude.json")
	}
	return filepath.Join(m.claudeAccountsDir, dir, ".claude.json")
}

// refreshLoginIdentities reads each login's account and pin once, so the
// details pane never parses a large .claude.json per frame.
func (m *MainMenuModel) refreshLoginIdentities() {
	m.loginEmails = map[string]string{}
	m.loginPins = map[string]string{}
	if m.claudeAccountsList != "" {
		m.loginPins = claudeaccount.LoadEmails(filepath.Join(filepath.Dir(m.claudeAccountsList), "claude-account-emails"))
	}
	for _, row := range m.subscriptionLoginRows() {
		m.loginEmails[loginSlotKey(row.Dir)] = claudeaccount.StateEmail(m.loginStateFile(row.Dir))
	}
}

// startSubscriptionRelogin hands the terminal to a fresh sign-in under the
// login's own config dir.
func (m *MainMenuModel) startSubscriptionRelogin() tea.Cmd {
	rows := m.subscriptionLoginRows()
	idx := m.subscriptionModalLoginIndex()
	if idx < 0 || idx >= len(rows) {
		return nil
	}
	if m.claudeLogin == nil {
		m.subscriptionModal.err = errors.New("sign-in is not available from this menu")
		return nil
	}
	m.subscriptionModal.detailCursor = subscriptionDetailRelogin
	m.subscriptionModal.err = nil
	dir := rows[idx].Dir
	login := &loginExec{Cmd: m.claudeLogin(m.loginConfigDir(dir), m.loginPins[loginSlotKey(dir)])}
	return tea.Exec(login, func(err error) tea.Msg {
		return subscriptionReloginDoneMsg{err: err, detail: login.lastLine()}
	})
}

func (m *MainMenuModel) applySubscriptionReloginDone(msg subscriptionReloginDoneMsg) {
	m.refreshLoginIdentities()
	m.subscriptionModal.err = nil
	if msg.err != nil {
		reason := msg.err.Error()
		if msg.detail != "" {
			reason = msg.detail + " (" + reason + ")"
		}
		m.subscriptionModal.err = errors.New("sign-in failed: " + reason)
	}
}

// loginMismatch is the pinned email when the login holds another account.
func (m *MainMenuModel) loginMismatch(dir string) string {
	key := loginSlotKey(dir)
	pin, email := m.loginPins[key], m.loginEmails[key]
	if pin == "" || email == "" || strings.EqualFold(pin, email) {
		return ""
	}
	return pin
}

// loginExec gives the sign-in the terminal on stderr as well. Bubble Tea
// passes the menu's own stderr, which the launcher captures to a file.
// On a failure it waits for Enter: the menu redraws over the terminal the
// moment Run returns, which would wipe the reason.
type loginExec struct {
	*exec.Cmd
	in   io.Reader
	out  io.Writer
	tail bytes.Buffer
}

func (c *loginExec) SetStdin(r io.Reader) {
	c.in = r
	c.Stdin = r
}

func (c *loginExec) SetStdout(w io.Writer) {
	c.out = w
	tee := io.MultiWriter(w, &c.tail)
	c.Stdout = tee
	c.Stderr = tee
}

func (c *loginExec) SetStderr(io.Writer) {}

func (c *loginExec) Run() error {
	err := c.Cmd.Run()
	if err != nil && c.out != nil && c.in != nil {
		fmt.Fprintf(c.out, "\r\nSign-in failed (%v). Press Enter to return to the menu.", err)
		buf := make([]byte, 1)
		for {
			if n, rerr := c.in.Read(buf); rerr != nil || (n == 1 && (buf[0] == '\n' || buf[0] == '\r')) {
				break
			}
		}
	}
	return err
}

// lastLine is the last non-empty line the sign-in printed, without escapes.
func (c *loginExec) lastLine() string {
	text := ansiEscape.ReplaceAllString(c.tail.String(), "")
	lines := strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' })
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)
