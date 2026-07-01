// Package proxy implements Wisp Deck's in-house Claude account-rotation proxy.
//
// It sits between Claude Code and api.anthropic.com, injecting the active
// account's OAuth token per request and rotating to another account when the
// active one nears its quota or is rate-limited. Because the swap happens
// upstream, Claude Code runs under a single config dir and the conversation is
// continuous across switches. Modeled on github.com/KarpelesLab/teamclaude.
package proxy

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Account is one Claude login in the rotation pool, sourced from a
// claude-accounts directory's .credentials.json.
type Account struct {
	Label        string // display label from the accounts list
	Dir          string // directory name under the accounts dir
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64 // ms epoch; 0 if unknown
}

// credentialsFile mirrors the Claude Code ~/.claude/.credentials.json shape,
// where OAuth data is nested under "claudeAiOauth".
type credentialsFile struct {
	ClaudeAiOauth *oauthCreds `json:"claudeAiOauth"`
	// Fallback: some tools store the fields at the top level.
	oauthCreds
}

type oauthCreds struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresAt    int64  `json:"expiresAt"`
}

// LoadAccounts reads the accounts list (label:dir lines, "#" comments and blanks
// skipped) and loads each account's credentials from
// <accountsDir>/<dir>/.credentials.json. Accounts whose credentials are missing
// or unreadable are skipped rather than failing the whole load.
func LoadAccounts(accountsDir, listFile string) ([]Account, error) {
	f, err := os.Open(listFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var accounts []Account
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		label, dir, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		label, dir = strings.TrimSpace(label), strings.TrimSpace(dir)
		if label == "" || dir == "" {
			continue
		}
		creds, err := ParseCredentials(filepath.Join(accountsDir, dir, ".credentials.json"))
		if err != nil || creds.AccessToken == "" {
			continue
		}
		accounts = append(accounts, Account{
			Label:        label,
			Dir:          dir,
			AccessToken:  creds.AccessToken,
			RefreshToken: creds.RefreshToken,
			ExpiresAt:    creds.ExpiresAt,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return accounts, nil
}

// ParseCredentials reads a .credentials.json file and returns its OAuth fields,
// accepting both the nested "claudeAiOauth" shape and a flat one.
func ParseCredentials(path string) (oauthCreds, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return oauthCreds{}, err
	}
	var cf credentialsFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return oauthCreds{}, err
	}
	if cf.ClaudeAiOauth != nil && cf.ClaudeAiOauth.AccessToken != "" {
		return *cf.ClaudeAiOauth, nil
	}
	return cf.oauthCreds, nil
}
