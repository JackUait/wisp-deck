package models

import (
	"os/exec"
)

// AITool represents an AI coding assistant
type AITool struct {
	Name      string
	Command   string
	Installed bool
}

// String returns display string for AI tool
func (t AITool) String() string {
	displayName := t.Name
	switch t.Name {
	case "claude":
		displayName = "Claude Code"
	case "opencode":
		displayName = "OpenCode"
	}

	if t.Installed {
		return displayName + " ✓"
	}
	return displayName + " (not installed)"
}

// DetectAITools checks which AI tools are installed
func DetectAITools() []AITool {
	tools := []AITool{
		{Name: "claude", Command: "claude"},
		{Name: "opencode", Command: "npx opencode-ai@latest"},
	}

	for i := range tools {
		detectCmd := tools[i].Command
		// OpenCode is launched via npx — detect by checking for the npx binary
		if tools[i].Name == "opencode" {
			detectCmd = "npx"
		}
		tools[i].Installed = isCommandAvailable(detectCmd)
	}

	return tools
}

func isCommandAvailable(command string) bool {
	_, err := exec.LookPath(command)
	return err == nil
}

// DisplayName returns the human-readable name for an AI tool identifier.
// Unknown tools pass through unchanged.
func DisplayName(tool string) string {
	switch tool {
	case "claude":
		return "Claude Code"
	case "opencode":
		return "OpenCode"
	default:
		return tool
	}
}

// CycleTool cycles through available tools. direction=1 for next, direction=-1 for prev.
// Returns the new tool identifier. If there is only one tool, returns it unchanged.
// If current is not found in tools, returns the first tool.
func CycleTool(tools []string, current string, direction int) string {
	if len(tools) <= 1 {
		if len(tools) == 1 {
			return tools[0]
		}
		return current
	}

	for i, t := range tools {
		if t == current {
			next := (i + direction + len(tools)) % len(tools)
			return tools[next]
		}
	}

	// Current not found, return first tool
	return tools[0]
}

// ValidateTool returns the preference if it's in the tools list, otherwise returns the first tool.
func ValidateTool(tools []string, pref string) string {
	for _, t := range tools {
		if t == pref {
			return pref
		}
	}
	if len(tools) > 0 {
		return tools[0]
	}
	return pref
}
