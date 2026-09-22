package allin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackuait/wisp-deck/internal/claudeconfig"
	"github.com/jackuait/wisp-deck/internal/rolefix"
)

func readPicker(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	picker, _ := settings["modelPicker"].(map[string]any)
	if picker == nil {
		t.Fatalf("no modelPicker in %s", data)
	}
	return picker
}

func TestEnsureProfile_creates_the_profile_and_registers_it(t *testing.T) {
	env := rosterEnv(t)
	listFile := filepath.Join(t.TempDir(), "claude-configs.list")
	file, err := EnsureProfile(env, listFile, env.ConfigsDir)
	if err != nil {
		t.Fatal(err)
	}
	picker := readPicker(t, filepath.Join(env.ConfigsDir, file))
	options, _ := picker["options"].([]any)
	if len(options) == 0 {
		t.Fatal("no rows written")
	}
	if picker["replaceBuiltInOptions"] != true {
		t.Fatalf("built-ins not replaced: %v", picker)
	}
	registered := false
	for _, config := range readLines(listFile) {
		if config == ProfileName+":"+file {
			registered = true
		}
	}
	if !registered {
		t.Fatalf("not registered in %s", listFile)
	}
}

func TestEnsureProfile_refreshes_rows_without_a_second_registration(t *testing.T) {
	env := rosterEnv(t)
	listFile := filepath.Join(t.TempDir(), "claude-configs.list")
	first, err := EnsureProfile(env, listFile, env.ConfigsDir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EnsureProfile(env, listFile, env.ConfigsDir)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("%q != %q", first, second)
	}
	if lines := readLines(listFile); len(lines) != 1 {
		t.Fatalf("registered %d times: %v", len(lines), lines)
	}
}

func TestEnsureProfile_keeps_every_other_key_in_the_profile(t *testing.T) {
	env := rosterEnv(t)
	listFile := filepath.Join(t.TempDir(), "claude-configs.list")
	file, err := EnsureProfile(env, listFile, env.ConfigsDir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(env.ConfigsDir, file)
	data, _ := os.ReadFile(path)
	var settings map[string]any
	_ = json.Unmarshal(data, &settings)
	settings["statusLine"] = "keep me"
	patched, _ := json.Marshal(settings)
	_ = os.WriteFile(path, patched, 0o600)

	if _, err := EnsureProfile(env, listFile, env.ConfigsDir); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	_ = json.Unmarshal(data, &settings)
	if settings["statusLine"] != "keep me" {
		t.Fatalf("unrelated key lost: %s", data)
	}
}

func TestEnsureProfile_a_corrupted_existing_profile_fails_loudly_and_is_left_untouched(t *testing.T) {
	env := rosterEnv(t)
	listFile := filepath.Join(t.TempDir(), "claude-configs.list")
	file, err := EnsureProfile(env, listFile, env.ConfigsDir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(env.ConfigsDir, file)
	corrupted := []byte("{ this is not valid json")
	if err := os.WriteFile(path, corrupted, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := EnsureProfile(env, listFile, env.ConfigsDir); err == nil {
		t.Fatal("expected an error reading a corrupted profile, got nil")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(corrupted) {
		t.Fatalf("corrupted profile was overwritten: got %q, want %q", data, corrupted)
	}
}

func TestEnsureProfile_recomputes_the_roster_on_every_call(t *testing.T) {
	env := rosterEnv(t)
	listFile := filepath.Join(t.TempDir(), "claude-configs.list")
	file, err := EnsureProfile(env, listFile, env.ConfigsDir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(env.ConfigsDir, file)
	before := readPicker(t, path)
	beforeOptions, _ := before["options"].([]any)

	// A second account appears after the first EnsureProfile call, the same
	// way a real login can be added while the profile already exists.
	existing, err := os.ReadFile(env.AccountsList)
	if err != nil {
		t.Fatal(err)
	}
	updated := string(existing) + "Second:second\n"
	if err := os.WriteFile(env.AccountsList, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := EnsureProfile(env, listFile, env.ConfigsDir); err != nil {
		t.Fatal(err)
	}
	after := readPicker(t, path)
	afterOptions, _ := after["options"].([]any)
	if len(afterOptions) <= len(beforeOptions) {
		t.Fatalf("roster did not grow: before=%d after=%d", len(beforeOptions), len(afterOptions))
	}

	found := false
	for _, row := range afterOptions {
		fields, _ := row.(map[string]any)
		if model, _ := fields["model"].(string); strings.Contains(model, "acct.second/") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no row for the newly registered account in %v", afterOptions)
	}
}

func readEnv(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	return settings.Env
}

func generatedProfile(t *testing.T) (Env, string, string) {
	t.Helper()
	env := rosterEnv(t)
	listFile := filepath.Join(t.TempDir(), "claude-configs.list")
	file, err := EnsureProfile(env, listFile, env.ConfigsDir)
	if err != nil {
		t.Fatal(err)
	}
	return env, file, filepath.Join(env.ConfigsDir, file)
}

// Every place All-In can be chosen — the switcher's rows and the main page's
// subscription ring — hides a config ConfigReady refuses, so a profile that
// fails this is unreachable from the UI no matter how good its picker is.
func TestEnsureProfile_the_generated_profile_is_selectable(t *testing.T) {
	env, file, _ := generatedProfile(t)
	if !claudeconfig.ConfigReady(env.ConfigsDir, claudeconfig.Config{Name: ProfileName, File: file}) {
		t.Fatal("the generated profile is not ConfigReady, so nothing in the UI offers it")
	}
}

// The launch wrapper reads the profile's own endpoint and rewrites it to the
// loopback router. No endpoint means runLoopbackWrappedLaunch falls through and
// every wisp/… row goes verbatim to the session's own upstream.
func TestEnsureProfile_declares_the_endpoint_the_launch_wrapper_rewrites(t *testing.T) {
	_, _, path := generatedProfile(t)
	upstream, err := rolefix.UpstreamFromSettings(path)
	if err != nil {
		t.Fatalf("the router would never start: %v", err)
	}
	if upstream == "" {
		t.Fatal("empty upstream")
	}
}

// A profile whose name matches no provider alias resolves to Providers[0], so
// without an explicit marker All-In labels and colours as Zhipu / GLM.
func TestEnsureProfile_carries_its_own_provider_identity(t *testing.T) {
	env, file, _ := generatedProfile(t)
	provider := claudeconfig.ProviderForConfig(env.ConfigsDir,
		claudeconfig.Config{Name: ProfileName, File: file})
	if provider.Key != claudeconfig.AllInProvider.Key {
		t.Fatalf("provider %q (%s), want the All-In identity", provider.Key, provider.Name)
	}
}

// CLAUDE_CODE_DISABLE_1M_CONTEXT gates the string-marker branch of Claude
// Code's window choice, and that branch is the ONLY thing giving a Claude row
// its 1M window: the decoded `tc(model)` returns false outright when the key is
// set, whatever the id ends with. One inherited "1" from an older profile puts
// every row back at 200k, silently.
func TestEnsureProfile_leaves_the_1m_model_marker_armed(t *testing.T) {
	_, _, path := generatedProfile(t)
	if got, ok := readEnv(t, path)["CLAUDE_CODE_DISABLE_1M_CONTEXT"]; ok {
		t.Fatalf("CLAUDE_CODE_DISABLE_1M_CONTEXT = %q, which neutralises every row's marker", got)
	}
}

// The built-in Explore agent is the one agent type that does not inherit the
// session model: decoded from 2.1.267, `LX()` hands it {inheritCap:"opus"}
// whenever the session model's id names no Claude family (haiku/sonnet/opus),
// and "opus" then resolves to the first-party `claude-opus-5`. The router
// places that id as an unrouted row, so an Explore subagent spends the
// session's OWN login — api.anthropic.com — instead of the row the user
// picked. Measured on a live All-In pane: three Explore subagents ran on
// claude-opus-5 and died on the Claude subscription's weekly limit while that
// same session's general-purpose subagents ran on the picked row. The cap is a
// plain env gate, and nothing else turns it off.
func TestEnsureProfile_disarms_the_explore_inherit_cap(t *testing.T) {
	_, _, path := generatedProfile(t)
	if got := readEnv(t, path)["CLAUDE_CODE_DISABLE_EXPLORE_INHERIT_CAP"]; got != "1" {
		t.Fatalf("CLAUDE_CODE_DISABLE_EXPLORE_INHERIT_CAP = %q, want \"1\": every Explore subagent then runs on claude-opus-5 over the session's own login", got)
	}
}

// Disarming the Explore cap only covers an agent that inherits. A model named
// on the Agent call itself — superpowers' own skills tell the model to always
// name one, `model: "sonnet"` for an implementer and `"haiku"` for a probe —
// outranks inheritance, and agent frontmatter outranks it too. Resolved
// without the four ANTHROPIC_DEFAULT_*_MODEL aliases this profile deliberately
// does not carry, `sonnet` becomes the first-party claude-sonnet-5, `haiku`
// claude-haiku-4-5-20251001, and Route reads either as an unrouted row: the
// subagent bills the session's OWN Claude subscription instead of the row the
// user picked.
//
// CLAUDE_CODE_SUBAGENT_MODEL_FORCE drops both of those sources, so every
// subagent inherits the session model and carries its wisp/… id through the
// router. Measured end to end on a real 2.1.268 pane driven by a local
// endpoint, one Agent(model:"sonnet") call per run: without it the subagent's
// first request carried claude-sonnet-5, with it wisp/cfg.deepseek/deepseek-flash.
//
// It has to ship beside CLAUDE_CODE_DISABLE_EXPLORE_INHERIT_CAP, not instead
// of it: the force keeps an object-shaped spec's inheritCap, so an armed
// Explore cap (an object) would still resolve to opus.
func TestEnsureProfile_forces_subagents_onto_the_picked_row(t *testing.T) {
	_, _, path := generatedProfile(t)
	if got := readEnv(t, path)["CLAUDE_CODE_SUBAGENT_MODEL_FORCE"]; got != "1" {
		t.Fatalf("CLAUDE_CODE_SUBAGENT_MODEL_FORCE = %q, want \"1\": a subagent then runs on claude-sonnet-5 / claude-haiku-4-5 over the session's own login", got)
	}
}

// The declared window must equal what contextWindowEnv would compute for
// rosterWindow, or every install rewrites this file. At 1M that means ONE key:
// the sub-1M trio is deleted, and CLAUDE_CODE_AUTO_COMPACT_WINDOW surviving at
// any value would cap the session straight back down.
func TestEnsureProfile_survives_the_context_budget_sweep(t *testing.T) {
	env, file, path := generatedProfile(t)
	changed, err := claudeconfig.EnsureContextBudget(env.ConfigsDir, file)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("the context-budget sweep rewrote the All-In profile")
	}
	env2 := readEnv(t, path)
	if env2["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] != "1000000" {
		t.Errorf("declared window = %q, want 1000000", env2["CLAUDE_CODE_MAX_CONTEXT_TOKENS"])
	}
	for _, key := range []string{
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW",
		"CLAUDE_CODE_DISABLE_1M_CONTEXT",
		"CLAUDE_CODE_MAX_OUTPUT_TOKENS",
	} {
		if value, ok := env2[key]; ok {
			t.Errorf("%s survived at %q and caps the session below 1M", key, value)
		}
	}
}

// Every profile written before the 1M rows carries the 200k quartet. Merging
// routerEnv over it is not enough: DISABLE_1M_CONTEXT alone makes Claude Code
// ignore the marker, and AUTO_COMPACT_WINDOW alone caps the window. A refresh
// has to DELETE the keys routerEnv no longer declares.
func TestEnsureProfile_clears_a_sub_1m_windows_leftover_keys(t *testing.T) {
	env := rosterEnv(t)
	listFile := filepath.Join(t.TempDir(), "claude-configs.list")
	file, err := EnsureProfile(env, listFile, env.ConfigsDir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(env.ConfigsDir, file)
	if err := os.WriteFile(path, []byte(`{"env":{
"CLAUDE_CODE_MAX_CONTEXT_TOKENS":"200000",
"CLAUDE_CODE_AUTO_COMPACT_WINDOW":"200000",
"CLAUDE_CODE_DISABLE_1M_CONTEXT":"1",
"CLAUDE_CODE_MAX_OUTPUT_TOKENS":"32000",
"ANTHROPIC_AUTH_TOKEN":"the user's own key"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureProfile(env, listFile, env.ConfigsDir); err != nil {
		t.Fatal(err)
	}
	got := readEnv(t, path)
	for _, key := range []string{
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW",
		"CLAUDE_CODE_DISABLE_1M_CONTEXT",
		"CLAUDE_CODE_MAX_OUTPUT_TOKENS",
	} {
		if value, ok := got[key]; ok {
			t.Errorf("%s left at %q, which caps the session below 1M", key, value)
		}
	}
	if got["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] != "1000000" {
		t.Errorf("declared window = %q, want 1000000", got["CLAUDE_CODE_MAX_CONTEXT_TOKENS"])
	}
	if got["ANTHROPIC_AUTH_TOKEN"] != "the user's own key" {
		t.Error("the refresh dropped a key that is the user's own")
	}
}

// Every All-In router can now rewrite the profile at once. A shared fixed temp
// name lets one writer truncate the file another is about to rename into
// place; standing a directory on that name proves no fixed name is used.
func TestEnsureProfile_writes_through_its_own_temp_file(t *testing.T) {
	env := rosterEnv(t)
	listFile := filepath.Join(t.TempDir(), "claude-configs.list")
	file, err := EnsureProfile(env, listFile, env.ConfigsDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(env.ConfigsDir, file)+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureProfile(env, listFile, env.ConfigsDir); err != nil {
		t.Fatalf("a second writer's temp name blocked this one: %v", err)
	}
}
