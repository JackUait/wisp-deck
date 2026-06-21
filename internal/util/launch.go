package util

import "strings"

// BuildAILaunchCmd constructs the shell command string to launch an AI tool.
// tool: the AI tool identifier (claude, opencode)
// command: the command/binary path to execute
// projectDir: the project directory path (used by opencode as positional arg)
// args: additional arguments (used by claude/unknown tools)
// Returns the complete command string ready for shell execution.
//
// Behavior per tool matches the bash build_ai_launch_cmd():
//   - opencode: command "projectDir"
//   - claude/unknown: command args... (space-joined, omitted if empty)
func BuildAILaunchCmd(tool, command, projectDir string, args []string) string {
	switch tool {
	case "opencode":
		return command + ` "` + projectDir + `"`
	default:
		// claude and unknown tools: append args if present
		extra := strings.Join(args, " ")
		if extra != "" {
			return command + " " + extra
		}
		return command
	}
}
