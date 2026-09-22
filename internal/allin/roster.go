package allin

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/jackuait/wisp-deck/internal/claudeaccount"
	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

// minRosterContext is the narrowest window worth offering. Claude Code's own
// floor is ~20k tokens before a conversation starts, and a profile reserves a
// quarter of the window for the reply, so anything tighter cannot finish a task.
const minRosterContext = 200000

// rosterWindow is the window an All-In session runs at, declared by routerEnv.
// It is 1M because every Claude row carries OneMillionMarker, which grants the
// whole SESSION 1M off the raw model string.
//
// The trade this accepts: a provider row is narrower than the session believes,
// so picking one after the transcript has grown past that endpoint's own cap
// fails, and /compact cannot recover — it resends the same oversized transcript
// plus a summarization prompt. Chosen deliberately over a uniform 200k.
const rosterWindow = 1000000

// Env names the files the roster is built from. AccountsList, AccountsDir,
// ConfigsList and ConfigsDir are the same four files the account switcher and
// the subscription modal already own, and EnsureProfileIfEligible refuses an
// Env missing any of them.
//
// DefaultLabelFile is OPTIONAL — deliberately not part of that required-field
// gate. claudeaccount.GetDefaultLabel already falls back to "Default" for an
// empty path, a missing file, or an empty one, so a construction site that
// forgets to plumb this field degrades to today's hardcoded label instead of
// breaking: EnsureProfileIfEligible would otherwise refuse every unplumbed
// call site's Env outright, silently stopping it from ever maintaining the
// profile again (see the four-field comment on EnsureProfileIfEligible).
type Env struct {
	AccountsList     string
	AccountsDir      string
	ConfigsList      string
	ConfigsDir       string
	DefaultLabelFile string
}

// Row is one entry of the settings key `modelPicker.options`.
//
// There is deliberately no behavesAs. Measured on a live pane: an unknown model
// id without it is given thinking:{"type":"adaptive"} and keeps effort
// available, while behavesAs:claude-sonnet-4-5 turns that into
// thinking:{"type":"enabled",budget} and "Effort not supported". Absent is the
// more capable default, and it carries no context window across the router
// either — so the field bought nothing and cost the pane its effort control.
type Row struct {
	Model       string `json:"model"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
}

// claudeModel is one first-party model offered for every Claude login.
type claudeModel struct {
	id    string
	label string
}

// claudeLineup is the fallback until the router has cached Anthropic's own
// list (modelcache.go). An id it does not know still routes fine.
var claudeLineup = []claudeModel{
	{"claude-opus-5-5", "Opus 5.5"},
	{"claude-sonnet-5", "Sonnet 5"},
	{"claude-fable-5-1", "Fable 5.1"},
	{"claude-haiku-4-5-20251001", "Haiku 4.5"},
}

// claudeModels is the cached Anthropic list, or the pinned lineup.
func claudeModels(cache ModelCache) []claudeModel {
	entry, ok := cache[anthropicCacheKey]
	if !ok || len(entry.Models) == 0 {
		return claudeLineup
	}
	out := make([]claudeModel, 0, len(entry.Models))
	for _, m := range entry.Models {
		label := strings.TrimPrefix(m.Label, "Claude ")
		if label == "" {
			label = m.ID
		}
		out = append(out, claudeModel{id: m.ID, label: label})
	}
	return out
}

// SourceCount reports how many distinct credentials the roster spans: each
// Claude login and each routable provider profile counts once, however many
// models it contributes. It is derived from Roster itself, so the two can never
// disagree about what this build can actually offer.
func SourceCount(env Env) int {
	seen := map[string]bool{}
	for _, row := range Roster(env) {
		target := Route(row.Model)
		if target.Kind == KindSession || target.Source == "" {
			continue
		}
		seen[fmt.Sprintf("%d/%s", target.Kind, target.Source)] = true
	}
	return len(seen)
}

// Roster builds every picker row, accounts first, then configured providers.
// A source that cannot be read contributes nothing rather than failing the set.
func Roster(env Env) []Row {
	rows := accountRows(env)
	return append(rows, configRows(env)...)
}

func accountRows(env Env) []Row {
	// The implicit login can be tagged by the user (Subscriptions modal's
	// LOGINS section); GetDefaultLabel returns that tag, or "Default" when
	// none was ever set. The row id below still names the directory
	// ("acct.default"), never the label, so a saved picker default survives
	// a later relabel untouched.
	accounts := []struct{ label, dir string }{
		{claudeaccount.GetDefaultLabel(env.DefaultLabelFile), "default"},
	}
	for _, line := range readLines(env.AccountsList) {
		label, dir, ok := strings.Cut(line, ":")
		if !ok || label == "" || dir == "" {
			continue
		}
		accounts = append(accounts, struct{ label, dir string }{label, dir})
	}
	lineup := claudeModels(LoadModelCache(ModelCachePath(env)))
	var rows []Row
	for _, account := range accounts {
		for _, model := range lineup {
			// The marker must trail the whole id: Claude Code matches it at the
			// end of the raw model string, and Route strips it there too.
			rows = append(rows, Row{
				Model:       fmt.Sprintf("wisp/acct.%s/%s%s", account.dir, model.id, OneMillionMarker),
				Label:       account.label + " · " + model.label,
				Description: "Claude subscription: " + account.label,
			})
		}
	}
	return rows
}

type routableConfig struct {
	Config   claudeconfig.Config
	Provider claudeconfig.Provider
}

// routableConfigs is every profile the picker may offer. The refresher walks
// the same list, so it never fetches for a row the picker would not show.
func routableConfigs(env Env) []routableConfig {
	var out []routableConfig
	// A disabled subscription stays fully manageable in the modal but is
	// hidden from the in-session switcher popup (claudeconfig.LoadDisabled);
	// All-In must treat it the same way — the user turned it off.
	disabled := claudeconfig.LoadDisabled(claudeconfig.DisabledFile(env.ConfigsList))
	for _, config := range claudeconfig.Load(env.ConfigsList) {
		if disabled[config.File] {
			continue
		}
		// The generated profile is registered in the very list this iterates,
		// and a row naming it would send a turn back into the router that asked
		// for it. Its own AuthWispRouter identity is skipped by the Auth check
		// below, but that identity is only on disk AFTER EnsureProfile writes —
		// and EnsureProfile computes these rows first, from whatever profile it
		// adopted under this name. So the name is the only guard that holds on
		// the call that matters.
		if strings.EqualFold(config.Name, ProfileName) {
			continue
		}
		if !claudeconfig.ConfigReady(env.ConfigsDir, config) {
			continue
		}
		provider := claudeconfig.ProviderForConfig(env.ConfigsDir, config)
		// routableAuth (credential.go) is the same rule Resolve enforces, so
		// the picker can never offer a row the router refuses. It admits an
		// API-key provider and a ChatGPT one — the latter has no endpoint and
		// no key of its own, and is served by the Codex bridge instead.
		if !routableAuth(provider.Auth) {
			continue
		}
		out = append(out, routableConfig{Config: config, Provider: provider})
	}
	return out
}

func configRows(env Env) []Row {
	var rows []Row
	cache := LoadModelCache(ModelCachePath(env))
	for _, rc := range routableConfigs(env) {
		config, provider := rc.Config, rc.Provider
		// RemoteCatalog (Featherless) is offered too: proxy.go delegates a
		// resolved RemoteCatalog target to internal/rolefix's own handler,
		// which repairs the role:"system" 400 and the thinking-disables-tool-
		// parsing failure on the way through. See credential.go's Resolve.
		source := strings.TrimSuffix(config.File, ".json")
		for _, model := range offeredModels(env, config, provider, cache) {
			if model.Context != 0 && model.Context < minRosterContext {
				continue
			}
			rows = append(rows, Row{
				Model:       fmt.Sprintf("wisp/cfg.%s/%s", source, model.ID),
				Label:       config.Name + " · " + model.ID,
				Description: provider.Name,
			})
		}
	}
	return rows
}

// offeredModels prefers the cached live list. A model's window comes from the
// listing, then the catalog, else stays 0 (unknown, admitted by the floor).
func offeredModels(env Env, config claudeconfig.Config, provider claudeconfig.Provider, cache ModelCache) []claudeconfig.Model {
	entry, ok := cache[configCacheKey(config.File)]
	if provider.SuppliesOwnModel() || !ok || len(entry.Models) == 0 {
		return providerModels(env, config, provider)
	}
	out := make([]claudeconfig.Model, 0, len(entry.Models))
	for _, m := range entry.Models {
		window := m.Context
		if window == 0 {
			window, _, _ = claudeconfig.ModelLimit(m.ID)
		}
		out = append(out, claudeconfig.Model{ID: m.ID, Context: window})
	}
	return out
}

// providerModels returns the catalog's models, or the single model the user
// supplied for a provider that ships none. If a user-supplied model's declared
// window does not parse or is too small, the provider is skipped.
func providerModels(env Env, config claudeconfig.Config, provider claudeconfig.Provider) []claudeconfig.Model {
	if provider.SuppliesOwnModel() {
		id := claudeconfig.ReadCustomModel(env.ConfigsDir, config.File)
		if id == "" {
			return nil
		}
		windowStr := claudeconfig.ReadContextWindow(env.ConfigsDir, config.File)
		window, err := strconv.Atoi(strings.TrimSpace(windowStr))
		if err != nil || window <= 0 {
			return nil
		}
		return []claudeconfig.Model{{ID: id, Context: window}}
	}
	return provider.Models
}

func readLines(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}
