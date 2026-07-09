package models_test

import (
	"testing"

	"github.com/jackuait/wisp-deck/internal/models"
)

// Codex is a peer of OpenCode: detected on PATH, selectable, launchable — and
// opted out of the claude-exclusive machinery. It is listed after opencode so
// claude keeps first-available priority.

func TestDetectAITools_includes_codex(t *testing.T) {
	tools := models.DetectAITools()

	var found *models.AITool
	for i := range tools {
		if tools[i].Name == "codex" {
			found = &tools[i]
		}
	}
	if found == nil {
		t.Fatalf("DetectAITools() must include codex; got %+v", tools)
	}
	// Unlike opencode (which is probed via npx), codex is a plain PATH binary.
	if found.Command != "codex" {
		t.Errorf("codex Command = %q, want %q", found.Command, "codex")
	}
}

func TestDetectAITools_codex_is_listed_after_opencode(t *testing.T) {
	tools := models.DetectAITools()
	idx := map[string]int{}
	for i, tool := range tools {
		idx[tool.Name] = i
	}
	if idx["claude"] > idx["opencode"] || idx["opencode"] > idx["codex"] {
		t.Errorf("want order claude < opencode < codex, got %v", idx)
	}
}

func TestDisplayName_codex(t *testing.T) {
	if got := models.DisplayName("codex"); got != "Codex" {
		t.Errorf("DisplayName(codex) = %q, want %q", got, "Codex")
	}
}

func TestAITool_String_codex(t *testing.T) {
	if got := (models.AITool{Name: "codex", Installed: true}).String(); got != "Codex ✓" {
		t.Errorf("got %q, want %q", got, "Codex ✓")
	}
	if got := (models.AITool{Name: "codex"}).String(); got != "Codex (not installed)" {
		t.Errorf("got %q, want %q", got, "Codex (not installed)")
	}
}

// CycleTool and ValidateTool are list-generic, but the AI-tool row in the main
// menu now cycles through three tools rather than two.
func TestCycleTool_cycles_through_three_tools(t *testing.T) {
	tools := []string{"claude", "opencode", "codex"}
	if got := models.CycleTool(tools, "opencode", 1); got != "codex" {
		t.Errorf("next after opencode = %q, want codex", got)
	}
	if got := models.CycleTool(tools, "codex", 1); got != "claude" {
		t.Errorf("next after codex wraps to %q, want claude", got)
	}
	if got := models.CycleTool(tools, "claude", -1); got != "codex" {
		t.Errorf("prev before claude wraps to %q, want codex", got)
	}
}
