package models

import (
	"os"
	"path/filepath"
	"strings"
)

// LoadDisabledTools reads the disabled-tools file (one tool name per line).
// A missing or unreadable file means nothing is disabled.
func LoadDisabledTools(path string) map[string]bool {
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

// ToggleDisabledTool flips a tool's membership in the disabled-tools file and
// rewrites it. Returns the tool's new disabled state.
func ToggleDisabledTool(path, tool string) (bool, error) {
	disabled := LoadDisabledTools(path)
	nowDisabled := !disabled[tool]
	if nowDisabled {
		disabled[tool] = true
	} else {
		delete(disabled, tool)
	}

	// Stable order so the file diffs cleanly: known tools first, then extras.
	var lines []string
	for _, name := range []string{"claude", "opencode", "codex"} {
		if disabled[name] {
			lines = append(lines, name)
			delete(disabled, name)
		}
	}
	for name := range disabled {
		lines = append(lines, name)
	}

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
