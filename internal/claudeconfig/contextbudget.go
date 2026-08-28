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

func contextWindowEnv(window string) map[string]string {
	values := map[string]string{
		ContextBudgetKey:     window,
		autoCompactWindowKey: "",
		disable1MContextKey:  "",
	}
	tokens, _ := strconv.Atoi(window)
	if tokens > 0 && tokens < oneMillionContextSize {
		values[autoCompactWindowKey] = window
		values[disable1MContextKey] = "1"
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
	marker, _ := env["WISP_DECK_SUBSCRIPTION_PROVIDER"].(string)
	provider, marked := providerByKey(marker)
	userConfigured := marked && provider.UserConfigured
	if !marked {
		provider = providerFor(configName)
		userConfigured = provider.UserConfigured
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
	for key, value := range contextWindowEnv(window) {
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
	stampContextBudget(env, file)
	afterBudget, hasBudget := env[ContextBudgetKey].(string)
	afterAuto, hasAuto := env[autoCompactWindowKey].(string)
	afterDisable, hasDisable := env[disable1MContextKey].(string)
	if hadBudget == hasBudget && beforeBudget == afterBudget &&
		hadAuto == hasAuto && beforeAuto == afterAuto &&
		hadDisable == hasDisable && beforeDisable == afterDisable {
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
