package claudeconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
)

// ContextBudgetKey is Claude Code's override for its own auto-compact budget.
// Claude Code otherwise sizes that budget from ITS model catalog, which knows
// nothing about the server ANTHROPIC_BASE_URL points at: a subscription model
// it does not recognize gets a flat 200000, and a session model still carrying
// Anthropic's `[1m]` marker gets 1000000. Either figure can sit above the cap
// the provider actually enforces, and overshooting it is unrecoverable —
// /compact must itself send the oversized transcript, so it fails too.
//
// Claude Code honors this key only for models outside its own `claude-*`
// namespace, which is exactly the subscription case.
const ContextBudgetKey = "CLAUDE_CODE_MAX_CONTEXT_TOKENS"

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
// A config the catalog cannot size keeps whatever it had: inventing a number
// for an unknown provider would cap a session at a limit nobody enforces.
func stampContextBudget(env map[string]any) {
	models := make(map[string]string, len(envKeys))
	for _, key := range envKeys {
		if value, ok := env[key].(string); ok {
			models[key] = value
		}
	}
	if budget, ok := ContextBudget(models); ok {
		env[ContextBudgetKey] = strconv.Itoa(budget)
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
	before, had := env[ContextBudgetKey].(string)
	stampContextBudget(env)
	after, has := env[ContextBudgetKey].(string)
	if had == has && before == after {
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
