package claudeconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ContextBudgetKey declares the model window Claude Code's catalog cannot know.
// A sub-1M window also needs a direct auto-compact cap: a global `[1m]` model
// marker is resolved before this key and otherwise lets the session grow past
// the endpoint's limit. /compact cannot recover after that because it sends the
// same oversized transcript.
const (
	ContextBudgetKey      = "CLAUDE_CODE_MAX_CONTEXT_TOKENS"
	autoCompactWindowKey  = "CLAUDE_CODE_AUTO_COMPACT_WINDOW"
	disable1MContextKey   = "CLAUDE_CODE_DISABLE_1M_CONTEXT"
	oneMillionContextSize = 1000000
)

// OutputReserveKey caps the max_tokens Claude Code asks for. It sizes that
// figure from its own catalog, which knows nothing about a subscription
// endpoint, and settles on 32000 — the whole of a small window. An endpoint
// enforcing input + max_tokens <= context then rejects every turn a real
// session sends, before the model reads a word of it.
//
// Measured against api.featherless.ai on 2026-09-02 by replaying a request
// captured from a live pane against a model with a 32768-token window:
// max_tokens 32000 and 30000 both answered 400 "The request was rejected as
// invalid", 28000 and below answered 200, and adding ~4000 tokens of input
// moved the boundary down by the same amount. The identical profile carrying an
// 8192 reserve ran the turn to completion.
const OutputReserveKey = "CLAUDE_CODE_MAX_OUTPUT_TOKENS"

// claudeDefaultOutputTokens is the max_tokens Claude Code asks for unprompted.
// The reserve never exceeds it: this exists to fit a small window, not to let a
// session ask a provider for more than Claude would have asked for anyway.
const claudeDefaultOutputTokens = 32000

// outputReserveShare is how much of a window the reply may claim. A quarter
// leaves three quarters for the system prompt, the tool schemas and the
// conversation.
const outputReserveShare = 4

// outputReserve is the room a reply needs inside a window of the given size.
// Never zero: a window that small is already unusable, and max_tokens 0 is
// rejected outright, which would fail the pane for the wrong reason.
func outputReserve(window int) int {
	reserve := window / outputReserveShare
	if reserve > claudeDefaultOutputTokens {
		return claudeDefaultOutputTokens
	}
	if reserve < 1 {
		return 1
	}
	return reserve
}

// contextWindowEnv renders the keys a declared window implies. declaredReserve
// is the profile's own output cap, or 0 to derive one from the window.
//
// Declaring the reserve is the whole of the fit: Claude Code's own auto-compact
// buffer is sized from it, so once it is right the room for the reply is
// already carved out of the window. Measured on a live pane with a 32768-token
// model — /context reported "Autocompact buffer: 33k tokens (100.7%)" and no
// free space at all before, "21.2k (64.7%)" with "Free space: 9.3k (28.3%)"
// after. Taking the reserve out of the auto-compact window on top of that
// changes nothing: 32768, 24576, 20000 and 10000 all produced identical
// accounting, so that key keeps naming the window.
func contextWindowEnv(window string, declaredReserve int) map[string]string {
	values := map[string]string{
		ContextBudgetKey:     window,
		autoCompactWindowKey: "",
		disable1MContextKey:  "",
		OutputReserveKey:     "",
	}
	tokens, _ := strconv.Atoi(window)
	if tokens > 0 && tokens < oneMillionContextSize {
		reserve := declaredReserve
		if reserve <= 0 || reserve >= tokens {
			reserve = outputReserve(tokens)
		}
		values[autoCompactWindowKey] = window
		values[disable1MContextKey] = "1"
		values[OutputReserveKey] = strconv.Itoa(reserve)
	}
	return values
}

// ContextBudget returns the window every model a config can run fits inside:
// the smallest catalog context across its four ANTHROPIC_DEFAULT_*_MODEL
// mappings. One env var governs the whole session — /model and subagents move
// freely between all four aliases — so the smallest has to win; anything larger
// would let the session grow past whichever mapped model has the tightest cap.
// Models absent from the catalog carry no known window and cannot lower it.
func ContextBudget(env map[string]string) (int, bool) {
	budget := 0
	for _, key := range envKeys {
		window, _, ok := ModelLimit(env[key])
		if !ok || window <= 0 {
			continue
		}
		if budget == 0 || window < budget {
			budget = window
		}
	}
	return budget, budget > 0
}

// stampContextBudget rewrites the declared window on a decoded settings env.
// A custom profile owns its declared window even when its model is cataloged.
// Other profiles use the catalog first and never invent an unknown limit.
func stampContextBudget(env map[string]any, configName string) {
	models := make(map[string]string, len(envKeys))
	for _, key := range envKeys {
		if value, ok := env[key].(string); ok {
			models[key] = value
		}
	}
	declaredWindow := ""
	if declared, ok := env[ContextBudgetKey].(string); ok {
		tokens, err := strconv.Atoi(strings.TrimSpace(declared))
		if err == nil && tokens > 0 {
			declaredWindow = strconv.Itoa(tokens)
		}
	}
	// A reserve the profile already declares is the user's own figure for an
	// endpoint whose output cap they know, so it survives every sweep.
	declaredReserve := 0
	if declared, ok := env[OutputReserveKey].(string); ok {
		if tokens, err := strconv.Atoi(strings.TrimSpace(declared)); err == nil && tokens > 0 {
			declaredReserve = tokens
		}
	}
	marker, _ := env["WISP_DECK_SUBSCRIPTION_PROVIDER"].(string)
	provider, marked := providerByKey(marker)
	userConfigured := marked && provider.SuppliesOwnModel()
	if !marked {
		provider = providerFor(configName)
		userConfigured = provider.SuppliesOwnModel()
		endpoint, _ := env["ANTHROPIC_BASE_URL"].(string)
		endpoint = normalizeCustomEndpoint(endpoint)
		if endpoint != "" {
			catalogEndpoint := false
			for _, candidate := range Providers {
				if candidate.BaseURL != "" && normalizeCustomEndpoint(candidate.BaseURL) == endpoint {
					catalogEndpoint = true
					break
				}
			}
			// Profiles predating provider markers are self-hosted when their endpoint
			// belongs to no catalog provider, regardless of their filename.
			userConfigured = userConfigured || !catalogEndpoint
		}
	}
	window := ""
	if userConfigured {
		window = declaredWindow
	} else if budget, ok := ContextBudget(models); ok {
		window = strconv.Itoa(budget)
	} else {
		window = declaredWindow
	}
	if window == "" {
		return
	}
	for key, value := range contextWindowEnv(window, declaredReserve) {
		if value == "" {
			delete(env, key)
		} else {
			env[key] = value
		}
	}
}

// EnsureContextBudget backfills the declared window on a config already on
// disk, reporting whether the file changed. Profiles created before the window
// was declared are the ones that hit this bug, so they cannot be left alone;
// re-running it on a current config must be a no-op, since every launch path
// may call it.
func EnsureContextBudget(configsDir, file string) (bool, error) {
	path := filepath.Join(configsDir, file)
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return false, err
	}
	env, _ := settings["env"].(map[string]any)
	if env == nil {
		return false, nil
	}
	beforeBudget, hadBudget := env[ContextBudgetKey].(string)
	beforeAuto, hadAuto := env[autoCompactWindowKey].(string)
	beforeDisable, hadDisable := env[disable1MContextKey].(string)
	beforeReserve, hadReserve := env[OutputReserveKey].(string)
	stampContextBudget(env, file)
	afterBudget, hasBudget := env[ContextBudgetKey].(string)
	afterAuto, hasAuto := env[autoCompactWindowKey].(string)
	afterDisable, hasDisable := env[disable1MContextKey].(string)
	afterReserve, hasReserve := env[OutputReserveKey].(string)
	if hadBudget == hasBudget && beforeBudget == afterBudget &&
		hadAuto == hasAuto && beforeAuto == afterAuto &&
		hadDisable == hasDisable && beforeDisable == afterDisable &&
		hadReserve == hasReserve && beforeReserve == afterReserve {
		return false, nil
	}
	settings["env"] = env
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, err
	}
	if err := writeSecure(path, append(out, '\n')); err != nil {
		return false, err
	}
	return true, nil
}

// EnsureContextBudgetAll backfills every profile in configsDir and reports how
// many changed. A profile it cannot read or parse is skipped rather than
// failing the sweep: one hand-edited file must not leave every other profile
// unprotected.
func EnsureContextBudgetAll(configsDir string) (int, error) {
	entries, err := os.ReadDir(configsDir)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		did, err := EnsureContextBudget(configsDir, entry.Name())
		if err == nil && did {
			changed++
		}
	}
	return changed, nil
}
