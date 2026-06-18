package models_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackuait/ghost-tab/internal/models"
)

func TestDetectAITools(t *testing.T) {
	// Create temp bin directory with mock commands
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	os.Mkdir(binDir, 0755)

	// Create mock executables
	os.WriteFile(filepath.Join(binDir, "claude"), []byte("#!/bin/bash\necho test"), 0755)
	os.WriteFile(filepath.Join(binDir, "codex"), []byte("#!/bin/bash\necho test"), 0755)

	// Update PATH for test
	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", binDir+":"+oldPath)
	defer os.Setenv("PATH", oldPath)

	tools := models.DetectAITools()

	// Check that claude and codex are detected
	claudeFound := false
	codexFound := false

	for _, tool := range tools {
		if tool.Name == "claude" && tool.Installed {
			claudeFound = true
		}
		if tool.Name == "codex" && tool.Installed {
			codexFound = true
		}
	}

	if !claudeFound {
		t.Error("Expected claude to be detected")
	}
	if !codexFound {
		t.Error("Expected codex to be detected")
	}
}

func TestAIToolString(t *testing.T) {
	tool := models.AITool{
		Name:      "claude",
		Command:   "claude",
		Installed: true,
	}

	str := tool.String()
	if str != "Claude Code ✓" {
		t.Errorf("Expected 'Claude Code ✓', got %q", str)
	}

	tool.Installed = false
	str = tool.String()
	if str != "Claude Code (not installed)" {
		t.Errorf("Expected 'Claude Code (not installed)', got %q", str)
	}
}

func TestDetectAITools_AllToolsDetected(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	os.Mkdir(binDir, 0755)

	// Create mock executables. OpenCode is detected via "npx" availability
	// (launched as npx opencode-ai@latest).
	for _, cmd := range []string{"claude", "codex", "npx"} {
		os.WriteFile(filepath.Join(binDir, cmd), []byte("#!/bin/bash\necho test"), 0755)
	}

	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", binDir+":"+oldPath)
	defer os.Setenv("PATH", oldPath)

	tools := models.DetectAITools()

	if len(tools) != 3 {
		t.Fatalf("Expected 3 tools, got %d", len(tools))
	}

	// claude, codex, opencode should be installed (their commands are single binaries)
	expected := map[string]bool{
		"claude":   true,
		"codex":    true,
		"opencode": true,
	}

	for _, tool := range tools {
		want, ok := expected[tool.Name]
		if !ok {
			t.Errorf("Unexpected tool: %s", tool.Name)
			continue
		}
		if tool.Installed != want {
			t.Errorf("Tool %s: expected Installed=%v, got %v", tool.Name, want, tool.Installed)
		}
	}
}

func TestDetectAITools_NoneInstalled(t *testing.T) {
	// Use empty PATH so no tools are found
	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", t.TempDir())
	defer os.Setenv("PATH", oldPath)

	tools := models.DetectAITools()

	for _, tool := range tools {
		if tool.Installed {
			t.Errorf("Expected %s to not be installed with empty PATH", tool.Name)
		}
	}
}

func TestDisplayName(t *testing.T) {
	tests := []struct {
		tool string
		want string
	}{
		{"claude", "Claude Code"},
		{"codex", "Codex CLI"},
		{"opencode", "OpenCode"},
		{"vim", "vim"},
		{"unknown-tool", "unknown-tool"},
	}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			got := models.DisplayName(tt.tool)
			if got != tt.want {
				t.Errorf("DisplayName(%q) = %q, want %q", tt.tool, got, tt.want)
			}
		})
	}
}

func TestCycleTool(t *testing.T) {
	tests := []struct {
		name      string
		tools     []string
		current   string
		direction int
		want      string
	}{
		{
			name:      "next wraps from last to first",
			tools:     []string{"claude", "codex", "opencode"},
			current:   "opencode",
			direction: 1,
			want:      "claude",
		},
		{
			name:      "next advances by one",
			tools:     []string{"claude", "codex", "opencode"},
			current:   "claude",
			direction: 1,
			want:      "codex",
		},
		{
			name:      "prev wraps from first to last",
			tools:     []string{"claude", "codex", "opencode"},
			current:   "claude",
			direction: -1,
			want:      "opencode",
		},
		{
			name:      "no-op with single tool",
			tools:     []string{"claude"},
			current:   "claude",
			direction: 1,
			want:      "claude",
		},
		{
			name:      "current not found returns first",
			tools:     []string{"claude", "codex"},
			current:   "vim",
			direction: 1,
			want:      "claude",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := models.CycleTool(tt.tools, tt.current, tt.direction)
			if got != tt.want {
				t.Errorf("CycleTool(%v, %q, %d) = %q, want %q",
					tt.tools, tt.current, tt.direction, got, tt.want)
			}
		})
	}
}

func TestValidateTool(t *testing.T) {
	tests := []struct {
		name  string
		tools []string
		pref  string
		want  string
	}{
		{
			name:  "keeps valid preference",
			tools: []string{"claude", "codex"},
			pref:  "codex",
			want:  "codex",
		},
		{
			name:  "falls back to first when pref is invalid",
			tools: []string{"claude", "codex"},
			pref:  "vim",
			want:  "claude",
		},
		{
			name:  "falls back to first when pref is empty",
			tools: []string{"codex", "opencode"},
			pref:  "",
			want:  "codex",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := models.ValidateTool(tt.tools, tt.pref)
			if got != tt.want {
				t.Errorf("ValidateTool(%v, %q) = %q, want %q",
					tt.tools, tt.pref, got, tt.want)
			}
		})
	}
}

func TestAIToolString_AllTools(t *testing.T) {
	tests := []struct {
		name      string
		installed bool
		want      string
	}{
		{"claude", true, "Claude Code ✓"},
		{"claude", false, "Claude Code (not installed)"},
		{"codex", true, "Codex CLI ✓"},
		{"codex", false, "Codex CLI (not installed)"},
		{"opencode", true, "OpenCode ✓"},
		{"opencode", false, "OpenCode (not installed)"},
	}

	for _, tt := range tests {
		t.Run(tt.name+"_"+fmt.Sprintf("%v", tt.installed), func(t *testing.T) {
			tool := models.AITool{Name: tt.name, Installed: tt.installed}
			got := tool.String()
			if got != tt.want {
				t.Errorf("AITool{%q, installed=%v}.String() = %q, want %q", tt.name, tt.installed, got, tt.want)
			}
		})
	}
}
