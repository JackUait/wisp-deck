package allin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

// ProfileName is the display name of the generated profile. It is matched
// verbatim in the configs list, so renaming it orphans the existing one.
const ProfileName = "All-In"

// EnsureProfile creates the All-In profile when absent and rewrites its picker
// rows every call — logins and providers come and go, and a stale roster offers
// models the machine can no longer reach.
//
// Only modelPicker and the env keys routerEnv names are written; they are
// rewritten every call because each one is load-bearing (see routerEnv) and a
// profile missing any of them fails silently. A key routerEnv maps to "" is
// deleted, so a window from an older profile cannot survive a refresh. Every
// other key in the file is the user's, and the launch overlay copies the whole
// object.
func EnsureProfile(env Env, listFile, configsDir string) (string, error) {
	file := ProfileFile(listFile)
	if file == "" {
		created, err := claudeconfig.Add(listFile, configsDir, ProfileName)
		if err != nil {
			return "", err
		}
		file = created
	}

	path := filepath.Join(configsDir, file)
	settings := map[string]any{}
	// A missing file starts empty; any other read or parse failure must not
	// be treated as "no keys" — that would silently drop the user's own keys
	// (env, permissions, an API key) the next time this writes the file.
	if data, err := os.ReadFile(path); err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
	} else if err := json.Unmarshal(data, &settings); err != nil {
		return "", err
	}
	settingsEnv, _ := settings["env"].(map[string]any)
	if settingsEnv == nil {
		settingsEnv = map[string]any{}
	}
	for key, value := range routerEnv() {
		if value == "" {
			delete(settingsEnv, key)
			continue
		}
		settingsEnv[key] = value
	}
	settings["env"] = settingsEnv

	usage := Usage(env, time.Now())
	rows := AnnotateUsage(Roster(env), usage)
	hidden := LoadHidden(HiddenFile(env.ConfigsList))
	options := make([]Row, 0, len(rows))
	for _, row := range rows {
		if hidden[BareModel(row.Model)] {
			continue
		}
		options = append(options, row)
	}
	// A subscription with nothing left answers every turn with a limit error,
	// so those rows are dropped unless the user turned the setting off. The
	// filter is applied on top of the hidden set and only when something
	// survives it: a deck whose subscriptions are all spent still needs a
	// picker it can pick from.
	if LoadHideExhausted(HideExhaustedFile(env.ConfigsList)) {
		live := make([]Row, 0, len(options))
		for _, row := range options {
			if !usage[SourceKey(row.Model)].Exhausted() {
				live = append(live, row)
			}
		}
		if len(live) > 0 {
			options = live
		}
	}
	// replaceBuiltInOptions leaves no built-in row to fall back on, so an empty
	// options list is a picker with nothing to pick, in a session that has no
	// other way to change model. The modal refuses to hide the last row; a
	// hand-edited file still reaches here.
	if len(options) == 0 {
		options = append(options, rows...)
	}
	settings["modelPicker"] = map[string]any{
		// Every row names its account explicitly, so the built-in lineup would
		// only add rows whose credential is ambiguous.
		"replaceBuiltInOptions": true,
		"options":               options,
	}

	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", err
	}
	// Published by rename: a reader must never see half a settings file. The
	// temp name is unique because every All-In router can rewrite this at once.
	temporary, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return "", err
	}
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		_ = temporary.Close()
		_ = os.Remove(temporary.Name())
		return "", err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporary.Name())
		return "", err
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		_ = os.Remove(temporary.Name())
		return "", err
	}
	return file, nil
}

// EnsureProfileIfEligible applies the create-vs-refresh gate every mutation
// site shares (the CLI's own add/delete/ensure-allin, and the TUI's
// login/subscription add and delete): create the profile only once the
// machine has two or more sources, but refresh one that already exists no
// matter how the count moves — a login or provider removed today would
// otherwise leave rows that answer 400 for the life of the profile.
//
// A caller missing any of the four Env paths gets an error rather than a
// silent no-op: Roster reads an empty AccountsList/ConfigsList as "nothing
// there", so refreshing from an incomplete Env would silently strip real
// logins or providers out of an existing picker. Every one of today's six
// call sites discards the error (this must never break the mutation the user
// asked for), so an error here is the only signal a structurally broken
// caller leaves behind — a silent nil would make that caller invisible
// forever. No caller legitimately has an empty path — every site builds all
// four from the same config root.
func EnsureProfileIfEligible(env Env) error {
	if env.AccountsList == "" || env.AccountsDir == "" ||
		env.ConfigsList == "" || env.ConfigsDir == "" {
		return fmt.Errorf("allin: incomplete Env: AccountsList, AccountsDir, ConfigsList and ConfigsDir must all be set")
	}
	if SourceCount(env) < 2 && ProfileFile(env.ConfigsList) == "" {
		return nil
	}
	file, err := EnsureProfile(env, env.ConfigsList, env.ConfigsDir)
	if err != nil {
		return err
	}
	// A profile born here has no other creation path to reach it: it is
	// never copied from a default (there is no default) and never swept by
	// bin/wisp-deck's own ensure-watchdog, which only sees a file already on
	// disk. Never move this into routerEnv — that block rewrites its keys on
	// every call, while a declared watchdog value is documented as the
	// user's own and every other path keeps it untouched.
	_, err = claudeconfig.EnsureStreamWatchdog(env.ConfigsDir, file)
	return err
}

// routerEnv is the env block the generated profile must carry. Each key exists
// because something silently stops working without it:
//
//   - The marker resolves the profile's identity. Without it ProviderForConfig
//     falls back to alias-matching the display name, which matches nothing, so
//     All-In would label and colour itself as Providers[0] — Zhipu / GLM.
//   - ANTHROPIC_BASE_URL is what claude-allin reads to decide there is anything
//     to wrap. With no endpoint, rolefix.UpstreamFromSettings errors and
//     runLoopbackWrappedLaunch runs the child unwrapped: the launch gate still
//     fires (it greps for the picker rows), the router never starts, and every
//     wisp/… id is sent verbatim to the session's own upstream. The value is
//     the real endpoint; the launch rewrites the session's OVERLAY, never this
//     file, so the stored profile keeps naming the truth.
//   - The window keys are the session's 1M declaration, and they have to be
//     declared here: this is the one profile stampContextBudget cannot size on
//     its own, because All-In has no model mappings to compute a window from.
//   - CLAUDE_CODE_DISABLE_EXPLORE_INHERIT_CAP is what keeps an Explore subagent
//     on the row the user picked. Claude Code hands the built-in Explore agent
//     {inheritCap:"opus"} whenever the session model's id names no Claude
//     family — which every wisp/… row is — and "opus" resolves to the
//     first-party claude-opus-5. The router reads that id as an unrouted row
//     and spends the session's own login instead (see the package gotchas).
//   - CLAUDE_CODE_SUBAGENT_MODEL_FORCE is the same protection for every other
//     subagent. Inheriting is not enough: a model named on the Agent call
//     (`model: "sonnet"`) or in an agent's frontmatter outranks it, and with
//     no ANTHROPIC_DEFAULT_*_MODEL aliases to resolve through it becomes the
//     first-party claude-sonnet-5 / claude-haiku-4-5 — another unrouted row,
//     billed to the session's own login. The force drops both sources, so the
//     subagent inherits the session model and routes like any other turn.
//     It pairs with the key above rather than replacing it: the force keeps an
//     object-shaped spec's inheritCap, so an armed Explore cap still wins.
//
// A 1M window is ONE key. The sub-1M trio has to be actively deleted, not just
// left unwritten: CLAUDE_CODE_DISABLE_1M_CONTEXT makes Claude Code ignore the
// row marker outright, and CLAUDE_CODE_AUTO_COMPACT_WINDOW caps the window
// directly, so either one surviving from an older profile silently puts the
// session back at 200k. An empty value here means delete.
//
// The set must equal what contextWindowEnv computes for rosterWindow, or every
// install rewrites this file; TestEnsureProfile_survives_the_context_budget
// _sweep is what holds the two together.
func routerEnv() map[string]string {
	return map[string]string{
		"WISP_DECK_SUBSCRIPTION_PROVIDER":         claudeconfig.AllInProvider.Key,
		"ANTHROPIC_BASE_URL":                      claudeconfig.AllInProvider.BaseURL,
		"CLAUDE_CODE_DISABLE_EXPLORE_INHERIT_CAP": "1",
		"CLAUDE_CODE_SUBAGENT_MODEL_FORCE":        "1",
		claudeconfig.ContextBudgetKey:             strconv.Itoa(rosterWindow),
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW":         "",
		"CLAUDE_CODE_DISABLE_1M_CONTEXT":          "",
		claudeconfig.OutputReserveKey:             "",
	}
}

// ProfileFile returns the filename the All-In profile is registered under, or
// "" when the machine has none yet. Callers use it to tell creating from
// refreshing: the source gate applies to the first, never the second.
func ProfileFile(listFile string) string {
	for _, config := range claudeconfig.Load(listFile) {
		if strings.EqualFold(config.Name, ProfileName) {
			return config.File
		}
	}
	return ""
}
