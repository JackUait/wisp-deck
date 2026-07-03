// Package claudeaccount is the single source of truth for managing native
// Claude login "accounts" — separate subscriptions (work, personal, …) each
// isolated by its own CLAUDE_CONFIG_DIR, so they stay logged in simultaneously
// and are switched between by relaunching `claude` under a different dir.
//
// Storage layout (all under the wisp-deck config dir):
//   - <accountsDir>/<dir>/          the per-account CLAUDE_CONFIG_DIR (its login)
//   - <listFile>                    label:dir per line (display label decoupled)
//   - <pointerFile>                 active dir name, or absent/"default" = the
//     standard ~/.claude (Keychain) login
//
// Both the inline ACCOUNT switcher in the menu and the `wisp-deck-tui
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

// GetDefaultLabel returns the custom display label for the implicit Default
// login (the standard ~/.claude Keychain login), or "Default" if none is set.
func GetDefaultLabel(file string) string {
	data, err := os.ReadFile(file)
	if err != nil {
		return "Default"
	}
	v := strings.TrimSpace(string(data))
	if v == "" {
		return "Default"
	}
	return v
}

// SetDefaultLabel writes a custom display label for the Default login. An empty
// label, or the literal fallback "Default", removes the file (there is no point
// storing the default name).
func SetDefaultLabel(file, label string) error {
	label = strings.TrimSpace(label)
	if label == "" || label == "Default" {
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		return err
	}
	return os.WriteFile(file, []byte(label+"\n"), 0644)
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

// sharedStateItems mirrors WISP_DECK_CLAUDE_SHARED_STATE_ITEMS in
// lib/claude-shared-settings.sh: conversation state that belongs to the user's
// single shared store (~/.claude), not to any one login.
var sharedStateItems = []string{
	"projects", "history.jsonl", "todos", "session-env", "file-history", "plans",
}

// rescueState moves any REAL (non-symlink) conversation state left in an
// account dir into the shared store before the dir is deleted. Normally the
// launch-time sync has already symlinked these items, but an account that was
// never launched post-sharing (or whose link was severed) still holds real
// transcripts — os.RemoveAll would destroy the only copy. Files missing from
// the store are moved in; identical duplicates are dropped; a differing
// same-path copy is preserved as <name>.conflict so nothing is ever lost.
func rescueState(accountDir, claudeDir string) error {
	for _, item := range sharedStateItems {
		src := filepath.Join(accountDir, item)
		info, err := os.Lstat(src)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			continue // absent, or already an alias of the shared store
		}
		err = filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
			if err != nil || !fi.Mode().IsRegular() {
				return err
			}
			rel, err := filepath.Rel(src, p)
			if err != nil {
				return err
			}
			target := filepath.Join(claudeDir, item, rel)
			if info.Mode().IsRegular() {
				target = filepath.Join(claudeDir, item)
			}
			if _, err := os.Lstat(target); os.IsNotExist(err) {
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					return err
				}
				return os.Rename(p, target)
			}
			a, errA := os.ReadFile(target)
			b, errB := os.ReadFile(p)
			if errA == nil && errB == nil && string(a) == string(b) {
				return nil // identical duplicate — safe to drop with the dir
			}
			return os.Rename(p, target+".conflict")
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// Remove deletes an account: it drops the matching "label:dir" line from the
// list file, rescues any real conversation state into the shared ~/.claude
// store, and removes the account's config directory. If the rescue fails the
// dir is renamed aside instead of deleted — transcripts must never be
// destroyed. If the removed account was active, it clears the pointer
// (reverting to Default).
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
	accountDir := filepath.Join(accountsDir, dir)
	rescueErr := fmt.Errorf("no home dir")
	if home, err := os.UserHomeDir(); err == nil {
		rescueErr = rescueState(accountDir, filepath.Join(home, ".claude"))
	}
	if rescueErr != nil {
		// Never delete state we could not rescue: park the dir instead.
		if err := os.Rename(accountDir, accountDir+".removed"); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else if err := os.RemoveAll(accountDir); err != nil {
		return err
	}
	if GetActive(pointerFile) == dir {
		return SetActive(pointerFile, "default")
	}
	return nil
}
