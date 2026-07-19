// Package subusage fetches REAL 5-hour / 7-day usage percentages for
// subscription backends (the claude-configs), so the statusline can show the
// same 5h/7d bars for a subscription pane that native Claude logins get from
// Claude Code's rate_limits. Providers without a usage API (MiMo, Moonshot)
// report unsupported rather than fabricate data.
//
// The on-disk cache deliberately mirrors Claude Code's statusline rate_limits
// JSON shape ("rate_limits":{"five_hour":{"used_percentage":N},...}) so the
// existing bash extractors (gt_five_hour_used_pct / gt_weekly_used_pct in
// lib/statusline.sh) parse it without a new parser.
package subusage

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

// Window is one rate-limit window's usage.
type Window struct {
	UsedPercentage float64 `json:"used_percentage"`
	ResetAt        int64   `json:"reset_at,omitempty"` // unix seconds
}

// RateLimits carries the two windows the statusline bars render. A nil window
// means the provider reported no data for it (the bar hides).
type RateLimits struct {
	FiveHour *Window `json:"five_hour,omitempty"`
	SevenDay *Window `json:"seven_day,omitempty"`
}

// Snapshot is the cached fetch result. FetchedAt stamps the last SUCCESSFUL
// fetch (freshness gate for display); CheckedAt stamps the last attempt
// (throttle gate for refresh), so a failing provider is retried on the same
// cadence instead of every statusline render.
type Snapshot struct {
	RateLimits RateLimits `json:"rate_limits"`
	Provider   string     `json:"provider,omitempty"`
	FetchedAt  int64      `json:"fetched_at"`
	CheckedAt  int64      `json:"checked_at,omitempty"`
}

// QuotaHost reduces an Anthropic-compatible gateway base URL
// (https://api.z.ai/api/anthropic) to its scheme+host — the root the
// provider's monitor/usage API hangs off.
func QuotaHost(baseURL string) string {
	if baseURL == "" {
		return ""
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// FetchZhipu queries z.ai's coding-plan quota endpoint. The token is sent RAW
// in Authorization (no Bearer prefix — that is what the endpoint
// authenticates). An account without a coding plan answers HTTP 200 with an
// unsuccessful envelope; that is a real "no usage data" answer, not an error.
func FetchZhipu(client *http.Client, host, token string) (RateLimits, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(host, "/")+"/api/monitor/usage/quota/limit", nil)
	if err != nil {
		return RateLimits{}, err
	}
	req.Header.Set("Authorization", token)
	req.Header.Set("Accept-Language", "en-US,en")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return RateLimits{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return RateLimits{}, fmt.Errorf("zhipu quota endpoint: HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Limits []struct {
				Type          string  `json:"type"`
				Unit          int     `json:"unit"`
				Number        int     `json:"number"`
				Percentage    float64 `json:"percentage"`
				NextResetTime int64   `json:"nextResetTime"` // ms
			} `json:"limits"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return RateLimits{}, err
	}
	var rl RateLimits
	if !payload.Success {
		return rl, nil
	}
	for _, limit := range payload.Data.Limits {
		if limit.Type != "TOKENS_LIMIT" {
			continue
		}
		w := &Window{UsedPercentage: limit.Percentage, ResetAt: limit.NextResetTime / 1000}
		// unit/number encode the window: 3/5 = the 5-hour cycle, 6/1 = weekly.
		switch {
		case limit.Unit == 3 && limit.Number == 5:
			rl.FiveHour = w
		case limit.Unit == 6 && limit.Number == 1:
			rl.SevenDay = w
		}
	}
	return rl, nil
}

// FetchChatGPT queries the ChatGPT Codex usage endpoint ({base}/wham/usage)
// with the login `codex login` persisted in auth.json. Windows are classified
// by limit_window_seconds, NOT position: a Pro account reports its weekly
// window as primary with no secondary.
func FetchChatGPT(client *http.Client, baseURL, authPath string) (RateLimits, error) {
	data, err := os.ReadFile(authPath)
	if err != nil {
		return RateLimits{}, err
	}
	var auth struct {
		Tokens struct {
			AccessToken string `json:"access_token"`
			AccountID   string `json:"account_id"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(data, &auth); err != nil {
		return RateLimits{}, err
	}
	if auth.Tokens.AccessToken == "" {
		return RateLimits{}, errors.New("codex auth.json has no access token")
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/wham/usage", nil)
	if err != nil {
		return RateLimits{}, err
	}
	req.Header.Set("Authorization", "Bearer "+auth.Tokens.AccessToken)
	if auth.Tokens.AccountID != "" {
		req.Header.Set("chatgpt-account-id", auth.Tokens.AccountID)
	}
	req.Header.Set("User-Agent", "wisp-deck")
	resp, err := client.Do(req)
	if err != nil {
		return RateLimits{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return RateLimits{}, fmt.Errorf("chatgpt usage endpoint: HTTP %d", resp.StatusCode)
	}
	type window struct {
		UsedPercent        float64 `json:"used_percent"`
		LimitWindowSeconds int64   `json:"limit_window_seconds"`
		ResetAt            int64   `json:"reset_at"`
	}
	var payload struct {
		RateLimit struct {
			PrimaryWindow   *window `json:"primary_window"`
			SecondaryWindow *window `json:"secondary_window"`
		} `json:"rate_limit"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return RateLimits{}, err
	}
	var rl RateLimits
	for _, w := range []*window{payload.RateLimit.PrimaryWindow, payload.RateLimit.SecondaryWindow} {
		if w == nil {
			continue
		}
		mapped := &Window{UsedPercentage: w.UsedPercent, ResetAt: w.ResetAt}
		// Anything up to a day is the rolling session window (Codex uses 5h =
		// 18000s); longer is the weekly window (604800s).
		if w.LimitWindowSeconds <= 86400 {
			rl.FiveHour = mapped
		} else {
			rl.SevenDay = mapped
		}
	}
	return rl, nil
}

// ChatGPTBaseURL is the production Codex backend root FetchChatGPT hits;
// tests substitute an httptest server.
const ChatGPTBaseURL = "https://chatgpt.com/backend-api"

// Fetch resolves the config's provider and fetches its real usage. ok=false
// means the provider has no usage API (never an error): callers cache the
// empty answer so the pane shows no bars rather than fake ones.
func Fetch(client *http.Client, configsDir, listFile, configFile, codexAuthPath string) (Snapshot, bool, error) {
	cfg := claudeconfig.Config{Name: strings.TrimSuffix(configFile, ".json"), File: configFile}
	for _, c := range claudeconfig.Load(listFile) {
		if c.File == configFile {
			cfg = c
			break
		}
	}
	provider := claudeconfig.ProviderForConfig(configsDir, cfg)
	snap := Snapshot{Provider: provider.Key}
	switch provider.Key {
	case "zhipu":
		host := QuotaHost(claudeconfig.ReadBaseURL(configsDir, configFile))
		if host == "" {
			host = QuotaHost(provider.BaseURL)
		}
		rl, err := FetchZhipu(client, host, claudeconfig.ReadAPIKey(configsDir, configFile))
		if err != nil {
			return snap, true, err
		}
		snap.RateLimits = rl
		return snap, true, nil
	case "openai-chatgpt":
		if codexAuthPath == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return snap, true, err
			}
			codexAuthPath = filepath.Join(home, ".codex", "auth.json")
		}
		rl, err := FetchChatGPT(client, ChatGPTBaseURL, codexAuthPath)
		if err != nil {
			return snap, true, err
		}
		snap.RateLimits = rl
		return snap, true, nil
	}
	return snap, false, nil
}

// ReadCache loads a previously written snapshot.
func ReadCache(path string) (Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, err
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

// WriteCache atomically persists the snapshot (tmp + rename, so a concurrent
// statusline read never sees a torn file).
func WriteCache(path string, snap Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".subusage-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
