package tui

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jackuait/wisp-deck/internal/claudeaccount"
)

// ClaudeLoginFunc builds the sign-in process for one login: configDir is ""
// for the Default login, email prefills the sign-in page when not "".
type ClaudeLoginFunc func(configDir, email string) *exec.Cmd

// subscriptionReloginDoneMsg arrives when the sign-in process has exited and
// the menu has the terminal back.
type subscriptionReloginDoneMsg struct{ err error }

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
	cmd := m.claudeLogin(m.loginConfigDir(dir), m.loginPins[loginSlotKey(dir)])
	return tea.Exec(&loginExec{Cmd: cmd}, func(err error) tea.Msg {
		return subscriptionReloginDoneMsg{err: err}
	})
}

func (m *MainMenuModel) applySubscriptionReloginDone(msg subscriptionReloginDoneMsg) {
	m.refreshLoginIdentities()
	m.subscriptionModal.err = nil
	if msg.err != nil {
		m.subscriptionModal.err = errors.New("sign-in failed: " + msg.err.Error())
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
type loginExec struct{ *exec.Cmd }

func (c *loginExec) SetStdin(r io.Reader) { c.Stdin = r }

func (c *loginExec) SetStdout(w io.Writer) {
	c.Stdout = w
	c.Stderr = w
}

func (c *loginExec) SetStderr(io.Writer) {}
