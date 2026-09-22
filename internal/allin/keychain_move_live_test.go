//go:build darwin

package allin

import (
	"encoding/json"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/jackuait/wisp-deck/internal/claudeaccount"
)

// TestLiveKeychainLoginsMove crosses two THROWAWAY Keychain entries and proves
// Reconcile moves them back through the real `security` binary, keeping each
// slot's mcpOAuth in place. The default slot sits in a temp home and already
// matches its pin, so the real "Claude Code-credentials" entry is never read.
//
//	WISP_DECK_LIVE_LOGIN_MOVE_E2E=1 go test ./internal/allin/ -run TestLiveKeychainLoginsMove -v
func TestLiveKeychainLoginsMove(t *testing.T) {
	if os.Getenv("WISP_DECK_LIVE_LOGIN_MOVE_E2E") == "" {
		t.Skip("set WISP_DECK_LIVE_LOGIN_MOVE_E2E=1 to drive the real Keychain")
	}
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	accounts := filepath.Join(root, "claude-accounts")
	list := filepath.Join(root, "claude-accounts.list")
	emails := filepath.Join(root, "claude-account-emails")
	for _, d := range []string{home, filepath.Join(accounts, "a"), filepath.Join(accounts, "b")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(list, "A:a\nB:b\n")
	write(emails, "default:home@x.io\na:a@x.io\nb:b@x.io\n")
	write(filepath.Join(home, ".claude.json"), `{"oauthAccount":{"emailAddress":"home@x.io"}}`)
	// Crossed: slot a holds b's login and b holds a's.
	write(filepath.Join(accounts, "a", ".claude.json"), `{"oauthAccount":{"emailAddress":"b@x.io"}}`)
	write(filepath.Join(accounts, "b", ".claude.json"), `{"oauthAccount":{"emailAddress":"a@x.io"}}`)

	seed := map[string]string{
		"a": `{"claudeAiOauth":{"accessToken":"tok-b"},"mcpOAuth":{"only":"a"}}`,
		"b": `{"claudeAiOauth":{"accessToken":"tok-a"},"mcpOAuth":{"only":"b"}}`,
	}
	for slot, blob := range seed {
		service := KeychainService(filepath.Join(accounts, slot))
		if out, err := exec.Command("security", "add-generic-password", "-U",
			"-a", me.Username, "-s", service, "-w", blob).CombinedOutput(); err != nil {
			t.Fatalf("seeding %s: %v: %s", service, err, out)
		}
		t.Cleanup(func() {
			_ = exec.Command("security", "delete-generic-password", "-s", service).Run()
		})
	}

	err = claudeaccount.Reconcile(claudeaccount.ReconcileOptions{
		ListFile: list, AccountsDir: accounts, EmailsFile: emails,
		HomeDir: home, Store: KeychainLogins{},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	for slot, want := range map[string]string{"a": "tok-a", "b": "tok-b"} {
		blob, err := keychainBlob(filepath.Join(accounts, slot))
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			OAuth struct {
				AccessToken string `json:"accessToken"`
			} `json:"claudeAiOauth"`
			MCP struct {
				Only string `json:"only"`
			} `json:"mcpOAuth"`
		}
		if err := json.Unmarshal(blob, &got); err != nil {
			t.Fatal(err)
		}
		if got.OAuth.AccessToken != want || got.MCP.Only != slot {
			t.Fatalf("slot %s: login %q (want %q), mcp %q (want %q)",
				slot, got.OAuth.AccessToken, want, got.MCP.Only, slot)
		}
		t.Logf("slot %s: login %s, mcpOAuth stayed", slot, got.OAuth.AccessToken)
	}
}
