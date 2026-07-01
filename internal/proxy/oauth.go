package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// DefaultTokenEndpoint is Anthropic's OAuth token refresh endpoint.
const DefaultTokenEndpoint = "https://platform.claude.com/v1/oauth/token"

// defaultClientID is the Claude Code OAuth client id (same value teamclaude uses).
const defaultClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

// Tokens is the result of an OAuth refresh.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64 // ms epoch
}

// RefreshToken exchanges a refresh token for a fresh access token. A non-2xx
// response is returned as an error so the caller can decide whether to retire
// the account.
func RefreshToken(endpoint, refreshToken string) (Tokens, error) {
	reqBody, _ := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     defaultClientID,
	})
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return Tokens{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return Tokens{}, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return Tokens{}, fmt.Errorf("token refresh failed (%d): %s", res.StatusCode, string(body))
	}

	var data struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresAt    int64  `json:"expires_at"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return Tokens{}, err
	}
	tok := Tokens{
		AccessToken:  data.AccessToken,
		RefreshToken: data.RefreshToken,
		ExpiresAt:    normalizeExpiresAt(data.ExpiresAt),
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken
	}
	if tok.ExpiresAt == 0 {
		secs := data.ExpiresIn
		if secs == 0 {
			secs = 3600
		}
		tok.ExpiresAt = time.Now().Add(time.Duration(secs) * time.Second).UnixMilli()
	}
	return tok, nil
}

// normalizeExpiresAt converts a possibly-in-seconds timestamp to milliseconds.
func normalizeExpiresAt(v int64) int64 {
	if v == 0 {
		return 0
	}
	if v < 1e12 { // plausibly seconds
		return v * 1000
	}
	return v
}

// WriteCredentials updates the OAuth fields in a .credentials.json file in place,
// preserving any other keys (e.g. subscriptionType) that Claude Code relies on.
// Written atomically with 0600 permissions.
func WriteCredentials(path, access, refresh string, expiresAt int64) error {
	root := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &root)
	}
	oauth, _ := root["claudeAiOauth"].(map[string]any)
	if oauth == nil {
		oauth = map[string]any{}
	}
	oauth["accessToken"] = access
	oauth["refreshToken"] = refresh
	oauth["expiresAt"] = expiresAt
	root["claudeAiOauth"] = oauth

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
