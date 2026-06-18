// Package claudeconfig is the single source of truth for managing Claude
// settings "configs" — settings JSON files launched via `claude --settings <file>`.
//
// Storage layout (all under the ghost-tab config dir):
//   - <configsDir>/<file>.json     the settings files themselves
//   - <listFile>                   name:file per line (display name decoupled)
//   - <pointerFile>                active filename, or absent/"standard" = plain Claude
//
// Both the inline TUI panel and the `ghost-tab-tui claude-config` CLI call into
// this package, so the list format and mutation rules live in exactly one place.
package claudeconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Config is one selectable Claude settings file (display name + filename).
type Config struct {
	Name string
	File string
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

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
	if err := os.MkdirAll(configsDir, 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(configsDir, file), []byte("{}\n"), 0644); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(listFile), 0755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(listFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "%s:%s\n", name, file); err != nil {
		return "", err
	}
	return file, nil
}

// Rename rewrites the display name of the list line whose filename matches file.
// It returns an error if no line matches (including when the list is unreadable).
func Rename(listFile, file, newName string) error {
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

// ReadAPIKey reads ANTHROPIC_AUTH_TOKEN from a config JSON's env section.
// Returns "" if the file is missing, invalid JSON, or has no key.
func ReadAPIKey(configsDir, file string) string {
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
	return s.Env["ANTHROPIC_AUTH_TOKEN"]
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
	return os.WriteFile(path, append(out, '\n'), 0644)
}

// AnthropicAliases are the model alias slots that can be mapped.
var AnthropicAliases = []string{"opus", "sonnet", "haiku", "fable"}

// ProviderModels maps provider names to their available model lists.
var ProviderModels = map[string][]string{
	"zhipu":  {"glm-5.2", "glm-5.1", "glm-5", "glm-4.7", "glm-4.6", "glm-4.5-air"},
	"mimo":   {"mimo-v2.5-pro", "mimo-v2.5", "mimo-v2-pro", "mimo-v2-omni", "mimo-v2-flash"},
}

// ModelsForConfig returns the model list for the provider matching the config
// name. Falls back to GLM models if no provider matches.
func ModelsForConfig(configName string) []string {
	lower := strings.ToLower(configName)
	for key, models := range ProviderModels {
		if strings.Contains(lower, key) {
			return models
		}
	}
	// Default fallback
	return ProviderModels["zhipu"]
}

// AllModels returns a deduplicated list of all provider models.
func AllModels() []string {
	seen := make(map[string]bool)
	var out []string
	for _, models := range ProviderModels {
		for _, m := range models {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out
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
	m["env"] = env
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0644)
}
