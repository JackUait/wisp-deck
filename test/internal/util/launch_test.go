package util_test

import (
	"strings"
	"testing"

	"github.com/jackuait/ghost-tab/internal/util"
)

func TestBuildAILaunchCmd(t *testing.T) {
	// --- Basic tool behavior ---

	t.Run("claude passes args through", func(t *testing.T) {
		result := util.BuildAILaunchCmd("claude", "/usr/bin/claude", "", []string{"--resume"})
		expected := "/usr/bin/claude --resume"
		if result != expected {
			t.Errorf("got %q, want %q", result, expected)
		}
	})

	t.Run("opencode passes project dir via npx", func(t *testing.T) {
		result := util.BuildAILaunchCmd("opencode", "npx opencode-ai@latest", "/my/project", nil)
		expected := `npx opencode-ai@latest "/my/project"`
		if result != expected {
			t.Errorf("got %q, want %q", result, expected)
		}
	})

	// --- opencode edge cases ---

	t.Run("handles project path with spaces (opencode)", func(t *testing.T) {
		result := util.BuildAILaunchCmd("opencode", "npx opencode-ai@latest", "/path/with spaces", nil)
		expected := `npx opencode-ai@latest "/path/with spaces"`
		if result != expected {
			t.Errorf("got %q, want %q", result, expected)
		}
	})

	t.Run("handles project path with double quotes (opencode)", func(t *testing.T) {
		result := util.BuildAILaunchCmd("opencode", "npx opencode-ai@latest", `/path/with"quotes`, nil)
		expected := `npx opencode-ai@latest "/path/with"quotes"`
		if result != expected {
			t.Errorf("got %q, want %q", result, expected)
		}
	})

	t.Run("handles project path with unicode (opencode)", func(t *testing.T) {
		result := util.BuildAILaunchCmd("opencode", "npx opencode-ai@latest", "/path/émoji/\U0001F47B", nil)
		expected := "npx opencode-ai@latest \"/path/émoji/\U0001F47B\""
		if result != expected {
			t.Errorf("got %q, want %q", result, expected)
		}
	})

	t.Run("handles very long project path (opencode)", func(t *testing.T) {
		longPath := strings.Repeat("/very/long/path", 50)
		result := util.BuildAILaunchCmd("opencode", "npx opencode-ai@latest", longPath, nil)
		expected := `npx opencode-ai@latest "` + longPath + `"`
		if result != expected {
			t.Errorf("got %q, want %q", result, expected)
		}
	})

	t.Run("handles empty project path (opencode)", func(t *testing.T) {
		result := util.BuildAILaunchCmd("opencode", "npx opencode-ai@latest", "", nil)
		expected := `npx opencode-ai@latest ""`
		if result != expected {
			t.Errorf("got %q, want %q", result, expected)
		}
	})

	// --- claude edge cases ---

	t.Run("handles claude with args containing spaces", func(t *testing.T) {
		result := util.BuildAILaunchCmd("claude", "/usr/bin/claude", "", []string{"--message 'test message'"})
		expected := "/usr/bin/claude --message 'test message'"
		if result != expected {
			t.Errorf("got %q, want %q", result, expected)
		}
	})

	t.Run("handles claude with multiple args", func(t *testing.T) {
		result := util.BuildAILaunchCmd("claude", "/usr/bin/claude", "", []string{"--resume", "--fast"})
		expected := "/usr/bin/claude --resume --fast"
		if result != expected {
			t.Errorf("got %q, want %q", result, expected)
		}
	})

	t.Run("handles claude with no args", func(t *testing.T) {
		result := util.BuildAILaunchCmd("claude", "/usr/bin/claude", "", nil)
		expected := "/usr/bin/claude"
		if result != expected {
			t.Errorf("got %q, want %q", result, expected)
		}
	})

	t.Run("handles unknown tool falls back to claude behavior", func(t *testing.T) {
		result := util.BuildAILaunchCmd("unknown-tool", "/usr/bin/unknown", "", []string{"--some-flag"})
		expected := "/usr/bin/unknown --some-flag"
		if result != expected {
			t.Errorf("got %q, want %q", result, expected)
		}
	})
}

// Table-driven tests for comprehensive coverage
func TestBuildAILaunchCmdTable(t *testing.T) {
	longPath := strings.Repeat("/very/long/path", 50)

	tests := []struct {
		name       string
		tool       string
		command    string
		projectDir string
		args       []string
		expected   string
	}{
		{
			name:     "claude basic",
			tool:     "claude",
			command:  "/usr/bin/claude",
			args:     []string{"--resume"},
			expected: "/usr/bin/claude --resume",
		},
		{
			name:       "opencode basic",
			tool:       "opencode",
			command:    "npx opencode-ai@latest",
			projectDir: "/my/project",
			expected:   `npx opencode-ai@latest "/my/project"`,
		},
		{
			name:       "opencode spaces in path",
			tool:       "opencode",
			command:    "npx opencode-ai@latest",
			projectDir: "/path/with spaces",
			expected:   `npx opencode-ai@latest "/path/with spaces"`,
		},
		{
			name:       "opencode double quotes in path",
			tool:       "opencode",
			command:    "npx opencode-ai@latest",
			projectDir: `/path/with"quotes`,
			expected:   `npx opencode-ai@latest "/path/with"quotes"`,
		},
		{
			name:       "opencode unicode",
			tool:       "opencode",
			command:    "npx opencode-ai@latest",
			projectDir: "/path/émoji/\U0001F47B",
			expected:   "npx opencode-ai@latest \"/path/émoji/\U0001F47B\"",
		},
		{
			name:       "opencode long path",
			tool:       "opencode",
			command:    "npx opencode-ai@latest",
			projectDir: longPath,
			expected:   `npx opencode-ai@latest "` + longPath + `"`,
		},
		{
			name:       "opencode empty path",
			tool:       "opencode",
			command:    "npx opencode-ai@latest",
			projectDir: "",
			expected:   `npx opencode-ai@latest ""`,
		},
		{
			name:     "claude args with spaces",
			tool:     "claude",
			command:  "/usr/bin/claude",
			args:     []string{"--message 'test message'"},
			expected: "/usr/bin/claude --message 'test message'",
		},
		{
			name:     "claude multiple args",
			tool:     "claude",
			command:  "/usr/bin/claude",
			args:     []string{"--resume", "--fast"},
			expected: "/usr/bin/claude --resume --fast",
		},
		{
			name:     "claude no args",
			tool:     "claude",
			command:  "/usr/bin/claude",
			expected: "/usr/bin/claude",
		},
		{
			name:     "unknown tool falls back to claude",
			tool:     "unknown-tool",
			command:  "/usr/bin/unknown",
			args:     []string{"--some-flag"},
			expected: "/usr/bin/unknown --some-flag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := util.BuildAILaunchCmd(tt.tool, tt.command, tt.projectDir, tt.args)
			if result != tt.expected {
				t.Errorf("BuildAILaunchCmd(%q, %q, %q, %v) = %q, want %q",
					tt.tool, tt.command, tt.projectDir, tt.args, result, tt.expected)
			}
		})
	}
}
