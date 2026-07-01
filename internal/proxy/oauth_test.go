package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRefreshToken_returnsNewTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["grant_type"] != "refresh_token" || body["refresh_token"] != "old-refresh" {
			t.Errorf("unexpected refresh body: %v", body)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access",
			"refresh_token": "new-refresh",
			"expires_in":    3600,
		})
	}))
	defer srv.Close()

	tok, err := RefreshToken(srv.URL, "old-refresh")
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if tok.AccessToken != "new-access" || tok.RefreshToken != "new-refresh" {
		t.Errorf("got %+v", tok)
	}
	if tok.ExpiresAt <= 0 {
		t.Errorf("ExpiresAt not set: %d", tok.ExpiresAt)
	}
}

func TestRefreshToken_hardErrorReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()

	_, err := RefreshToken(srv.URL, "dead")
	if err == nil {
		t.Fatal("expected error on 401")
	}
}

func TestWriteCredentials_roundTripsAndPreservesShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".credentials.json")
	// Seed an existing file with extra fields that must survive the rewrite.
	seed := `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1,"subscriptionType":"max"}}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := WriteCredentials(path, "a2", "r2", 999); err != nil {
		t.Fatalf("WriteCredentials: %v", err)
	}

	creds, err := ParseCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	if creds.AccessToken != "a2" || creds.RefreshToken != "r2" || creds.ExpiresAt != 999 {
		t.Errorf("round-trip mismatch: %+v", creds)
	}
	// subscriptionType must be preserved (Claude Code reads it).
	raw, _ := os.ReadFile(path)
	var m map[string]map[string]any
	json.Unmarshal(raw, &m)
	if m["claudeAiOauth"]["subscriptionType"] != "max" {
		t.Errorf("subscriptionType not preserved: %s", raw)
	}
}
