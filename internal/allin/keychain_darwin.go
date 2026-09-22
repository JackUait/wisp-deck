//go:build darwin

package allin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/jackuait/wisp-deck/internal/proxy"
)

// keychainToken answers the access token one login's turn should carry. It
// refreshes the stored token first when that token has expired: nothing else
// does, because Claude Code only refreshes the login of the config dir it is
// running under and All-In never starts a process under the dirs it borrows
// credentials from.
func keychainToken(configDir string) (string, error) {
	return freshToken(keychainCLI{}, anthropicRefresh, configDir, time.Now)
}

func anthropicRefresh(refreshToken string) (oauthCredential, error) {
	tokens, err := proxy.RefreshToken(proxy.DefaultTokenEndpoint, refreshToken)
	if err != nil {
		return oauthCredential{}, err
	}
	return oauthCredential{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		ExpiresAt:    tokens.ExpiresAt,
	}, nil
}

// keychainCLI is the real store, driven through the `security` binary because
// the entries belong to Claude Code and are addressed by its service name.
type keychainCLI struct{}

func (keychainCLI) read(configDir string) (oauthCredential, error) {
	blob, err := keychainBlob(configDir)
	if err != nil {
		return oauthCredential{}, err
	}
	var parsed struct {
		OAuth struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
			ExpiresAt    int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(blob, &parsed); err != nil {
		return oauthCredential{}, fmt.Errorf("allin: parse keychain blob: %w", err)
	}
	return oauthCredential{
		AccessToken:  parsed.OAuth.AccessToken,
		RefreshToken: parsed.OAuth.RefreshToken,
		ExpiresAt:    parsed.OAuth.ExpiresAt,
	}, nil
}

// write patches the three OAuth fields back into the existing blob. It never
// rebuilds the blob: subscriptionType and scopes live beside them and belong
// to Claude Code, which reads this same entry when a pane runs under this dir.
func (keychainCLI) write(configDir string, cred oauthCredential) error {
	blob, err := keychainBlob(configDir)
	if err != nil {
		return err
	}
	root := map[string]any{}
	if err := json.Unmarshal(blob, &root); err != nil {
		return fmt.Errorf("allin: parse keychain blob: %w", err)
	}
	oauth, _ := root["claudeAiOauth"].(map[string]any)
	if oauth == nil {
		oauth = map[string]any{}
	}
	oauth["accessToken"] = cred.AccessToken
	oauth["refreshToken"] = cred.RefreshToken
	oauth["expiresAt"] = cred.ExpiresAt
	root["claudeAiOauth"] = oauth
	updated, err := json.Marshal(root)
	if err != nil {
		return err
	}
	return keychainPut(configDir, updated)
}

// keychainPut replaces one login's whole entry.
func keychainPut(configDir string, blob []byte) error {
	account, err := keychainAccount(configDir)
	if err != nil {
		return err
	}
	service := KeychainService(configDir)
	// -U updates the item these attributes already match; without it `security`
	// refuses rather than replacing.
	out, err := exec.Command("security", "add-generic-password",
		"-U", "-a", account, "-s", service, "-w", string(blob)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("allin: storing the login failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (KeychainLogins) Login(configDir string) (json.RawMessage, error) {
	blob, err := keychainBlob(configDir)
	if err != nil {
		return nil, err
	}
	return loginOf(blob)
}

func (KeychainLogins) SetLogin(configDir string, login json.RawMessage) error {
	blob, err := keychainBlob(configDir)
	if err != nil {
		return err
	}
	updated, err := withLogin(blob, login)
	if err != nil {
		return err
	}
	return keychainPut(configDir, updated)
}

// Lock is the refresh lock, so a move never interleaves with a borrowed
// login's refresh writing the same entry.
func (KeychainLogins) Lock(configDir string) (func(), error) {
	return keychainCLI{}.lock(configDir)
}

// lock serializes the refresh across every process sharing one login. The file
// is keyed by the Keychain service name, so two logins never wait on each
// other and the default login (which has no config dir) still gets its own.
func (keychainCLI) lock(configDir string) (func(), error) {
	sum := sha256.Sum256([]byte(KeychainService(configDir)))
	path := filepath.Join(os.TempDir(), "wisp-deck-allin-refresh-"+hex.EncodeToString(sum[:])[:16]+".lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close() //nolint:errcheck -- the lock was never taken
		return nil, err
	}
	return func() { file.Close() }, nil //nolint:errcheck -- closing the fd releases the lock
}

// keychainBlob reads one login's raw entry. Both read and write go through it,
// so the `security` read appears once in this package's spawn audit.
func keychainBlob(configDir string) ([]byte, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", KeychainService(configDir), "-w").Output()
	if err != nil {
		return nil, err
	}
	return []byte(strings.TrimSpace(string(out))), nil
}

// keychainAccountPattern pulls the item's account attribute out of `security`'s
// attribute dump. The write has to name it: -U matches on the attributes it is
// given, so a guessed account would create a SECOND item under the same service
// and leave the read answering whichever one the Keychain returns first.
var keychainAccountPattern = regexp.MustCompile(`"acct"<blob>="(.*)"`)

func keychainAccount(configDir string) (string, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", KeychainService(configDir)).Output()
	if err != nil {
		return "", err
	}
	match := keychainAccountPattern.FindSubmatch(out)
	if match == nil {
		return "", errors.New("allin: the keychain entry names no account")
	}
	return string(match[1]), nil
}
