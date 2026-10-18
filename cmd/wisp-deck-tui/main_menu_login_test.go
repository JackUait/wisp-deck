package main

import (
	"slices"
	"strings"
	"testing"
)

// A pane exports its own login and the All-In router's endpoint and token.
// Claude ranks ANTHROPIC_AUTH_TOKEN, ANTHROPIC_API_KEY and
// CLAUDE_CODE_OAUTH_TOKEN above a /login, so any of them leaking into the
// sign-in would point it at the wrong credential.
func TestClaudeLoginEnv_dropsPaneCredentialsAndSetsTheLoginDir(t *testing.T) {
	parent := []string{
		"PATH=/bin", "HOME=/h",
		"CLAUDE_CONFIG_DIR=/elsewhere",
		"ANTHROPIC_BASE_URL=http://127.0.0.1:9", "ANTHROPIC_AUTH_TOKEN=x", "ANTHROPIC_API_KEY=y",
		"CLAUDE_CODE_OAUTH_TOKEN=z", "CLAUDE_CODE_SESSION_ID=s", "CLAUDECODE=1",
	}
	got := claudeLoginEnv(parent, "/accounts/work")
	for _, kept := range []string{"PATH=/bin", "HOME=/h", "CLAUDE_CONFIG_DIR=/accounts/work"} {
		if !slices.Contains(got, kept) {
			t.Errorf("env missing %q: %v", kept, got)
		}
	}
	for _, kv := range got {
		if strings.HasPrefix(kv, "ANTHROPIC_") || strings.HasPrefix(kv, "CLAUDE_CODE_") ||
			strings.HasPrefix(kv, "CLAUDECODE=") || kv == "CLAUDE_CONFIG_DIR=/elsewhere" {
			t.Errorf("env kept %q", kv)
		}
	}
}

// The Default login is the one with no CLAUDE_CONFIG_DIR at all.
func TestClaudeLoginEnv_defaultLoginHasNoConfigDir(t *testing.T) {
	for _, kv := range claudeLoginEnv([]string{"CLAUDE_CONFIG_DIR=/elsewhere", "PATH=/bin"}, "") {
		if strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
			t.Fatalf("Default login got %q", kv)
		}
	}
}

func TestClaudeLoginArgs(t *testing.T) {
	if got := claudeLoginArgs(""); !slices.Equal(got, []string{"auth", "login", "--claudeai"}) {
		t.Errorf("no email: %v", got)
	}
	if got := claudeLoginArgs("w@corp.example"); !slices.Equal(got, []string{"auth", "login", "--claudeai", "--email", "w@corp.example"}) {
		t.Errorf("email: %v", got)
	}
}
