package gptbridge

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestValidateChatGPTSubscriptionAuth(t *testing.T) {
	tests := []struct {
		name    string
		account AccountReadResult
		wantErr string
	}{
		{
			name: "ChatGPT",
			account: AccountReadResult{
				Account:            &Account{Type: "chatgpt", PlanType: "plus"},
				RequiresOpenAIAuth: true,
			},
		},
		{
			name:    "signed out",
			account: AccountReadResult{RequiresOpenAIAuth: true},
			wantErr: "codex login",
		},
		{
			name:    "API key",
			account: AccountReadResult{Account: &Account{Type: "apiKey"}},
			wantErr: "API-key",
		},
		{
			name:    "external tokens",
			account: AccountReadResult{Account: &Account{Type: "chatgptAuthTokens"}},
			wantErr: "unsupported",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateChatGPTSubscription(tt.account)
			if tt.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestBuildClaudeEnvironmentOverridesOnlyBridgeRouting(t *testing.T) {
	base := []string{
		"PATH=/bin", "HOME=/home/test",
		"ANTHROPIC_BASE_URL=https://old.example",
		"ANTHROPIC_API_KEY=old", "ANTHROPIC_AUTH_TOKEN=old-token",
		"NO_PROXY=example.com",
	}
	got := BuildClaudeEnvironment(base, "http://127.0.0.1:4321", "bridge-secret")
	joined := "\n" + strings.Join(got, "\n") + "\n"
	for _, want := range []string{
		"\nPATH=/bin\n",
		"\nHOME=/home/test\n",
		"\nANTHROPIC_BASE_URL=http://127.0.0.1:4321\n",
		"\nANTHROPIC_API_KEY=bridge-secret\n",
		"\nANTHROPIC_AUTH_TOKEN=bridge-secret\n",
		"\nNO_PROXY=example.com,127.0.0.1,localhost\n",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("environment missing %q:\n%s", want, joined)
		}
	}
	for _, key := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "NO_PROXY"} {
		if countEnv(got, key) != 1 {
			t.Errorf("%s occurs %d times", key, countEnv(got, key))
		}
	}
}

func countEnv(env []string, key string) int {
	count := 0
	for _, entry := range env {
		if strings.HasPrefix(entry, key+"=") {
			count++
		}
	}
	return count
}

func TestRunAdapterLaunchesClaudeWithPrivateBridgeEnvironment(t *testing.T) {
	dir := t.TempDir()
	codex := filepath.Join(dir, "codex")
	claude := filepath.Join(dir, "claude")
	envLog := filepath.Join(dir, "claude-env")
	codexScript := `#!/bin/sh
set -eu
read init
printf '{"id":1,"result":{"userAgent":"fake","codexHome":"/tmp","platformFamily":"unix","platformOs":"macos"}}\n'
read initialized
read account
printf '{"id":2,"result":{"account":{"type":"chatgpt","email":null,"planType":"plus"},"requiresOpenaiAuth":true}}\n'
read models
printf '{"id":3,"result":{"data":[{"id":"gpt-test","model":"gpt-test","displayName":"GPT Test","description":"test","hidden":false,"isDefault":true,"defaultReasoningEffort":"medium","supportedReasoningEfforts":[]}]}}\n'
while read line; do :; done
`
	claudeScript := `#!/bin/sh
set -eu
{
  printf 'BASE=%s\n' "$ANTHROPIC_BASE_URL"
  printf 'KEY=%s\n' "$ANTHROPIC_API_KEY"
  printf 'TOKEN=%s\n' "$ANTHROPIC_AUTH_TOKEN"
  printf 'NO_PROXY=%s\n' "$NO_PROXY"
  printf 'PGID=%s\n' "$(ps -o pgid= -p $$ | tr -d ' ')"
} > "$CLAUDE_ENV_LOG"
`
	if err := os.WriteFile(codex, []byte(codexScript), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claude, []byte(claudeScript), 0700); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := RunAdapter(ctx, AdapterOptions{
		CodexPath: codex, ClaudeArgv: []string{claude},
		Environment:   append(os.Environ(), "CLAUDE_ENV_LOG="+envLog),
		ClientVersion: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("result = %+v", result)
	}
	data, err := os.ReadFile(envLog)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "BASE=http://127.0.0.1:") {
		t.Fatalf("Claude env = %s", text)
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	var key, token string
	for _, line := range lines {
		if strings.HasPrefix(line, "KEY=") {
			key = strings.TrimPrefix(line, "KEY=")
		}
		if strings.HasPrefix(line, "TOKEN=") {
			token = strings.TrimPrefix(line, "TOKEN=")
		}
	}
	if key == "" || key != token || key == "old" {
		t.Fatalf("bridge keys were not private/equal: key=%q token=%q", key, token)
	}
	if !strings.Contains(text, "PGID="+strconv.Itoa(syscall.Getpgrp())+"\n") {
		t.Fatalf("Claude left the interactive foreground process group: %s", text)
	}
}

func TestRunAdapterRejectsAPIKeyAuthBeforeLaunchingClaude(t *testing.T) {
	dir := t.TempDir()
	codex := filepath.Join(dir, "codex")
	claude := filepath.Join(dir, "claude")
	launched := filepath.Join(dir, "launched")
	codexScript := `#!/bin/sh
set -eu
read init
printf '{"id":1,"result":{"userAgent":"fake","codexHome":"/tmp","platformFamily":"unix","platformOs":"macos"}}\n'
read initialized
read account
printf '{"id":2,"result":{"account":{"type":"apiKey"},"requiresOpenaiAuth":true}}\n'
read models
printf '{"id":3,"result":{"data":[]}}\n'
while read line; do :; done
`
	claudeScript := `#!/bin/sh
touch "$CLAUDE_LAUNCHED"
`
	if err := os.WriteFile(codex, []byte(codexScript), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claude, []byte(claudeScript), 0700); err != nil {
		t.Fatal(err)
	}

	_, err := RunAdapter(context.Background(), AdapterOptions{
		CodexPath: codex, ClaudeArgv: []string{claude},
		Environment:   append(os.Environ(), "CLAUDE_LAUNCHED="+launched),
		ClientVersion: "test",
	})
	if err == nil || !strings.Contains(err.Error(), "API-key") {
		t.Fatalf("RunAdapter error = %v", err)
	}
	if _, statErr := os.Stat(launched); !os.IsNotExist(statErr) {
		t.Fatal("Claude launched despite API-key Codex auth")
	}
}
