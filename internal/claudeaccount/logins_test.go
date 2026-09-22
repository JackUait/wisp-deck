package claudeaccount

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeLogins stands in for the Keychain. It records every call so a test can
// prove the steady state never touches it.
type fakeLogins struct {
	logins map[string]string
	calls  int
	locked []string
}

func (f *fakeLogins) Login(configDir string) (json.RawMessage, error) {
	f.calls++
	v, ok := f.logins[configDir]
	if !ok {
		return nil, errors.New("no login")
	}
	return json.RawMessage(v), nil
}

func (f *fakeLogins) SetLogin(configDir string, login json.RawMessage) error {
	f.calls++
	f.logins[configDir] = string(login)
	return nil
}

func (f *fakeLogins) Lock(configDir string) (func(), error) {
	f.calls++
	f.locked = append(f.locked, configDir)
	return func() {}, nil
}

type loginsFixture struct {
	home, accounts, list, emails, usage string
	store                               *fakeLogins
}

// newLoginsFixture builds a default slot plus one managed slot per dir.
func newLoginsFixture(t *testing.T, dirs ...string) *loginsFixture {
	t.Helper()
	root := t.TempDir()
	f := &loginsFixture{
		home:     filepath.Join(root, "home"),
		accounts: filepath.Join(root, "claude-accounts"),
		list:     filepath.Join(root, "claude-accounts.list"),
		emails:   filepath.Join(root, "claude-account-emails"),
		usage:    filepath.Join(root, "account-usage"),
		store:    &fakeLogins{logins: map[string]string{}},
	}
	var lines []string
	for _, d := range dirs {
		lines = append(lines, strings.ToUpper(d[:1])+d[1:]+":"+d)
		if err := os.MkdirAll(filepath.Join(f.accounts, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(f.home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.list, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *loginsFixture) opts() ReconcileOptions {
	return ReconcileOptions{
		ListFile:    f.list,
		AccountsDir: f.accounts,
		EmailsFile:  f.emails,
		UsageDir:    f.usage,
		HomeDir:     f.home,
		Store:       f.store,
	}
}

func (f *loginsFixture) configDir(slot string) string {
	if slot == "default" {
		return ""
	}
	return filepath.Join(f.accounts, slot)
}

func (f *loginsFixture) stateFile(slot string) string {
	if slot == "default" {
		return filepath.Join(f.home, ".claude.json")
	}
	return filepath.Join(f.accounts, slot, ".claude.json")
}

// loggedIn puts one login into a slot the way Claude Code leaves it: the
// Keychain entry and the oauthAccount block in the slot's .claude.json.
func (f *loginsFixture) loggedIn(t *testing.T, slot, email string) {
	t.Helper()
	f.store.logins[f.configDir(slot)] = `{"accessToken":"tok-` + email + `"}`
	state := `{"numStartups":7,"oauthAccount":{"emailAddress":"` + email + `","accountUuid":"uuid-` + email + `"},"zLast":true}`
	if err := os.WriteFile(f.stateFile(slot), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *loginsFixture) pin(t *testing.T, pins ...string) {
	t.Helper()
	if err := os.WriteFile(f.emails, []byte(strings.Join(pins, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *loginsFixture) usageCache(t *testing.T, slot, body string) {
	t.Helper()
	if err := os.MkdirAll(f.usage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.usage, slot+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *loginsFixture) stateEmail(t *testing.T, slot string) string {
	t.Helper()
	var state struct {
		OAuth struct {
			Email string `json:"emailAddress"`
			UUID  string `json:"accountUuid"`
		} `json:"oauthAccount"`
	}
	data, err := os.ReadFile(f.stateFile(slot))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.OAuth.UUID != "uuid-"+state.OAuth.Email {
		t.Fatalf("slot %s: oauthAccount was split — email %q carries uuid %q", slot, state.OAuth.Email, state.OAuth.UUID)
	}
	return state.OAuth.Email
}

func TestReconcile_pins_each_logged_in_slot_on_first_sight(t *testing.T) {
	f := newLoginsFixture(t, "personal")
	f.loggedIn(t, "default", "work@corp.io")
	f.loggedIn(t, "personal", "me@gmail.com")

	if err := Reconcile(f.opts()); err != nil {
		t.Fatal(err)
	}

	pins := LoadEmails(f.emails)
	if pins["default"] != "work@corp.io" || pins["personal"] != "me@gmail.com" {
		t.Fatalf("pins = %v", pins)
	}
}

// The steady state runs on every launch, so it must never exec `security`.
func TestReconcile_never_touches_the_keychain_when_every_slot_matches(t *testing.T) {
	f := newLoginsFixture(t, "personal")
	f.loggedIn(t, "default", "work@corp.io")
	f.loggedIn(t, "personal", "me@gmail.com")
	f.pin(t, "default:work@corp.io", "personal:me@gmail.com")

	if err := Reconcile(f.opts()); err != nil {
		t.Fatal(err)
	}
	if f.store.calls != 0 {
		t.Fatalf("store was called %d times on a matching deck", f.store.calls)
	}
}

func TestReconcile_swaps_two_crossed_slots_back(t *testing.T) {
	f := newLoginsFixture(t, "personal")
	f.pin(t, "default:work@corp.io", "personal:me@gmail.com")
	f.loggedIn(t, "default", "me@gmail.com")
	f.loggedIn(t, "personal", "work@corp.io")
	f.usageCache(t, "default", "usage-of-me")
	f.usageCache(t, "personal", "usage-of-work")

	if err := Reconcile(f.opts()); err != nil {
		t.Fatal(err)
	}

	if got := f.store.logins[""]; got != `{"accessToken":"tok-work@corp.io"}` {
		t.Fatalf("default keychain login = %s", got)
	}
	if got := f.store.logins[f.configDir("personal")]; got != `{"accessToken":"tok-me@gmail.com"}` {
		t.Fatalf("personal keychain login = %s", got)
	}
	if got := f.stateEmail(t, "default"); got != "work@corp.io" {
		t.Fatalf("default .claude.json email = %s", got)
	}
	if got := f.stateEmail(t, "personal"); got != "me@gmail.com" {
		t.Fatalf("personal .claude.json email = %s", got)
	}
	for slot, want := range map[string]string{"default": "usage-of-work", "personal": "usage-of-me"} {
		got, err := os.ReadFile(filepath.Join(f.usage, slot+".json"))
		if err != nil || string(got) != want {
			t.Fatalf("usage cache %s = %q, %v; want %q", slot, got, err, want)
		}
	}
}

func TestReconcile_keeps_every_other_key_of_the_state_file(t *testing.T) {
	f := newLoginsFixture(t, "personal")
	f.pin(t, "default:work@corp.io", "personal:me@gmail.com")
	f.loggedIn(t, "default", "me@gmail.com")
	f.loggedIn(t, "personal", "work@corp.io")

	if err := Reconcile(f.opts()); err != nil {
		t.Fatal(err)
	}

	var state map[string]any
	data, _ := os.ReadFile(f.stateFile("personal"))
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state["numStartups"] != float64(7) || state["zLast"] != true {
		t.Fatalf("state lost its other keys: %v", state)
	}
	info, err := os.Stat(f.stateFile("personal"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestReconcile_locks_every_slot_it_moves(t *testing.T) {
	f := newLoginsFixture(t, "personal")
	f.pin(t, "default:work@corp.io", "personal:me@gmail.com")
	f.loggedIn(t, "default", "me@gmail.com")
	f.loggedIn(t, "personal", "work@corp.io")

	if err := Reconcile(f.opts()); err != nil {
		t.Fatal(err)
	}
	if len(f.store.locked) != 2 {
		t.Fatalf("locked %v, want both slots", f.store.locked)
	}
}

func TestReconcile_rotates_a_three_way_cycle_back(t *testing.T) {
	f := newLoginsFixture(t, "personal", "side")
	f.pin(t, "default:a@x.io", "personal:b@x.io", "side:c@x.io")
	f.loggedIn(t, "default", "b@x.io")
	f.loggedIn(t, "personal", "c@x.io")
	f.loggedIn(t, "side", "a@x.io")

	if err := Reconcile(f.opts()); err != nil {
		t.Fatal(err)
	}

	for slot, want := range map[string]string{"default": "a@x.io", "personal": "b@x.io", "side": "c@x.io"} {
		if got := f.stateEmail(t, slot); got != want {
			t.Fatalf("slot %s holds %s, want %s", slot, got, want)
		}
		if got := f.store.logins[f.configDir(slot)]; got != `{"accessToken":"tok-`+want+`"}` {
			t.Fatalf("slot %s keychain = %s", slot, got)
		}
	}
}

func TestReconcile_repins_a_slot_logged_into_an_email_no_slot_is_pinned_to(t *testing.T) {
	f := newLoginsFixture(t, "personal")
	f.pin(t, "default:work@corp.io", "personal:me@gmail.com")
	f.loggedIn(t, "default", "work@corp.io")
	f.loggedIn(t, "personal", "new@gmail.com")

	if err := Reconcile(f.opts()); err != nil {
		t.Fatal(err)
	}

	if got := LoadEmails(f.emails)["personal"]; got != "new@gmail.com" {
		t.Fatalf("personal pin = %q", got)
	}
	if f.store.calls != 0 {
		t.Fatalf("a re-pin must not touch the keychain, got %d calls", f.store.calls)
	}
}

// Both slots on one email: the other login's token is gone, so there is
// nothing to move back.
func TestReconcile_leaves_two_slots_on_one_email_alone(t *testing.T) {
	f := newLoginsFixture(t, "personal")
	f.pin(t, "default:work@corp.io", "personal:me@gmail.com")
	f.loggedIn(t, "default", "work@corp.io")
	f.loggedIn(t, "personal", "work@corp.io")

	if err := Reconcile(f.opts()); err != nil {
		t.Fatal(err)
	}

	pins := LoadEmails(f.emails)
	if pins["default"] != "work@corp.io" || pins["personal"] != "me@gmail.com" {
		t.Fatalf("pins changed: %v", pins)
	}
	if f.store.calls != 0 {
		t.Fatalf("store called %d times", f.store.calls)
	}
}

func TestReconcile_skips_a_logged_out_slot(t *testing.T) {
	f := newLoginsFixture(t, "personal")
	f.loggedIn(t, "default", "work@corp.io")

	if err := Reconcile(f.opts()); err != nil {
		t.Fatal(err)
	}

	pins := LoadEmails(f.emails)
	if _, ok := pins["personal"]; ok {
		t.Fatalf("a logged-out slot was pinned: %v", pins)
	}
	if pins["default"] != "work@corp.io" {
		t.Fatalf("pins = %v", pins)
	}
}

func TestReconcile_compares_emails_without_case(t *testing.T) {
	f := newLoginsFixture(t, "personal")
	f.pin(t, "default:Work@Corp.io", "personal:me@gmail.com")
	f.loggedIn(t, "default", "work@corp.io")
	f.loggedIn(t, "personal", "me@gmail.com")

	if err := Reconcile(f.opts()); err != nil {
		t.Fatal(err)
	}
	if f.store.calls != 0 {
		t.Fatalf("store called %d times", f.store.calls)
	}
}

func TestGatedReconcile_runs_again_only_after_a_slot_file_changes(t *testing.T) {
	f := newLoginsFixture(t, "personal")
	f.loggedIn(t, "default", "work@corp.io")
	f.loggedIn(t, "personal", "me@gmail.com")
	runs := 0
	reconcile := gatedReconcile(f.opts(), func(ReconcileOptions) error { runs++; return nil })

	reconcile()
	reconcile()
	if runs != 1 {
		t.Fatalf("runs = %d after two calls on unchanged files, want 1", runs)
	}

	f.loggedIn(t, "personal", "someone-else@gmail.com")
	reconcile()
	if runs != 2 {
		t.Fatalf("runs = %d after a slot's state changed, want 2", runs)
	}
}

// A caller that may not touch the host passes no store: pins still land, and
// nothing moves.
func TestReconcile_without_a_store_pins_but_moves_nothing(t *testing.T) {
	f := newLoginsFixture(t, "personal")
	f.pin(t, "default:work@corp.io", "personal:me@gmail.com")
	f.loggedIn(t, "default", "me@gmail.com")
	f.loggedIn(t, "personal", "work@corp.io")
	opts := f.opts()
	opts.Store = nil

	if err := Reconcile(opts); err != nil {
		t.Fatal(err)
	}
	if got := f.stateEmail(t, "default"); got != "me@gmail.com" {
		t.Fatalf("default moved to %s with no store", got)
	}
}
