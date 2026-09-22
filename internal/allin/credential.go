package allin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

// anthropicUpstream is where a Claude login's turn goes. A login has no
// endpoint of its own — only a credential.
const anthropicUpstream = "https://api.anthropic.com"

// KeychainService names the Keychain entry Claude Code writes for one config
// dir. The suffix is sha256 of the directory path, first eight hex characters;
// the default login (no CLAUDE_CONFIG_DIR) has no suffix. Only hashing, so it
// carries no build tag — keychain_darwin.go and keychain_other.go each keep
// their own keychainToken, the part that actually needs one.
func KeychainService(configDir string) string {
	const base = "Claude Code-credentials"
	if configDir == "" {
		return base
	}
	sum := sha256.Sum256([]byte(configDir))
	return base + "-" + hex.EncodeToString(sum[:])[:8]
}

// AccountConfigDir is the CLAUDE_CONFIG_DIR one login runs under. The implicit
// login has none, which is what gives it the unsuffixed Keychain service.
func AccountConfigDir(accountsDir, login string) string {
	if login == "" || login == "default" {
		return ""
	}
	return filepath.Join(accountsDir, login)
}

// AccountToken reads one login's OAuth token out of the Keychain. Resolve uses
// the same read to route a turn; the usage refresher uses it to ask the
// endpoint what that login has left.
func AccountToken(accountsDir, login string) (string, error) {
	return keychainToken(AccountConfigDir(accountsDir, login))
}

// ErrStaleAccount marks a login whose token could not be read. It is
// deterministic, so the router must surface it as 400: Claude Code retries a
// 401 about eleven times before giving up.
var ErrStaleAccount = errors.New("allin: account credential unavailable")

// Credential is the endpoint and auth header one target needs.
type Credential struct {
	BaseURL string
	Header  string
	Value   string
	// NeedsRepair marks a RemoteCatalog target (Featherless): proxy.go must
	// serve it through internal/rolefix's handler instead of the plain reverse
	// proxy, or it 400s on Claude Code's role:"system" messages and silently
	// stops parsing tool calls once a request carries `thinking`.
	NeedsRepair bool
	// DropTools names the tools this endpoint rejects the schema of, from the
	// target provider's own catalog entry. The profile-level deny cannot serve
	// this pane: All-In runs on the router profile, whose picker also carries
	// Claude rows that want the tool, so the drop is per request.
	DropTools []string
}

// Resolver answers what a parsed picker row should be sent with.
type Resolver interface {
	Resolve(Target) (Credential, error)
}

// ChatGPTBridge is the one thing this package knows about Codex: something that
// can hand it a loopback endpoint and a key. Everything else — the app-server
// child, the engine, the Anthropic translation, the lazy start, the shutdown —
// lives behind gptbridge.ChatGPTBridge, so this package stays "route a request
// and swap a credential".
//
// It is asked once per ChatGPT turn. Deduplication is the bridge's own job:
// asking here is how the router says "this turn needs Codex", not how it says
// "start one".
type ChatGPTBridge interface {
	Endpoint() (baseURL string, key string, err error)
}

// FileResolver reads the same files the account switcher owns. Token is a seam
// so tests never touch the real Keychain; Bridge is nil in every build that
// cannot start Codex, which is a refusal rather than a panic.
type FileResolver struct {
	Env    Env
	Token  func(configDir string) (string, error)
	Bridge ChatGPTBridge
	// Reconcile puts crossed logins back in their slots before a token is
	// read. Nil in tests, which must never reach the real home dir.
	Reconcile func()
}

func NewResolver(env Env) *FileResolver {
	return &FileResolver{Env: env, Token: keychainToken}
}

func (r *FileResolver) Resolve(target Target) (Credential, error) {
	if err := validSource(target.Source); err != nil {
		return Credential{}, err
	}
	switch target.Kind {
	case KindAccount:
		if r.Reconcile != nil {
			r.Reconcile()
		}
		token, err := r.Token(AccountConfigDir(r.Env.AccountsDir, target.Source))
		if err != nil || token == "" {
			return Credential{}, fmt.Errorf("%w: %s", ErrStaleAccount, target.Source)
		}
		return Credential{BaseURL: anthropicUpstream, Header: "Authorization", Value: "Bearer " + token}, nil
	case KindConfig:
		file := target.Source + ".json"
		provider, err := routableProfile(r.Env, file)
		if err != nil {
			return Credential{}, err
		}
		// Ahead of the key/endpoint read, which a ChatGPT profile fails by
		// design: Codex authenticates it and a bridge process serves it, so it
		// stores neither.
		if provider.Auth == claudeconfig.AuthCodexChatGPT {
			return r.chatGPTCredential()
		}
		key := claudeconfig.ReadAPIKey(r.Env.ConfigsDir, file)
		base := claudeconfig.ReadBaseURL(r.Env.ConfigsDir, file)
		if key == "" || base == "" {
			return Credential{}, fmt.Errorf("allin: profile %q is not ready", target.Source)
		}
		return Credential{
			BaseURL:     base,
			Header:      "Authorization",
			Value:       "Bearer " + key,
			NeedsRepair: provider.RemoteCatalog,
			DropTools:   provider.UnsupportedTools,
		}, nil
	}
	return Credential{}, errors.New("allin: session target needs no credential")
}

// chatGPTCredential starts (or reuses) the Codex bridge and addresses the turn
// at it. NeedsRepair stays false: rolefix rewrites a request for Featherless's
// stricter published schema, and the bridge reads the very fields it strips.
func (r *FileResolver) chatGPTCredential() (Credential, error) {
	if r.Bridge == nil {
		return Credential{}, errors.New(
			"allin: this session cannot serve the OpenAI / ChatGPT subscription — " +
				"it was launched without a Codex bridge")
	}
	base, key, err := r.Bridge.Endpoint()
	if err != nil {
		return Credential{}, fmt.Errorf("allin: the ChatGPT bridge could not start: %w", err)
	}
	if base == "" || key == "" {
		return Credential{}, errors.New("allin: the ChatGPT bridge reported no endpoint")
	}
	return Credential{BaseURL: base, Header: "Authorization", Value: "Bearer " + key}, nil
}

// routableAuth is the one rule the roster and the resolver must agree on:
// which authentication shapes this router can address at all. It is an
// allowlist rather than a "not AuthWispRouter" test, so a new AuthKind is
// refused until someone decides how to serve it — the safe direction, because
// an unserved row 400s a turn while an unoffered one costs nothing.
//
//   - AuthAPIKey: the profile stores the endpoint and the key; the router
//     swaps the header and forwards.
//   - AuthCodexChatGPT: the profile stores neither, and a lazily started Codex
//     bridge supplies both (see FileResolver.chatGPTCredential).
//   - AuthWispRouter is refused. The generated All-In profile sits in the very
//     configs list this iterates, so a row could name the router that is asking
//     for it. configRows also skips it by display name, but a renamed profile
//     keeps its marker, so this is the check that actually holds.
func routableAuth(auth claudeconfig.AuthKind) bool {
	return auth == claudeconfig.AuthAPIKey || auth == claudeconfig.AuthCodexChatGPT
}

// routableProfile refuses what routableAuth refuses. configRows decides what
// the picker OFFERS; this decides what the router will ADDRESS, and the id
// comes off the wire — hand-typed, or saved as a picker default by an older
// build — so the roster's exclusion is advice until it is enforced here too.
//
// RemoteCatalog (Featherless) is NOT refused: the caller (Resolve) reads the
// returned provider's RemoteCatalog bit and marks the credential NeedsRepair,
// so proxy.go routes it through internal/rolefix's handler instead of
// refusing it outright. UserConfigured is also not refused, for a different
// reason — a self-hosted endpoint speaks the Anthropic API directly and needs
// no repair at all. SuppliesOwnModel() is true for both, so keying either
// decision off it instead of RemoteCatalog would wrongly repair (or refuse) a
// self-hosted profile too.
func routableProfile(env Env, file string) (claudeconfig.Provider, error) {
	name := ""
	for _, config := range claudeconfig.Load(env.ConfigsList) {
		if config.File == file {
			name = config.Name
			break
		}
	}
	provider := claudeconfig.ProviderForConfig(env.ConfigsDir,
		claudeconfig.Config{Name: name, File: file})
	if !routableAuth(provider.Auth) {
		return provider, fmt.Errorf("allin: profile %q is served by %s, which this router cannot address",
			strings.TrimSuffix(file, ".json"), provider.Name)
	}
	return provider, nil
}

// validSource keeps a model id from naming a path. The id comes off the wire,
// so a "../" source would otherwise read any file the user can read.
func validSource(source string) error {
	if source == "" {
		return errors.New("allin: empty source")
	}
	if strings.ContainsAny(source, "/\\") || strings.Contains(source, "..") {
		return fmt.Errorf("allin: source %q is not a single name", source)
	}
	return nil
}
