// Package claudeaccount is the single source of truth for managing native
// Claude login "accounts" — separate subscriptions (work, personal, …) each
// isolated by its own CLAUDE_CONFIG_DIR, so they stay logged in simultaneously
// and are switched between by relaunching `claude` under a different dir.
//
// Storage layout (all under the ghost-tab config dir):
//   - <accountsDir>/<dir>/          the per-account CLAUDE_CONFIG_DIR (its login)
//   - <listFile>                    label:dir per line (display label decoupled)
//   - <pointerFile>                 active dir name, or absent/"default" = the
//     standard ~/.claude (Keychain) login
//
// Both the inline ACCOUNT switcher in the menu and the `ghost-tab-tui
// claude-account` CLI call into this package, so the list format and mutation
// rules live in exactly one place.
package claudeaccount

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Account is one selectable native login (display label + config dir name).
type Account struct {
	Label string
	Dir   string
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify lowercases label, collapses every run of non-alphanumeric characters
// to a single dash, and trims leading/trailing dashes.
func Slugify(label string) string {
	s := strings.ToLower(label)
	s = nonSlug.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// Load parses a label:dir list file into Account entries, skipping blank lines,
// comment lines (leading '#'), and lines without a colon. Returns nil if the
// file cannot be read.
func Load(listFile string) []Account {
	data, err := os.ReadFile(listFile)
	if err != nil {
		return nil
	}
	var out []Account
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		out = append(out, Account{Label: line[:i], Dir: line[i+1:]})
	}
	return out
}

// GetActive returns the active account dir name from the pointer file, or "" if
// the file is absent, empty, or names the virtual "default" account.
func GetActive(pointerFile string) string {
	data, err := os.ReadFile(pointerFile)
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(data))
	if v == "default" {
		return ""
	}
	return v
}

// SetActive writes dir to the pointer file. An empty or "default" dir removes
// the pointer file (selecting the standard Keychain login).
func SetActive(pointerFile, dir string) error {
	if dir == "" || dir == "default" {
		if err := os.Remove(pointerFile); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(pointerFile), 0755); err != nil {
		return err
	}
	return os.WriteFile(pointerFile, []byte(dir+"\n"), 0644)
}

// ResolveDir returns the absolute path of the active account's CLAUDE_CONFIG_DIR,
// but only if that directory exists; otherwise it returns "" (Default/Keychain).
func ResolveDir(accountsDir, pointerFile string) string {
	active := GetActive(pointerFile)
	if active == "" {
		return ""
	}
	path := filepath.Join(accountsDir, active)
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return ""
	}
	return path
}

// Add registers a new account: it slugifies label into a dir name (resolving
// collisions with -2, -3, …), creates <accountsDir>/<dir>/, appends "label:dir"
// to the list file, and returns the chosen dir name. A label that slugifies to
// empty falls back to "account". The created dir is an empty CLAUDE_CONFIG_DIR;
// the caller runs `claude auth login` under it to populate the login.
func Add(listFile, accountsDir, label string) (string, error) {
	slug := Slugify(label)
	if slug == "" {
		slug = "account"
	}
	dir := slug
	for n := 2; ; n++ {
		if _, err := os.Stat(filepath.Join(accountsDir, dir)); os.IsNotExist(err) {
			break
		}
		dir = fmt.Sprintf("%s-%d", slug, n)
	}
	if err := os.MkdirAll(filepath.Join(accountsDir, dir), 0700); err != nil {
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
	if _, err := fmt.Fprintf(f, "%s:%s\n", label, dir); err != nil {
		return "", err
	}
	return dir, nil
}

// Rename changes the display label of the account whose dir matches, leaving the
// config directory (and its login) untouched. It is a no-op if no line matches.
// The dir name is the stable identifier; only the label changes.
func Rename(listFile, dir, newLabel string) error {
	data, err := os.ReadFile(listFile)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if j := strings.Index(trimmed, ":"); j >= 0 && trimmed[j+1:] == dir {
			lines[i] = newLabel + ":" + dir
		}
	}
	return os.WriteFile(listFile, []byte(strings.Join(lines, "\n")), 0644)
}

// Remove deletes an account: it drops the matching "label:dir" line from the
// list file and removes the account's config directory. If the removed account
// was active, it clears the pointer (reverting to Default).
func Remove(listFile, accountsDir, pointerFile, dir string) error {
	data, err := os.ReadFile(listFile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if i := strings.Index(trimmed, ":"); i >= 0 && trimmed[i+1:] == dir {
			continue
		}
		kept = append(kept, line)
	}
	out := strings.Join(kept, "\n")
	if out != "" {
		out += "\n"
	}
	if err := os.WriteFile(listFile, []byte(out), 0644); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(accountsDir, dir)); err != nil {
		return err
	}
	if GetActive(pointerFile) == dir {
		return SetActive(pointerFile, "default")
	}
	return nil
}
