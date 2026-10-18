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

// The child must get the terminal on stderr too: the menu's own stderr is a
// file the launcher captures.
func TestLoginExec_sendsStderrToTheTerminal(t *testing.T) {
	c := &loginExec{Cmd: exec.Command("true")}
	var tty strings.Builder
	c.SetStdout(&tty)
	c.SetStderr(os.Stderr)
	if c.Stderr != c.Stdout {
		t.Fatal("stderr does not follow the terminal")
	}
}
