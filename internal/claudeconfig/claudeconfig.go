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
