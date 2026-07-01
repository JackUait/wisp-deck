package proxy

import (
	"os"
	"path/filepath"
	"testing"
)

// writeCreds writes a .credentials.json for an account dir in the Claude Code
// shape (OAuth nested under claudeAiOauth).
func writeCreds(t *testing.T, accountsDir, dir, access, refresh string, expiresAt int64) {
	t.Helper()
	full := filepath.Join(accountsDir, dir)
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"claudeAiOauth":{"accessToken":"` + access +
		`","refreshToken":"` + refresh +
		`","expiresAt":` + itoa(expiresAt) + `,"subscriptionType":"max"}}`
	if err := os.WriteFile(filepath.Join(full, ".credentials.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func itoa(v int64) string {
	// small helper to avoid importing strconv in the test literal
	return string(appendInt(nil, v))
}

func appendInt(b []byte, v int64) []byte {
	if v < 0 {
		b = append(b, '-')
		v = -v
	}
	if v >= 10 {
		b = appendInt(b, v/10)
	}
	return append(b, byte('0'+v%10))
}

func writeList(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAccounts_readsListedCredentials(t *testing.T) {
	root := t.TempDir()
	accountsDir := filepath.Join(root, "claude-accounts")
	listFile := filepath.Join(root, "claude-accounts.list")

	writeCreds(t, accountsDir, "work", "tok-work", "ref-work", 111)
	writeCreds(t, accountsDir, "personal", "tok-personal", "ref-personal", 222)
	writeList(t, listFile, "# comment\nWork:work\nPersonal:personal\n\n")

	accts, err := LoadAccounts(accountsDir, listFile)
	if err != nil {
		t.Fatalf("LoadAccounts: %v", err)
	}
	if len(accts) != 2 {
		t.Fatalf("got %d accounts, want 2", len(accts))
	}
	if accts[0].Label != "Work" || accts[0].AccessToken != "tok-work" || accts[0].RefreshToken != "ref-work" {
		t.Errorf("account[0] = %+v", accts[0])
	}
	if accts[0].ExpiresAt != 111 {
		t.Errorf("account[0].ExpiresAt = %d, want 111", accts[0].ExpiresAt)
	}
	if accts[1].Label != "Personal" || accts[1].AccessToken != "tok-personal" {
		t.Errorf("account[1] = %+v", accts[1])
	}
}

func TestLoadAccounts_skipsAccountsMissingCredentials(t *testing.T) {
	root := t.TempDir()
	accountsDir := filepath.Join(root, "claude-accounts")
	listFile := filepath.Join(root, "claude-accounts.list")

	writeCreds(t, accountsDir, "work", "tok-work", "ref-work", 111)
	// "ghost" is listed but has no credentials file — should be skipped.
	writeList(t, listFile, "Work:work\nGhost:ghost\n")

	accts, err := LoadAccounts(accountsDir, listFile)
	if err != nil {
		t.Fatalf("LoadAccounts: %v", err)
	}
	if len(accts) != 1 || accts[0].Label != "Work" {
		t.Fatalf("got %+v, want only Work", accts)
	}
}

func TestLoadAccounts_emptyListReturnsNoAccounts(t *testing.T) {
	root := t.TempDir()
	accts, err := LoadAccounts(filepath.Join(root, "claude-accounts"), filepath.Join(root, "missing.list"))
	if err != nil {
		t.Fatalf("LoadAccounts: %v", err)
	}
	if len(accts) != 0 {
		t.Fatalf("got %d, want 0", len(accts))
	}
}
