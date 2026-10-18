package tui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// A login can be signed in again from its details pane. `/login` in a pane
// routed to another login writes into the wrong slot, and the only fix is a
// fresh sign-in under the slot's own config dir. The sign-in runs inside the
// modal: claude finishes on its own when the browser reaches its local
// callback, and a pasted code is the fallback.

type loginCall struct {
	configDir string
	email     string
}

type fakeSignIn struct {
	submitted []string
	cancelled int
	events    chan signInEvent
}

func (f *fakeSignIn) Submit(code string) error {
	f.submitted = append(f.submitted, code)
	return nil
}

func (f *fakeSignIn) Cancel() { f.cancelled++ }

// newReloginMenu is a unified-modal menu with a recording login builder, a
// fake sign-in process, a private HOME for the Default login's .claude.json,
// and pinned emails.
func newReloginMenu(t *testing.T) (*MainMenuModel, *[]loginCall, *fakeSignIn) {
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
	fake := &fakeSignIn{}
	m.startSignIn = func(*exec.Cmd) (claudeSignIn, <-chan signInEvent, error) {
		fake.events = make(chan signInEvent, 8)
		return fake, fake.events, nil
	}
	return m, calls, fake
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

func signInEventFor(m *MainMenuModel, ev signInEvent) subscriptionSignInMsg {
	return subscriptionSignInMsg{generation: m.subscriptionModal.signIn.generation, event: ev}
}

func detailText(m *MainMenuModel) string {
	return stripAnsi(strings.Join(m.subscriptionDetailLines(60, 30), "\n"))
}

func TestLoginActions_offerSignInAgainBeforeDelete(t *testing.T) {
	m, _, _ := newReloginMenu(t)
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
	m, calls, _ := newReloginMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 0)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd == nil {
		t.Fatal("sign-in returned no command")
	}
	if len(*calls) != 1 || (*calls)[0] != (loginCall{"", "work@corp.example"}) {
		t.Fatalf("login calls = %+v", *calls)
	}
	if m.subscriptionModal.mode != subscriptionLoginSignIn {
		t.Fatalf("mode = %v, want the in-modal sign-in", m.subscriptionModal.mode)
	}
}

func TestReloginKey_signsAManagedLoginInUnderItsOwnDir(t *testing.T) {
	m, calls, _ := newReloginMenu(t)
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
	m, calls, _ := newReloginMenu(t)
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

func TestRelogin_aFailedStartIsShown(t *testing.T) {
	m, _, _ := newReloginMenu(t)
	m.startSignIn = func(*exec.Cmd) (claudeSignIn, <-chan signInEvent, error) {
		return nil, nil, errors.New("no pty")
	}
	m.openSubscriptionModal()
	putCursorOnLogin(m, 0)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if m.subscriptionModal.mode != subscriptionBrowse || m.subscriptionModal.err == nil ||
		!strings.Contains(m.subscriptionModal.err.Error(), "no pty") {
		t.Fatalf("mode=%v err=%v", m.subscriptionModal.mode, m.subscriptionModal.err)
	}
}

// The panel stays inside the modal and names the account to approve.
func TestSignInPanel_asksForTheBrowserFirstAndOffersAPasteField(t *testing.T) {
	// 60 is the details pane in the widest modal; the email must not be cut.
	m, _, _ := newReloginMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 0)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m.Update(signInEventFor(m, signInEvent{url: "https://claude.com/cai/oauth/authorize?x=1"}))
	out := detailText(m)
	for _, want := range []string{"SIGN IN AGAIN", "as work@corp.example.", "browser", "paste", "[ Submit code ]", "[ Cancel ]"} {
		if !strings.Contains(out, want) {
			t.Errorf("panel missing %q:\n%s", want, out)
		}
	}
}

// Pasted text goes to the input, never to the browse shortcuts (d deletes,
// x disables, u switches login, r renames).
func TestSignInPanel_aPastedCodeLandsInTheInput(t *testing.T) {
	m, _, fake := newReloginMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 1)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	before := m.selectedAccount
	cursor := m.subscriptionModal.profileCursor
	code := "duxraies#Code-123"
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(code), Paste: true})
	for _, k := range []tea.KeyType{tea.KeyUp, tea.KeyDown, tea.KeyTab} {
		m.Update(tea.KeyMsg{Type: k})
	}
	if got := m.subscriptionModal.signIn.input.Value(); got != code {
		t.Fatalf("input = %q, want %q", got, code)
	}
	if m.subscriptionModal.mode != subscriptionLoginSignIn || m.selectedAccount != before ||
		m.subscriptionModal.profileCursor != cursor || len(m.claudeAccounts) != 2 {
		t.Fatal("a key in the code ran a browse action")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(fake.submitted) != 1 || fake.submitted[0] != code {
		t.Fatalf("submitted = %q", fake.submitted)
	}
}

func TestSignInPanel_trimsTheCodeAndRefusesAnEmptyOne(t *testing.T) {
	m, _, fake := newReloginMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 0)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(fake.submitted) != 0 {
		t.Fatalf("empty code submitted: %q", fake.submitted)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("  abc#1 \n"), Paste: true})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(fake.submitted) != 1 || fake.submitted[0] != "abc#1" {
		t.Fatalf("submitted = %q", fake.submitted)
	}
}

func TestSignInPanel_escCancelsTheProcess(t *testing.T) {
	m, _, fake := newReloginMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 0)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if fake.cancelled == 0 || m.subscriptionModal.mode != subscriptionBrowse {
		t.Fatalf("cancelled=%d mode=%v", fake.cancelled, m.subscriptionModal.mode)
	}
	if !m.subscriptionModal.open {
		t.Fatal("Esc during sign-in must cancel the sign-in, not close the modal")
	}
}

func TestSignInPanel_closingTheModalCancelsTheProcess(t *testing.T) {
	m, _, fake := newReloginMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 0)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m.cancelSubscriptionAuth()
	if fake.cancelled == 0 {
		t.Fatal("closing the modal left the sign-in running")
	}
}

func TestSignInPanel_cancelButtonCancels(t *testing.T) {
	m, _, fake := newReloginMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 0)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	x, y := subscriptionCardCell(t, m, "[ Cancel ]")
	m.Update(subscriptionScreenMouse(m, x, y, tea.MouseActionPress, tea.MouseButtonLeft))
	if fake.cancelled == 0 || m.subscriptionModal.mode != subscriptionBrowse {
		t.Fatalf("cancelled=%d mode=%v", fake.cancelled, m.subscriptionModal.mode)
	}
}

// Exit 0 ends the panel and the pane shows the account the login now holds.
func TestSignInDone_successRefreshesTheShownAccount(t *testing.T) {
	m, _, _ := newReloginMenu(t)
	identity := filepath.Join(os.Getenv("HOME"), ".claude.json")
	writeIdentity(t, identity, "me@home.example")
	m.openSubscriptionModal()
	putCursorOnLogin(m, 0)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	writeIdentity(t, identity, "work@corp.example")
	m.Update(signInEventFor(m, signInEvent{done: true}))
	if m.subscriptionModal.mode != subscriptionBrowse || m.subscriptionModal.err != nil {
		t.Fatalf("mode=%v err=%v", m.subscriptionModal.mode, m.subscriptionModal.err)
	}
	out := detailText(m)
	if !strings.Contains(out, "work@corp.example") || strings.Contains(out, "Expected") {
		t.Errorf("identity not refreshed:\n%s", out)
	}
}

func TestSignInDone_failureShowsClaudesMessage(t *testing.T) {
	m, _, _ := newReloginMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 0)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m.Update(signInEventFor(m, signInEvent{line: "Login failed: Request failed with status code 400"}))
	m.Update(signInEventFor(m, signInEvent{done: true, err: errors.New("exit status 1")}))
	if m.subscriptionModal.err == nil || !strings.Contains(m.subscriptionModal.err.Error(), "status code 400") {
		t.Fatalf("err = %v", m.subscriptionModal.err)
	}
}

// An event from a cancelled sign-in must not end a newer one.
func TestSignInEvents_fromAnOldSignInAreIgnored(t *testing.T) {
	m, _, _ := newReloginMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 0)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	stale := signInEventFor(m, signInEvent{done: true, err: errors.New("killed")})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m.Update(stale)
	if m.subscriptionModal.mode != subscriptionLoginSignIn || m.subscriptionModal.err != nil {
		t.Fatalf("stale event ended the new sign-in: mode=%v err=%v", m.subscriptionModal.mode, m.subscriptionModal.err)
	}
}

// The details pane names the signed-in account and flags one that does not
// match the pin, which is how a crossed login shows up.
func TestLoginDetails_flagAnAccountThatDoesNotMatchThePin(t *testing.T) {
	m, _, _ := newReloginMenu(t)
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
	m, _, _ := newReloginMenu(t)
	writeIdentity(t, filepath.Join(os.Getenv("HOME"), ".claude.json"), "Work@Corp.example")
	m.openSubscriptionModal()
	putCursorOnLogin(m, 0)
	out := stripAnsi(strings.Join(m.subscriptionLoginDetailLines(70, 30), "\n"))
	if strings.Contains(out, "Expected") {
		t.Errorf("matching account flagged:\n%s", out)
	}
}

func TestSubscriptionModalMouse_clickSignInAgain(t *testing.T) {
	m, calls, _ := newReloginMenu(t)
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

// The real process side: claude prints its link inside an OSC 8 hyperlink,
// then a prompt with no newline, reads a line, and exits.
func TestPTYSignIn_reportsTheLinkThePromptAndTheExit(t *testing.T) {
	out := filepath.Join(t.TempDir(), "code")
	script := `printf 'Opening browser to sign in\342\200\246\r\nIf the browser didn'"'"'t open, visit: \033]8;;https://claude.com/cai/oauth/authorize?code=true&x=1\a\033[94mhttps://claude.com/cai/oauth/authorize?code=true&x=1\033[39m\033]8;;\a\r\nPaste code here if prompted > '; read -r code; printf '%s' "$code" > ` + out + `; echo 'Login failed: bad'; exit 1`
	handle, events, err := startPTYSignIn(exec.Command("sh", "-c", script))
	if err != nil {
		t.Fatal(err)
	}
	var url string
	var prompted bool
	deadline := time.After(10 * time.Second)
	for !prompted || url == "" {
		select {
		case ev := <-events:
			if ev.url != "" {
				url = ev.url
			}
			prompted = prompted || ev.prompt
			if ev.done {
				t.Fatalf("exited before the prompt: %v", ev.err)
			}
		case <-deadline:
			t.Fatalf("no link/prompt: url=%q prompted=%v", url, prompted)
		}
	}
	if url != "https://claude.com/cai/oauth/authorize?code=true&x=1" {
		t.Errorf("url = %q", url)
	}
	if err := handle.Submit("abc#1"); err != nil {
		t.Fatal(err)
	}
	var last string
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("events closed without done")
			}
			if ev.line != "" {
				last = ev.line
			}
			if ev.done {
				if ev.err == nil || last != "Login failed: bad" {
					t.Fatalf("done err=%v last=%q", ev.err, last)
				}
				if got, _ := os.ReadFile(out); string(got) != "abc#1" {
					t.Fatalf("child read %q", got)
				}
				return
			}
		case <-deadline:
			t.Fatal("no exit")
		}
	}
}

func TestPTYSignIn_cancelKillsTheProcess(t *testing.T) {
	handle, events, err := startPTYSignIn(exec.Command("sh", "-c", "trap '' INT; sleep 30"))
	if err != nil {
		t.Fatal(err)
	}
	handle.Cancel()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok || ev.done {
				return
			}
		case <-deadline:
			t.Fatal("cancel did not end the process")
		}
	}
}
