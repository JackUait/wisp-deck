package opencodeconfig

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jackuait/ghost-tab/internal/claudeconfig"
)

// Inputs are the ghost-tab config paths plus the home dir used to locate the
// OpenCode global config.
type Inputs struct {
	ListFile    string
	ConfigsDir  string
	PointerFile string
	Home        string
}

// configFilenames is OpenCode's global-config resolution order; first existing
// wins, else we create opencode.json.
var configFilenames = []string{"opencode.jsonc", "opencode.json", "config.json"}

// ConfigPath returns the OpenCode global config path to write, honoring
// XDG_CONFIG_HOME and OpenCode's filename resolution order.
func ConfigPath(home string) string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	dir := filepath.Join(base, "opencode")
	for _, name := range configFilenames {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join(dir, "opencode.json")
}

// BuildSubscriptions reads the ghost-tab config files and resolves each config
// into a Subscription (api key, mapped models, base URL, active flag).
func BuildSubscriptions(in Inputs) []Subscription {
	configs := claudeconfig.Load(in.ListFile)
	active := claudeconfig.GetActive(in.PointerFile)
	var subs []Subscription
	for _, c := range configs {
		models := claudeconfig.ModelsForConfig(c.Name)
		idx := claudeconfig.ReadModelMappings(in.ConfigsDir, c.File, models)
		var mapped []string
		seen := map[string]bool{}
		for _, i := range idx {
			if i >= 0 && i < len(models) && !seen[models[i]] {
				seen[models[i]] = true
				mapped = append(mapped, models[i])
			}
		}
		opus := ""
		if idx[0] >= 0 && idx[0] < len(models) {
			opus = models[idx[0]]
		}
		subs = append(subs, Subscription{
			Name:      c.Name,
			File:      c.File,
			APIKey:    claudeconfig.ReadAPIKey(in.ConfigsDir, c.File),
			BaseURL:   claudeconfig.ProviderBaseURL(c.Name),
			OpusModel: opus,
			Models:    mapped,
			Active:    c.File == active,
		})
	}
	return subs
}

// Sync rebuilds the ghost-tab-* providers in OpenCode's global config. It is
// best-effort: recoverable failures log a warning and return nil so a Claude-side
// mutation is never blocked.
func Sync(in Inputs) error {
	home := in.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home == "" {
		return nil
	}
	path := ConfigPath(home)

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "ghost-tab: opencode sync: read %s: %v\n", path, err)
		return nil
	}

	subs := BuildSubscriptions(in)
	for _, s := range subs {
		if s.APIKey != "" && len(s.Models) > 0 && s.BaseURL == "" {
			fmt.Fprintf(os.Stderr, "ghost-tab: opencode sync: no base URL for %q; skipped\n", s.Name)
		}
	}

	out, ok := MergeSubscriptions(existing, subs)
	if !ok {
		fmt.Fprintf(os.Stderr, "ghost-tab: opencode sync: %s is not plain JSON; left unchanged\n", path)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "ghost-tab: opencode sync: mkdir: %v\n", err)
		return nil
	}
	if err := os.WriteFile(path, out, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "ghost-tab: opencode sync: write %s: %v\n", path, err)
		return nil
	}
	// os.WriteFile only sets perms on creation; tighten an existing file too,
	// since it now holds the plaintext API key. Best-effort.
	_ = os.Chmod(path, 0600)
	return nil
}
