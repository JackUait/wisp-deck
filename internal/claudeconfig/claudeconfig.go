// Package claudeconfig is the single source of truth for managing Claude
// settings "configs" — settings JSON files launched via `claude --settings <file>`.
//
// Storage layout (all under the wisp-deck config dir):
//   - <configsDir>/<file>.json     the settings files themselves
//   - <listFile>                   name:file per line (display name decoupled)
//   - <pointerFile>                active filename, or absent/"standard" = plain Claude
//
// Both the inline TUI panel and the `wisp-deck-tui claude-config` CLI call into
// this package, so the list format and mutation rules live in exactly one place.
package claudeconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Config is one selectable Claude settings file (display name + filename).
type Config struct {
	Name string
	File string
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func validateName(name string) error {
	if strings.ContainsAny(name, ":\r\n") {
		return fmt.Errorf("claudeconfig: profile name cannot contain ':', carriage return, or newline")
	}
	return nil
}

// Slugify lowercases name, collapses every run of non-alphanumeric characters
// to a single dash, and trims leading/trailing dashes.
func Slugify(name string) string {
	s := strings.ToLower(name)
	s = nonSlug.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// Load parses a name:file list file into Config entries, skipping blank lines,
// comment lines (leading '#'), and lines without a colon. Returns nil if the
// file cannot be read.
func Load(listFile string) []Config {
	data, err := os.ReadFile(listFile)
	if err != nil {
		return nil
	}
	var out []Config
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		out = append(out, Config{Name: line[:i], File: line[i+1:]})
	}
	return out
}

// GetActive returns the active filename from the pointer file, or "" if the
// file is absent, empty, or names the virtual "standard" entry.
func GetActive(pointerFile string) string {
	data, err := os.ReadFile(pointerFile)
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(data))
	if v == "standard" {
		return ""
	}
	return v
}

// SetActive writes filename to the pointer file. An empty or "standard"
// filename removes the pointer file (selecting plain Claude).
func SetActive(pointerFile, filename string) error {
	if filename == "" || filename == "standard" {
		if err := os.Remove(pointerFile); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(pointerFile), 0755); err != nil {
		return err
	}
	return os.WriteFile(pointerFile, []byte(filename+"\n"), 0644)
}

// ResolvePath returns the absolute path of the active config file, but only if
// that file exists; otherwise it returns "".
func ResolvePath(configsDir, pointerFile string) string {
	active := GetActive(pointerFile)
	if active == "" {
		return ""
	}
	path := filepath.Join(configsDir, active)
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// Add creates a new config: it slugifies name into "<slug>.json" (resolving
// filename collisions with -2, -3, …), writes "{}" into configsDir, appends
// "name:file" to the list file, and returns the chosen filename. A name that
// slugifies to empty falls back to "config".
func Add(listFile, configsDir, name string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	file := nextConfigFilename(configsDir, name)
	if err := os.MkdirAll(configsDir, 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(configsDir, file), []byte("{}\n"), 0644); err != nil {
		return "", err
	}
	if err := appendConfig(listFile, name, file); err != nil {
		return "", err
	}
	return file, nil
}

func nextConfigFilename(configsDir, name string) string {
	slug := Slugify(name)
	if slug == "" {
		slug = "config"
	}
	file := slug + ".json"
	for n := 2; ; n++ {
		if _, err := os.Stat(filepath.Join(configsDir, file)); os.IsNotExist(err) {
			break
		}
		file = fmt.Sprintf("%s-%d.json", slug, n)
	}
	return file
}

func appendConfig(listFile, name, file string) error {
	if err := os.MkdirAll(filepath.Dir(listFile), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(listFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "%s:%s\n", name, file); err != nil {
		return err
	}
	return nil
}

// AddForProvider creates a securely initialized config for a catalog provider.
// The explicit marker keeps provider identity stable if the profile is renamed.
func AddForProvider(listFile, configsDir, name, providerKey string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	provider, ok := providerByKey(providerKey)
	if !ok {
		return "", fmt.Errorf("claudeconfig: unknown provider %q", providerKey)
	}

	file := nextConfigFilename(configsDir, name)
	if err := os.MkdirAll(configsDir, 0755); err != nil {
		return "", err
	}

	env := map[string]string{
		"WISP_DECK_SUBSCRIPTION_PROVIDER": provider.Key,
	}
	if provider.BaseURL != "" {
		env["ANTHROPIC_BASE_URL"] = provider.BaseURL
	}
	for i, key := range envKeys {
		if provider.DefaultModels[i] == "" {
			continue
		}
		env[key] = provider.DefaultModels[i]
	}
	if budget, ok := ContextBudget(env); ok {
		for key, value := range contextWindowEnv(strconv.Itoa(budget), 0) {
			if value != "" {
				env[key] = value
			}
		}
	}
	if provider.UserConfigured {
		env[ByteWatchdogKey] = byteWatchdogDisarmed
	}
	env[StreamWatchdogKey] = streamWatchdogDisarmed
	settings := map[string]any{
		"$schema": "https://json.schemastore.org/claude-code-settings.json",
		"env":     env,
	}
	if provider.Auth == AuthCodexChatGPT {
		// ChatGPT sessions start on the strongest tier (the fable slot), not
		// the sonnet workhorse slot API-key providers would imply.
		settings["model"] = provider.DefaultModels[3]
		settings["disableClaudeAiConnectors"] = true
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(configsDir, file)
	if err := writeSecure(path, append(data, '\n')); err != nil {
		return "", err
	}
	if err := appendConfig(listFile, name, file); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return file, nil
}

// Rename rewrites the display name of the list line whose filename matches file.
// It returns an error if no line matches (including when the list is unreadable).
func Rename(listFile, file, newName string) error {
	if err := validateName(newName); err != nil {
		return err
	}
	configs := Load(listFile)
	found := false
	var b strings.Builder
	for _, c := range configs {
		if c.File == file {
			found = true
			c.Name = newName
		}
		fmt.Fprintf(&b, "%s:%s\n", c.Name, c.File)
	}
	if !found {
		return fmt.Errorf("claudeconfig: no config with file %q", file)
	}
	return os.WriteFile(listFile, []byte(b.String()), 0644)
}

// Delete removes the config file and its list line. If the deleted config was
// the active one, the pointer is reset to standard (plain Claude).
func Delete(listFile, configsDir, pointerFile, file string) error {
	if err := os.Remove(filepath.Join(configsDir, file)); err != nil && !os.IsNotExist(err) {
		return err
	}
	configs := Load(listFile)
	var b strings.Builder
	for _, c := range configs {
		if c.File == file {
			continue
		}
		fmt.Fprintf(&b, "%s:%s\n", c.Name, c.File)
	}
	if err := os.WriteFile(listFile, []byte(b.String()), 0644); err != nil {
		return err
	}
	if GetActive(pointerFile) == file {
		return SetActive(pointerFile, "")
	}
	return nil
}

// writeSecure atomically writes data to path with 0600 permissions. It writes
// to a temp file in the same directory (created 0600) then renames over the
// target, so the credential-bearing file is never world-readable, even briefly.
// Plain os.WriteFile would leave an existing file's looser mode untouched.
func writeSecure(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// ReadAPIKey reads ANTHROPIC_AUTH_TOKEN from a config JSON's env section.
// Returns "" if the file is missing, invalid JSON, or has no key.
func ReadAPIKey(configsDir, file string) string {
	return readEnvValue(configsDir, file, "ANTHROPIC_AUTH_TOKEN")
}

func readEnvValue(configsDir, file, key string) string {
	data, err := os.ReadFile(filepath.Join(configsDir, file))
	if err != nil {
		return ""
	}
	var s struct {
		Env map[string]string `json:"env"`
	}
	if json.Unmarshal(data, &s) != nil || s.Env == nil {
		return ""
	}
	return s.Env[key]
}

// ReadProviderMarker returns Wisp Deck's explicit subscription-provider marker
// from a settings file. Missing files, invalid JSON, and absent markers return
// an empty string.
func ReadProviderMarker(configsDir, file string) string {
	return readEnvValue(configsDir, file, "WISP_DECK_SUBSCRIPTION_PROVIDER")
}

// ReadBaseURL returns the configured Anthropic-compatible endpoint from a
// settings file, or an empty string if it is absent or unreadable.
func ReadBaseURL(configsDir, file string) string {
	return readEnvValue(configsDir, file, "ANTHROPIC_BASE_URL")
}

// WriteProviderMarker persists an explicit catalog provider identity in an
// existing settings file while preserving all other settings.
func WriteProviderMarker(configsDir, file, providerKey string) error {
	if _, ok := providerByKey(providerKey); !ok {
		return fmt.Errorf("claudeconfig: unknown provider %q", providerKey)
	}
	path := filepath.Join(configsDir, file)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return err
	}
	env, _ := settings["env"].(map[string]any)
	if env == nil {
		env = make(map[string]any)
	}
	env["WISP_DECK_SUBSCRIPTION_PROVIDER"] = providerKey
	stampByteWatchdog(env)
	stampStreamWatchdog(env)
	settings["env"] = env
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return writeSecure(path, append(out, '\n'))
}

// ProviderForConfig resolves explicit provider metadata first and retains the
// display-name heuristic for legacy or invalid settings files.
func ProviderForConfig(configsDir string, config Config) Provider {
	if provider, ok := providerByKey(ReadProviderMarker(configsDir, config.File)); ok {
		return provider
	}
	return providerFor(config.Name)
}

// ConfigReady reports whether a config has enough local authentication
// metadata to be selectable. ChatGPT authentication is verified at launch by
// Codex, while API providers require a stored key.
func ConfigReady(configsDir string, config Config) bool {
	provider := ProviderForConfig(configsDir, config)
	switch provider.Auth {
	case AuthCodexChatGPT:
		return true
	case AuthAPIKey:
		if strings.TrimSpace(ReadAPIKey(configsDir, config.File)) == "" {
			return false
		}
		if !provider.SuppliesOwnModel() {
			return true
		}
		endpoint := strings.TrimSpace(ReadBaseURL(configsDir, config.File))
		model := strings.TrimSpace(ReadCustomModel(configsDir, config.File))
		window := strings.TrimSpace(ReadContextWindow(configsDir, config.File))
		if endpoint == "" || model == "" || window == "" {
			return false
		}
		for _, key := range envKeys {
			if strings.TrimSpace(readEnvValue(configsDir, config.File, key)) != model {
				return false
			}
		}
		return ValidateCustomEndpoint(endpoint) == nil &&
			ValidateCustomModel(model) == nil &&
			ValidateCustomContextWindow(window) == nil
	default:
		return false
	}
}

// WriteAPIKey sets ANTHROPIC_AUTH_TOKEN in a config JSON's env section,
// preserving all other fields. Creates the env section if absent.
func WriteAPIKey(configsDir, file, key string) error {
	path := filepath.Join(configsDir, file)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	env, _ := m["env"].(map[string]any)
	if env == nil {
		env = make(map[string]any)
	}
	env["ANTHROPIC_AUTH_TOKEN"] = key
	m["env"] = env
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeSecure(path, append(out, '\n'))
}

// ReadContextWindow reads the declared context window from a config JSON.
func ReadContextWindow(configsDir, file string) string {
	return readEnvValue(configsDir, file, ContextBudgetKey)
}

// ReadCustomModel reports the model id a user-configured profile runs. All four
// aliases name the same model, so the first one set answers for the profile.
func ReadCustomModel(configsDir, file string) string {
	for _, key := range envKeys {
		if value := readEnvValue(configsDir, file, key); value != "" {
			return value
		}
	}
	return ""
}

// WriteCustomEndpoint sets the base URL of a user-configured profile. An empty
// value removes the key: a blank ANTHROPIC_BASE_URL is not "unset" to Claude
// Code, it is an endpoint that cannot resolve.
func WriteCustomEndpoint(configsDir, file, endpoint string) error {
	if err := ValidateCustomEndpoint(endpoint); err != nil {
		return err
	}
	return writeEnvValues(configsDir, file,
		map[string]string{"ANTHROPIC_BASE_URL": normalizeCustomEndpoint(endpoint)})
}

// ValidateCustomEndpoint reports why an endpoint cannot be stored, if it cannot.
func ValidateCustomEndpoint(endpoint string) error {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil
	}
	if strings.ContainsAny(endpoint, " \t\r\n") {
		return fmt.Errorf("endpoint cannot contain whitespace")
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		return fmt.Errorf("endpoint must start with http:// or https://")
	}
	return nil
}

func normalizeCustomEndpoint(endpoint string) string {
	return strings.TrimRight(strings.TrimSpace(endpoint), "/")
}

// WriteCustomModel points every alias at one model id. One endpoint serves one
// model, and /model and subagents move freely across all four aliases, so a
// partially mapped profile launches some tiers with no model at all.
func WriteCustomModel(configsDir, file, model string) error {
	if err := ValidateCustomModel(model); err != nil {
		return err
	}
	model = strings.TrimSpace(model)
	values := make(map[string]string, len(envKeys))
	for _, key := range envKeys {
		values[key] = model
	}
	return writeEnvValues(configsDir, file, values)
}

// WriteCustomContextWindow declares the window the endpoint actually enforces.
// The catalog cannot size a model it has never heard of, so for these profiles
// this is the only source of the figure — and overshooting it is unrecoverable,
// which is why a value that is not a positive integer is refused rather than
// coerced.
func WriteCustomContextWindow(configsDir, file, window string) error {
	if err := ValidateCustomContextWindow(window); err != nil {
		return err
	}
	// The reserve is derived from the window being written rather than carried
	// over: the user is changing how much the endpoint can hold, and the room
	// for a reply is a share of that. A later sweep keeps whatever lands here.
	return writeEnvValues(configsDir, file, contextWindowEnv(strings.TrimSpace(window), 0))
}

// ValidateCustomModel reports why a model id cannot be stored, if it cannot.
func ValidateCustomModel(model string) error {
	if strings.ContainsAny(strings.TrimSpace(model), " \t\r\n") {
		return fmt.Errorf("model id cannot contain whitespace")
	}
	return nil
}

// ValidateCustomContextWindow reports why a window cannot be stored, if it cannot.
func ValidateCustomContextWindow(window string) error {
	window = strings.TrimSpace(window)
	if window == "" {
		return nil
	}
	tokens, err := strconv.Atoi(window)
	if err != nil || tokens <= 0 {
		return fmt.Errorf("context window must be a positive number of tokens")
	}
	return nil
}

// writeEnvValues sets each key in a config JSON's env section, preserving every
// other field. An empty value deletes its key.
func writeEnvValues(configsDir, file string, values map[string]string) error {
	path := filepath.Join(configsDir, file)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return err
	}
	env, _ := settings["env"].(map[string]any)
	if env == nil {
		env = make(map[string]any)
	}
	for key, value := range values {
		if value == "" {
			delete(env, key)
			continue
		}
		env[key] = value
	}
	settings["env"] = env
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return writeSecure(path, append(out, '\n'))
}

// codingKeyPrefix marks a Kimi For Coding subscription credential. Moonshot's
// open-platform keys carry no such prefix, and no other vendor in the catalog
// issues one, so the prefix identifies the gateway a key belongs to.
const codingKeyPrefix = "sk-kimi-"

// RepairGatewayForKey re-points a profile at the gateway its stored credential
// actually authenticates against, and reports whether it changed anything.
//
// Moonshot sells the metered open platform and the flat-rate Kimi For Coding
// subscription behind separate hosts with separate model namespaces, and each
// rejects the other's credential with a bare "401 Invalid Authentication". A
// subscription key saved into an open-platform profile therefore produces an
// endless retry loop in the agent pane with nothing pointing at the endpoint as
// the cause, so saving the key moves the profile — marker, base URL, and model
// routing — to the gateway that accepts it.
//
// Only the sk-kimi- prefix is treated as evidence, and only within the Moonshot
// family: an unrecognized key is left exactly where the user put it rather than
// breaking a working profile to fix one that was never broken.
func RepairGatewayForKey(configsDir, file string) (bool, error) {
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
	key, _ := env["ANTHROPIC_AUTH_TOKEN"].(string)
	marker, _ := env["WISP_DECK_SUBSCRIPTION_PROVIDER"].(string)
	if marker != "moonshot" || !strings.HasPrefix(key, codingKeyPrefix) {
		return false, nil
	}
	provider, ok := providerByKey("moonshot-coding")
	if !ok {
		return false, nil
	}

	env["WISP_DECK_SUBSCRIPTION_PROVIDER"] = provider.Key
	env["ANTHROPIC_BASE_URL"] = provider.BaseURL
	for i, envKey := range envKeys {
		env[envKey] = provider.DefaultModels[i]
	}
	stampContextBudget(env, file)
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

// AnthropicAliases are the model alias slots that can be mapped.
var AnthropicAliases = []string{"opus", "sonnet", "haiku", "fable"}

// ProviderModels maps each provider key to its model id list. Derived from the
// Providers catalog so the ids stay identical everywhere they are referenced.
var ProviderModels = func() map[string][]string {
	m := make(map[string][]string, len(Providers))
	for _, p := range Providers {
		ids := make([]string, len(p.Models))
		for i, mod := range p.Models {
			ids[i] = mod.ID
		}
		m[p.Key] = ids
	}
	return m
}()

// ProviderBaseURL returns the Anthropic-compatible gateway base URL for the
// provider selected by the config name (defaulting to the first provider, so the
// result always matches the provider ModelsForConfig picks).
func ProviderBaseURL(configName string) string {
	return providerFor(configName).BaseURL
}

// ModelsForConfig returns the model ids for the provider selected by the config
// name (defaulting to the first provider's models).
func ModelsForConfig(configName string) []string {
	return ProviderModels[providerFor(configName).Key]
}

// envKeys maps AnthropicAliases indices to their env var names.
var envKeys = []string{
	"ANTHROPIC_DEFAULT_OPUS_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL",
	"ANTHROPIC_DEFAULT_FABLE_MODEL",
}

// ReadModelMappings reads the four ANTHROPIC_DEFAULT_*_MODEL values from a
// config JSON and returns model list indices for each alias. Unmapped aliases
// return -1.
func ReadModelMappings(configsDir, file string, models []string) [4]int {
	var result [4]int
	for i := range result {
		result[i] = -1
	}
	data, err := os.ReadFile(filepath.Join(configsDir, file))
	if err != nil {
		return result
	}
	var s struct {
		Env map[string]string `json:"env"`
	}
	if json.Unmarshal(data, &s) != nil || s.Env == nil {
		return result
	}
	for i, key := range envKeys {
		if val, ok := s.Env[key]; ok {
			for j, model := range models {
				if val == model {
					result[i] = j
					break
				}
			}
		}
	}
	return result
}

// WriteModelMappings writes the four ANTHROPIC_DEFAULT_*_MODEL values into a
// config JSON. Indices of -1 clear the corresponding key.
func WriteModelMappings(configsDir, file string, mappings [4]int, models []string) error {
	path := filepath.Join(configsDir, file)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	env, _ := m["env"].(map[string]any)
	if env == nil {
		env = make(map[string]any)
	}
	for i, key := range envKeys {
		if mappings[i] >= 0 && mappings[i] < len(models) {
			env[key] = models[mappings[i]]
		} else {
			delete(env, key)
		}
	}
	stampContextBudget(env, file)
	m["env"] = env
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeSecure(path, append(out, '\n'))
}
