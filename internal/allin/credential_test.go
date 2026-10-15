package allin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeychainService_names_the_default_login_without_a_suffix(t *testing.T) {
	if got := KeychainService(""); got != "Claude Code-credentials" {
		t.Fatalf("got %q", got)
	}
}

func TestKeychainService_derives_the_suffix_from_the_config_dir(t *testing.T) {
	// sha256("/Users/jackuait/.config/wisp-deck/claude-accounts/personal")[:8]
	got := KeychainService("/Users/jackuait/.config/wisp-deck/claude-accounts/personal")
	if got != "Claude Code-credentials-7646b36d" {
		t.Fatalf("got %q", got)
	}
}

func TestResolve_hands_an_account_row_its_own_oauth_token(t *testing.T) {
	env := rosterEnv(t)
	resolver := NewResolver(env)
	var gotConfigDir string
	resolver.Token = func(configDir string) (string, error) {
		gotConfigDir = configDir
		return "oat-personal", nil
	}
	got, err := resolver.Resolve(Target{Kind: KindAccount, Source: "personal", Model: "claude-opus-5"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Header != "Authorization" || got.Value != "Bearer oat-personal" {
		t.Fatalf("got %+v", got)
	}
	if got.BaseURL != anthropicUpstream {
		t.Fatalf("base %q", got.BaseURL)
	}
	if want := filepath.Join(env.AccountsDir, "personal"); gotConfigDir != want {
		t.Fatalf("configDir = %q, want %q", gotConfigDir, want)
	}
}

func TestResolve_reads_the_default_login_with_no_config_dir(t *testing.T) {
	env := rosterEnv(t)
	resolver := NewResolver(env)
	var gotConfigDir string
	resolver.Token = func(configDir string) (string, error) {
		gotConfigDir = configDir
		return "oat-default", nil
	}
	if _, err := resolver.Resolve(Target{Kind: KindAccount, Source: "default", Model: "claude-opus-5"}); err != nil {
		t.Fatal(err)
	}
	// The default login has no CLAUDE_CONFIG_DIR, so its Keychain entry is
	// unsuffixed — Token must see an empty configDir, not a joined path.
	if gotConfigDir != "" {
		t.Fatalf("configDir = %q, want empty", gotConfigDir)
	}
}

func TestResolve_hands_a_config_row_its_profile_key_and_endpoint(t *testing.T) {
	env := rosterEnv(t)
	got, err := NewResolver(env).Resolve(Target{Kind: KindConfig, Source: "zhipu-glm", Model: "glm-4.7"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != "Bearer k" || got.BaseURL != "https://api.z.ai/api/anthropic" {
		t.Fatalf("got %+v", got)
	}
}

func TestResolve_reports_a_stale_account_by_name(t *testing.T) {
	env := rosterEnv(t)
	resolver := NewResolver(env)
	resolver.Token = func(string) (string, error) { return "", errors.New("not found") }
	_, err := resolver.Resolve(Target{Kind: KindAccount, Source: "personal"})
	if !errors.Is(err, ErrStaleAccount) {
		t.Fatalf("err = %v", err)
	}
}

// staleResolver is a resolver on the default pane of a deck whose default
// login is tagged "Work", with Token failing the way the test names.
func staleResolver(t *testing.T, paneLogin string, tokenErr error) *FileResolver {
	t.Helper()
	env := rosterEnv(t)
	env.DefaultLabelFile = filepath.Join(t.TempDir(), "claude-account-default-label")
	if err := os.WriteFile(env.DefaultLabelFile, []byte("Work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(env)
	resolver.PaneLogin = paneLogin
	resolver.Token = func(string) (string, error) { return "", tokenErr }
	return resolver
}

// /login writes the login of the pane it runs in. Advising it from a pane on
// another login signs that other login in and overwrites it, so the advice
// must name the login to fix and warn off this pane.
func TestResolve_tells_a_signed_out_login_where_to_run_login(t *testing.T) {
	cause := fmt.Errorf("%w: the refresh token was refused: token refresh failed (400): invalid_grant", ErrSignedOut)
	_, err := staleResolver(t, "default", cause).Resolve(Target{Kind: KindAccount, Source: "personal"})
	if !errors.Is(err, ErrStaleAccount) || !errors.Is(err, ErrSignedOut) {
		t.Fatalf("err = %v, want ErrStaleAccount and ErrSignedOut", err)
	}
	for _, want := range []string{
		`"Personal" is signed out`,
		"invalid_grant",
		`open a wisp-deck tab on "Personal" and run /login there`,
		`Do not run /login in this pane: it signs in "Work", not "Personal"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message lacks %q:\n%s", want, err)
		}
	}
}

func TestResolve_tells_a_pane_on_its_own_signed_out_login_to_run_login_here(t *testing.T) {
	cause := fmt.Errorf("%w: it stores no refresh token", ErrSignedOut)
	_, err := staleResolver(t, "personal", cause).Resolve(Target{Kind: KindAccount, Source: "personal"})
	if !strings.Contains(err.Error(), "Run /login in this pane") || strings.Contains(err.Error(), "Do not run") {
		t.Fatalf("message:\n%s", err)
	}
}

func TestResolve_asks_for_a_retry_when_the_login_failed_for_a_passing_reason(t *testing.T) {
	cause := errors.New("allin: refreshing the login failed: dial tcp: i/o timeout")
	_, err := staleResolver(t, "default", cause).Resolve(Target{Kind: KindAccount, Source: "personal"})
	if !errors.Is(err, ErrStaleAccount) {
		t.Fatalf("err = %v, want ErrStaleAccount", err)
	}
	if !strings.Contains(err.Error(), "Retry") || !strings.Contains(err.Error(), "i/o timeout") ||
		strings.Contains(err.Error(), "signed out") {
		t.Fatalf("message:\n%s", err)
	}
}

func TestPaneLogin_names_the_login_a_pane_runs_under(t *testing.T) {
	accounts := "/home/u/.config/wisp-deck/claude-accounts"
	for _, tc := range []struct{ configDir, want string }{
		{"", "default"},
		{accounts + "/personal", "personal"},
		{accounts + "/personal/", "personal"},
		{"/somewhere/else", ""},
	} {
		if got := PaneLogin(accounts, tc.configDir); got != tc.want {
			t.Errorf("PaneLogin(%q) = %q, want %q", tc.configDir, got, tc.want)
		}
	}
}

func TestResolve_refuses_a_source_that_escapes_its_directory(t *testing.T) {
	env := rosterEnv(t)
	if _, err := NewResolver(env).Resolve(Target{Kind: KindConfig, Source: "../../etc/passwd"}); err == nil {
		t.Fatal("traversal accepted")
	}
}

// writeProfile registers one profile in the roster env and writes its settings.
func writeProfile(t *testing.T, env Env, name, file, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(env.ConfigsDir, file), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	existing, err := os.ReadFile(env.ConfigsList)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.ConfigsList,
		append(existing, []byte(name+":"+file+"\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// configRows will not offer this, for a reason that is about the ROUTER, not
// about the profile: a wisp-router profile has no credential of its own, and a
// row naming it would send the turn back into the router that asked for it.
// Resolve enforces the same rule, so a hand-typed id — or a picker default
// saved from an older roster — cannot walk past it. The name is deliberately
// not "All-In": the display-name guard would catch that one, and the check
// under test here is the marker.
func TestResolve_refuses_a_provider_the_roster_would_not_offer(t *testing.T) {
	env := rosterEnv(t)
	writeProfile(t, env, "Everything", "everything.json", `{"env":{
"WISP_DECK_SUBSCRIPTION_PROVIDER":"allin",
"ANTHROPIC_BASE_URL":"https://api.anthropic.com"}}`)
	_, err := NewResolver(env).Resolve(Target{Kind: KindConfig, Source: "everything", Model: "m"})
	if err == nil {
		t.Fatal("a router profile resolved as a routing destination")
	}
	// The exact refusal, not just any error: without the routableAuth check the
	// resolver falls through to the key/endpoint read and answers "is not
	// ready" — an accident of this profile carrying no token, which would stop
	// holding the moment a user added one to it by hand.
	if !strings.Contains(err.Error(), `profile "everything" is served by All-In, which this router cannot address`) {
		t.Fatalf("error does not refuse the profile by its provider: %v", err)
	}
}

// Featherless (RemoteCatalog) now resolves rather than being refused: proxy.go
// delegates a NeedsRepair credential to internal/rolefix's own handler, which
// runs the same role/thinking repairs a dedicated Featherless pane gets. A
// hand-typed or stale-roster id must reach that path too, not a dead end.
func TestResolve_serves_a_featherless_target_and_marks_it_for_repair(t *testing.T) {
	env := rosterEnv(t)
	writeProfile(t, env, "Featherless", "featherless.json", `{"env":{
"WISP_DECK_SUBSCRIPTION_PROVIDER":"featherless",
"ANTHROPIC_BASE_URL":"https://api.featherless.ai",
"ANTHROPIC_AUTH_TOKEN":"sk-test"}}`)
	got, err := NewResolver(env).Resolve(Target{Kind: KindConfig, Source: "featherless", Model: "m"})
	if err != nil {
		t.Fatalf("Featherless refused to resolve: %v", err)
	}
	if got.BaseURL != "https://api.featherless.ai" || got.Value != "Bearer sk-test" {
		t.Fatalf("got %+v", got)
	}
	if !got.NeedsRepair {
		t.Fatalf("got %+v, want NeedsRepair so proxy.go routes it through rolefix", got)
	}
}

// A gateway that speaks the Anthropic API natively (Zhipu here) must not be
// marked for repair — that would send an ordinary, already-conforming request
// through rolefix's role/thinking rewrite for no reason.
func TestResolve_does_not_mark_an_ordinary_gateway_for_repair(t *testing.T) {
	env := rosterEnv(t)
	got, err := NewResolver(env).Resolve(Target{Kind: KindConfig, Source: "zhipu-glm", Model: "glm-4.7"})
	if err != nil {
		t.Fatal(err)
	}
	if got.NeedsRepair {
		t.Fatalf("got %+v, an ordinary gateway must not be routed through rolefix", got)
	}
}

// The All-In profile is in the same configs list, and its own rows are the ones
// being typed — so it can name itself. Routing into the router that is asking
// would loop a turn back through this handler.
func TestResolve_refuses_the_router_profile_itself(t *testing.T) {
	env := rosterEnv(t)
	if _, err := EnsureProfile(env, env.ConfigsList, env.ConfigsDir); err != nil {
		t.Fatal(err)
	}
	if _, err := NewResolver(env).Resolve(
		Target{Kind: KindConfig, Source: "all-in", Model: "m"}); err == nil {
		t.Fatal("All-In resolved as a routing destination")
	}
}

// The refusal must not have swallowed the providers the roster does offer.
func TestResolve_still_serves_a_provider_the_roster_offers(t *testing.T) {
	env := rosterEnv(t)
	if _, err := NewResolver(env).Resolve(
		Target{Kind: KindConfig, Source: "zhipu-glm", Model: "glm-4.7"}); err != nil {
		t.Fatalf("an ordinary gateway stopped resolving: %v", err)
	}
}

// A self-hosted profile speaks the Anthropic API directly and needs no repair,
// so it must resolve AND must not be routed through rolefix — NeedsRepair must
// key on RemoteCatalog, never on SuppliesOwnModel, which is true for both a
// self-hosted profile and Featherless and would wrongly repair this one too.
func TestResolve_still_serves_a_self_hosted_profile(t *testing.T) {
	env := rosterEnv(t)
	writeProfile(t, env, "Self Hosted", "self-hosted.json", `{"env":{
"WISP_DECK_SUBSCRIPTION_PROVIDER":"custom",
"ANTHROPIC_BASE_URL":"http://localhost:8000",
"ANTHROPIC_AUTH_TOKEN":"sk-test"}}`)
	got, err := NewResolver(env).Resolve(
		Target{Kind: KindConfig, Source: "self-hosted", Model: "qwen"})
	if err != nil {
		t.Fatalf("a self-hosted profile stopped resolving: %v", err)
	}
	if got.NeedsRepair {
		t.Fatalf("got %+v, a self-hosted profile must not be routed through rolefix", got)
	}
}

// A borrowed login must be put back in its slot before its token is read, or
// the turn is billed to the other subscription.
func TestResolve_reconciles_the_logins_before_reading_an_account_token(t *testing.T) {
	env := rosterEnv(t)
	resolver := NewResolver(env)
	var order []string
	resolver.Reconcile = func() { order = append(order, "reconcile") }
	resolver.Token = func(string) (string, error) {
		order = append(order, "token")
		return "oat", nil
	}
	if _, err := resolver.Resolve(Target{Kind: KindAccount, Source: "personal"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "reconcile,token" {
		t.Fatalf("order = %v", order)
	}
}
