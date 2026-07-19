package claudeconfig

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Disabled subscriptions: a config listed in the disabled file stays fully
// manageable in the Subscription modal but is hidden from the in-session
// switcher popup. The file lives next to the list file (one config filename
// per line), so every surface that knows the list can derive it — no extra
// flag plumbing, same pattern as the claude-config-colors sidecar.

// DisabledFile returns the disabled-configs sidecar path for a configs list
// file, or "" when no list path is known.
func DisabledFile(listFile string) string {
	if listFile == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(listFile), "claude-configs.disabled")
}

// LoadDisabled reads the disabled-configs file (one config filename per line).
// A missing or unreadable file means nothing is disabled.
func LoadDisabled(path string) map[string]bool {
	disabled := map[string]bool{}
	data, err := os.ReadFile(path)
	if err != nil {
		return disabled
	}
	for _, line := range strings.Split(string(data), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			disabled[name] = true
		}
	}
	return disabled
}

// ToggleDisabled flips a config's membership in the disabled-configs file and
// rewrites it. Returns the config's new disabled state.
func ToggleDisabled(path, file string) (bool, error) {
	disabled := LoadDisabled(path)
	nowDisabled := !disabled[file]
	if nowDisabled {
		disabled[file] = true
	} else {
		delete(disabled, file)
	}

	lines := make([]string, 0, len(disabled))
	for name := range disabled {
		lines = append(lines, name)
	}
	sort.Strings(lines)

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, err
	}
	content := ""
	if len(lines) > 0 {
		content = strings.Join(lines, "\n") + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return false, err
	}
	return nowDisabled, nil
}
