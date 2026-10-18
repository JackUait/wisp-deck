package tui

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/creack/pty"

	"github.com/jackuait/wisp-deck/internal/claudeaccount"
)

// ClaudeLoginFunc builds the sign-in process for one login: configDir is ""
// for the Default login, email prefills the sign-in page when not "".
type ClaudeLoginFunc func(configDir, email string) *exec.Cmd

// SetClaudeLogin wires the sign-in builder. Left unset (tests, builds that may
// not touch the host), the action reports it is unavailable.
func (m *MainMenuModel) SetClaudeLogin(fn ClaudeLoginFunc) { m.claudeLogin = fn }

// claudeSignIn is a running `claude auth login`.
type claudeSignIn interface {
	Submit(code string) error
	Cancel()
}

// signInEvent is one thing the sign-in printed or did. done is last.
type signInEvent struct {
	url    string // the manual sign-in link, once
	prompt bool   // the paste-code prompt appeared
	line   string // newest non-empty output line
	done   bool
	err    error
}

type subscriptionSignInMsg struct {
	generation uint64
	event      signInEvent
	events     <-chan signInEvent
}

type subscriptionSignInState struct {
	generation uint64
	handle     claudeSignIn
	label      string
	email      string
	url        string
	submitted  bool
	lastLine   string
	input      textinput.Model
}

// signInTimeout ends a sign-in nobody finished. claude ignores Ctrl-C at its
// paste prompt, so a forgotten one would otherwise run until the menu exits.
const signInTimeout = 10 * time.Minute

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

// loginMismatch is the pinned email when the login holds another account.
func (m *MainMenuModel) loginMismatch(dir string) string {
	key := loginSlotKey(dir)
	pin, email := m.loginPins[key], m.loginEmails[key]
	if pin == "" || email == "" || strings.EqualFold(pin, email) {
		return ""
	}
	return pin
}

// startSubscriptionRelogin runs a fresh sign-in under the login's own config
// dir and shows it inside the modal.
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
	m.cancelSubscriptionSignIn()
	m.subscriptionModal.detailCursor = subscriptionDetailRelogin
	m.subscriptionModal.err = nil
	dir := rows[idx].Dir
	email := m.loginPins[loginSlotKey(dir)]
	start := m.startSignIn
	if start == nil {
		start = startPTYSignIn
	}
	handle, events, err := start(m.claudeLogin(m.loginConfigDir(dir), email))
	if err != nil {
		m.subscriptionModal.err = errors.New("sign-in could not start: " + err.Error())
		return nil
	}
	input := textinput.New()
	input.Width = 36
	input.Placeholder = "code from the browser page"
	input.Focus()
	state := &m.subscriptionModal.signIn
	*state = subscriptionSignInState{
		generation: state.generation + 1,
		handle:     handle,
		label:      rows[idx].Label,
		email:      email,
		input:      input,
	}
	m.subscriptionModal.mode = subscriptionLoginSignIn
	m.subscriptionModal.pane = subscriptionDetailsPane
	m.subscriptionModal.lifecycleCursor = subscriptionLifecycleConfirm
	return tea.Batch(textinput.Blink, waitSubscriptionSignIn(state.generation, events))
}

func waitSubscriptionSignIn(generation uint64, events <-chan signInEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			ev = signInEvent{done: true, err: errors.New("sign-in ended unexpectedly")}
		}
		return subscriptionSignInMsg{generation: generation, event: ev, events: events}
	}
}

func (m *MainMenuModel) applySubscriptionSignIn(msg subscriptionSignInMsg) tea.Cmd {
	state := &m.subscriptionModal.signIn
	var next tea.Cmd
	if msg.events != nil && !msg.event.done {
		// Keep draining a stale sign-in too, so its reader never blocks.
		next = waitSubscriptionSignIn(msg.generation, msg.events)
	}
	if msg.generation != state.generation || state.handle == nil {
		return next
	}
	ev := msg.event
	if ev.url != "" {
		state.url = ev.url
	}
	if ev.line != "" {
		state.lastLine = ev.line
	}
	if ev.prompt && state.submitted {
		// A second prompt means the code was not accepted.
		state.submitted = false
	}
	if !ev.done {
		return next
	}
	state.handle = nil
	m.subscriptionModal.mode = subscriptionBrowse
	m.refreshLoginIdentities()
	m.subscriptionModal.err = nil
	if ev.err != nil {
		reason := ev.err.Error()
		if state.lastLine != "" {
			reason = state.lastLine + " (" + reason + ")"
		}
		m.subscriptionModal.err = errors.New("sign-in failed: " + reason)
	}
	return nil
}

// cancelSubscriptionSignIn kills a running sign-in. Every modal close path
// reaches it through cancelSubscriptionAuth.
func (m *MainMenuModel) cancelSubscriptionSignIn() {
	state := &m.subscriptionModal.signIn
	if state.handle != nil {
		state.handle.Cancel()
		state.handle = nil
	}
	state.generation++
	if m.subscriptionModal.mode == subscriptionLoginSignIn {
		m.subscriptionModal.mode = subscriptionBrowse
	}
}

// updateSubscriptionSignIn sends every key to the code input, so a pasted
// code can never fire a browse shortcut (d deletes, x disables, u switches).
func (m *MainMenuModel) updateSubscriptionSignIn(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	state := &m.subscriptionModal.signIn
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.cancelSubscriptionSignIn()
		return m, nil
	case tea.KeyLeft, tea.KeyRight:
		if state.input.Value() == "" {
			delta := 1
			if msg.Type == tea.KeyLeft {
				delta = -1
			}
			m.moveSubscriptionLifecycleAction(delta)
			return m, nil
		}
	case tea.KeyEnter:
		if m.subscriptionModal.lifecycleCursor == subscriptionLifecycleCancel {
			m.cancelSubscriptionSignIn()
			return m, nil
		}
		code := strings.TrimSpace(state.input.Value())
		if code == "" || state.handle == nil {
			return m, nil
		}
		if err := state.handle.Submit(code); err != nil {
			m.subscriptionModal.err = err
			return m, nil
		}
		state.submitted = true
		state.input.SetValue("")
		return m, nil
	}
	var cmd tea.Cmd
	state.input, cmd = state.input.Update(msg)
	return m, cmd
}

// subscriptionSignInLines is the in-modal sign-in panel.
func (m *MainMenuModel) subscriptionSignInLines(width int, buttons func(string, lipgloss.Style, string) string) []string {
	state := &m.subscriptionModal.signIn
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	accent := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	approve := "Approve the sign-in in your browser."
	if state.email != "" {
		approve = "Approve the sign-in in your browser as " + state.email + "."
	}
	status := "Waiting for the browser…"
	switch {
	case state.submitted:
		status = "Checking the code…"
	case state.url == "":
		status = "Starting sign-in…"
	}
	lines := []string{
		dim.Bold(true).Render(modalTruncate("SIGN IN AGAIN · "+strings.ToUpper(state.label), width)),
		"",
		modalTruncate(approve, width),
		dim.Render(modalTruncate("This window updates by itself when you finish.", width)),
		"",
		accent.Render(modalTruncate(status, width)),
		"",
		dim.Render(modalTruncate("If the page shows a code instead, paste it here:", width)),
		state.input.View(),
		"",
		buttons("[ Submit code ]", accent, "[ Cancel ]"),
	}
	if strings.Contains(strings.ToLower(state.lastLine), "fail") {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(modalTruncate(state.lastLine, width)))
	}
	return lines
}

// ptySignIn runs claude on its own pseudo-terminal: claude reads the pasted
// code from a terminal, and the menu keeps the real one.
type ptySignIn struct {
	cmd  *exec.Cmd
	tty  *os.File
	once sync.Once
}

func (p *ptySignIn) Submit(code string) error {
	_, err := p.tty.Write([]byte(code + "\r"))
	return err
}

// Cancel kills the process. claude ignores Ctrl-C at its paste prompt.
func (p *ptySignIn) Cancel() {
	p.once.Do(func() {
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
	})
}

var (
	signInURL    = regexp.MustCompile(`https://[^\s\x07\x1b]*oauth/authorize[^\s\x07\x1b]*`)
	signInEscape = regexp.MustCompile(`\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b\[[0-9;?]*[ -/]*[@-~]`)
)

const signInPrompt = "Paste code here"

// startPTYSignIn starts cmd and reports what it prints until it exits. The
// wide window keeps claude from wrapping its link.
func startPTYSignIn(cmd *exec.Cmd) (claudeSignIn, <-chan signInEvent, error) {
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 50, Cols: 1000})
	if err != nil {
		return nil, nil, err
	}
	p := &ptySignIn{cmd: cmd, tty: tty}
	events := make(chan signInEvent, 16)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		var raw bytes.Buffer
		sentURL, prompts, last := false, 0, ""
		buf := make([]byte, 4096)
		for {
			n, rerr := tty.Read(buf)
			raw.Write(buf[:n])
			text := raw.String()
			if !sentURL {
				if url := signInURL.FindString(text); url != "" {
					sentURL = true
					events <- signInEvent{url: url}
				}
			}
			if c := strings.Count(text, signInPrompt); c > prompts {
				prompts = c
				events <- signInEvent{prompt: true}
			}
			if line := lastOutputLine(signInEscape.ReplaceAllString(text, "")); line != last && !strings.HasPrefix(line, signInPrompt) {
				last = line
				events <- signInEvent{line: line}
			}
			if rerr != nil {
				return
			}
		}
	}()
	timer := time.AfterFunc(signInTimeout, p.Cancel)
	go func() {
		err := cmd.Wait()
		timer.Stop()
		select {
		case <-readDone:
		case <-time.After(time.Second):
		}
		_ = tty.Close()
		events <- signInEvent{done: true, err: err}
		close(events)
	}()
	return p, events, nil
}

func lastOutputLine(text string) string {
	lines := strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' })
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}
