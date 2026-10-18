package tui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// A login can be signed in again from its details pane. `/login` in a pane
// routed to another login writes into the wrong slot, and the only fix is a
// fresh sign-in under the slot's own config dir.

type loginCall struct {
	configDir string
	email     string
}

// newReloginMenu is a unified-modal menu with a recording login runner, a
// private HOME for the Default login's .claude.json, and pinned emails.
func newReloginMenu(t *testing.T) (*MainMenuModel, *[]loginCall) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := newUnifiedModalMenu(t)
	pins := "default:work@corp.example\nwork:work2@corp.example\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(m.claudeAccountsList), "claude-account-emails"), []byte(pins), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := &[]loginCall{}
	m.SetClaudeLogin(func(configDir, email string) *exec.Cmd {
		*calls = append(*calls, loginCall{configDir, email})
		return exec.Command("true")
	})
	return m, calls
}

func writeIdentity(t *testing.T, file, email string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"oauthAccount":{"emailAddress":"` + email + `"}}`
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoginActions_offerSignInAgainBeforeDelete(t *testing.T) {
	m, _ := newReloginMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 0)
	if got := m.subscriptionLoginActions(); len(got) != 3 || got[2] != subscriptionDetailRelogin {
		t.Fatalf("Default actions = %v, want Use, Rename, Sign in again", got)
	}
	putCursorOnLogin(m, 1)
	got := m.subscriptionLoginActions()
	want := []int{subscriptionDetailUse, subscriptionDetailRename, subscriptionDetailRelogin, subscriptionDetailDelete}
	if len(got) != len(want) {
		t.Fatalf("managed actions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("managed actions = %v, want %v", got, want)
		}
	}
}

// The Default login runs without CLAUDE_CONFIG_DIR and prefills its pin.
func TestReloginKey_signsTheDefaultLoginInWithItsPinnedEmail(t *testing.T) {
	m, calls := newReloginMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 0)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd == nil {
		t.Fatal("sign-in returned no command")
	}
	if len(*calls) != 1 || (*calls)[0] != (loginCall{"", "work@corp.example"}) {
		t.Fatalf("login calls = %+v", *calls)
	}
}

func TestReloginKey_signsAManagedLoginInUnderItsOwnDir(t *testing.T) {
	m, calls := newReloginMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 1) // Work
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	want := loginCall{filepath.Join(m.claudeAccountsDir, "work"), "work2@corp.example"}
	if len(*calls) != 1 || (*calls)[0] != want {
		t.Fatalf("login calls = %+v, want %+v", *calls, want)
	}
}

// A login with no pin still signs in, just without a prefilled address.
func TestRelogin_withoutAPinPassesNoEmail(t *testing.T) {
	m, calls := newReloginMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 2) // Personal, not pinned
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if len(*calls) != 1 || (*calls)[0].email != "" {
		t.Fatalf("login calls = %+v", *calls)
	}
}

func TestRelogin_withoutARunnerExplainsItself(t *testing.T) {
	m := newUnifiedModalMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 0)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd != nil || m.subscriptionModal.err == nil {
		t.Fatalf("cmd=%v err=%v, want no command and an error", cmd != nil, m.subscriptionModal.err)
	}
}

// The details pane names the signed-in account and flags one that does not
// match the pin, which is how a crossed login shows up.
func TestLoginDetails_flagAnAccountThatDoesNotMatchThePin(t *testing.T) {
	m, _ := newReloginMenu(t)
	writeIdentity(t, filepath.Join(os.Getenv("HOME"), ".claude.json"), "me@home.example")
	m.openSubscriptionModal()
	putCursorOnLogin(m, 0)
	out := stripAnsi(strings.Join(m.subscriptionLoginDetailLines(70, 30), "\n"))
	for _, want := range []string{"me@home.example", "Expected work@corp.example", "[ Sign in again ]"} {
		if !strings.Contains(out, want) {
			t.Errorf("details missing %q:\n%s", want, out)
		}
	}
}

func TestLoginDetails_matchingAccountHasNoWarning(t *testing.T) {
	m, _ := newReloginMenu(t)
	writeIdentity(t, filepath.Join(os.Getenv("HOME"), ".claude.json"), "Work@Corp.example")
	m.openSubscriptionModal()
	putCursorOnLogin(m, 0)
	out := stripAnsi(strings.Join(m.subscriptionLoginDetailLines(70, 30), "\n"))
	if strings.Contains(out, "Expected") {
		t.Errorf("matching account flagged:\n%s", out)
	}
}

// After the sign-in returns, the pane re-reads the identity it shows.
func TestReloginDone_refreshesTheShownAccount(t *testing.T) {
	m, _ := newReloginMenu(t)
	identity := filepath.Join(os.Getenv("HOME"), ".claude.json")
	writeIdentity(t, identity, "me@home.example")
	m.openSubscriptionModal()
	putCursorOnLogin(m, 0)
	writeIdentity(t, identity, "work@corp.example")
	m.Update(subscriptionReloginDoneMsg{})
	out := stripAnsi(strings.Join(m.subscriptionLoginDetailLines(70, 30), "\n"))
	if !strings.Contains(out, "work@corp.example") || strings.Contains(out, "Expected") {
		t.Errorf("identity not refreshed:\n%s", out)
	}
}

func TestReloginDone_showsAFailure(t *testing.T) {
	m, _ := newReloginMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 0)
	m.Update(subscriptionReloginDoneMsg{err: errors.New("exit status 1")})
	if m.subscriptionModal.err == nil || !strings.Contains(m.subscriptionModal.err.Error(), "exit status 1") {
		t.Fatalf("err = %v", m.subscriptionModal.err)
	}
}

func TestSubscriptionModalMouse_clickSignInAgain(t *testing.T) {
	m, calls := newReloginMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 1)
	x, y := subscriptionCardCell(t, m, "[ Sign in again ]")
	if target := m.subscriptionModalTarget(x, y); target.kind != subscriptionHitRelogin {
		t.Fatalf("target = %+v, want subscriptionHitRelogin", target)
	}
	m.Update(subscriptionScreenMouse(m, x, y, tea.MouseActionPress, tea.MouseButtonLeft))
	if len(*calls) != 1 {
		t.Fatalf("click did not sign in: %+v", *calls)
	}
}

// A failed sign-in keeps its output on screen until Enter, and its last line
// reaches the details pane, so the reason is not wiped by the menu redraw.
func TestLoginExec_pausesAfterAFailureAndKeepsTheLastLine(t *testing.T) {
	c := &loginExec{Cmd: exec.Command("sh", "-c", "printf 'first\\n\\033[31mOAuth error: bad code\\033[0m\\n'; exit 1")}
	var tty strings.Builder
	c.SetStdin(strings.NewReader("\n"))
	c.SetStdout(&tty)
	if err := c.Run(); err == nil {
		t.Fatal("expected the failure")
	}
	if !strings.Contains(tty.String(), "Press Enter") {
		t.Errorf("no pause prompt:\n%q", tty.String())
	}
	if got := c.lastLine(); got != "OAuth error: bad code" {
		t.Errorf("lastLine = %q", got)
	}
}

func TestLoginExec_doesNotPauseAfterASuccess(t *testing.T) {
	c := &loginExec{Cmd: exec.Command("true")}
	var tty strings.Builder
	c.SetStdin(strings.NewReader(""))
	c.SetStdout(&tty)
	if err := c.Run(); err != nil || strings.Contains(tty.String(), "Press Enter") {
		t.Fatalf("err=%v out=%q", err, tty.String())
	}
}

func TestReloginDone_showsTheLastOutputLine(t *testing.T) {
	m, _ := newReloginMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 0)
	m.Update(subscriptionReloginDoneMsg{err: errors.New("exit status 1"), detail: "OAuth error: bad code"})
	if m.subscriptionModal.err == nil || !strings.Contains(m.subscriptionModal.err.Error(), "OAuth error: bad code") {
		t.Fatalf("err = %v", m.subscriptionModal.err)
	}
}

// The child must get the terminal on stderr too: the menu's own stderr is a
// file the launcher captures.
func TestLoginExec_sendsStderrToTheTerminal(t *testing.T) {
	c := &loginExec{Cmd: exec.Command("true")}
	var tty strings.Builder
	c.SetStdout(&tty)
	c.SetStderr(os.Stderr)
	if _, err := c.Stderr.Write([]byte("x")); err != nil || tty.String() != "x" {
		t.Fatal("stderr does not reach the terminal")
	}
}

// Bun's kqueue rejects the /dev/tty handle Bubble Tea passes, so the sign-in
// runs on the real terminal device instead.
func TestLoginExec_runsOnTheRealTerminalDevice(t *testing.T) {
	device := filepath.Join(t.TempDir(), "ttys-fake")
	if err := os.WriteFile(device, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	c := &loginExec{
		Cmd:      exec.Command("sh", "-c", "echo on-device"),
		terminal: func() (string, error) { return device, nil },
	}
	var passed strings.Builder
	c.SetStdin(strings.NewReader(""))
	c.SetStdout(&passed)
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(device)
	if !strings.Contains(string(got), "on-device") || passed.Len() != 0 {
		t.Fatalf("device got %q, passed writer got %q", got, passed.String())
	}
	if c.lastLine() != "on-device" {
		t.Errorf("lastLine = %q", c.lastLine())
	}
}

// Without a device it falls back to what Bubble Tea passed.
func TestLoginExec_fallsBackWhenNoDevice(t *testing.T) {
	c := &loginExec{
		Cmd:      exec.Command("sh", "-c", "echo fallback"),
		terminal: func() (string, error) { return "", errors.New("none") },
	}
	var passed strings.Builder
	c.SetStdin(strings.NewReader(""))
	c.SetStdout(&passed)
	if err := c.Run(); err != nil || !strings.Contains(passed.String(), "fallback") {
		t.Fatalf("err=%v out=%q", err, passed.String())
	}
}
