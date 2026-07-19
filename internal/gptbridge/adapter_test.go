package gptbridge

import (
	"bytes"
	"context"
	"errors"
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
			wantErr: "relaunch",
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

func TestRunAdapterMissingCodexExplainsAutomaticLoginRecovery(t *testing.T) {
	_, err := RunAdapter(context.Background(), AdapterOptions{
		ClaudeArgv: []string{"claude"},
	})
	if err == nil || !strings.Contains(err.Error(), "relaunch") {
		t.Fatalf("RunAdapter error = %v, want relaunch recovery", err)
	}
	if strings.Contains(err.Error(), "codex login") {
		t.Fatalf("RunAdapter still requires manual login: %v", err)
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
		"\nANTHROPIC_AUTH_TOKEN=bridge-secret\n",
		"\nNO_PROXY=example.com,127.0.0.1,localhost\n",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("environment missing %q:\n%s", want, joined)
		}
	}
	if countEnv(got, "ANTHROPIC_API_KEY") != 0 {
		t.Fatalf("ANTHROPIC_API_KEY leaked into Claude environment: %q", got)
	}
	for _, key := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "NO_PROXY"} {
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
  if env | grep '^ANTHROPIC_API_KEY=' >/dev/null; then
    printf 'KEY_SET=yes\n'
  else
    printf 'KEY_SET=no\n'
  fi
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
	var keySet, token string
	for _, line := range lines {
		if strings.HasPrefix(line, "KEY_SET=") {
			keySet = strings.TrimPrefix(line, "KEY_SET=")
		}
		if strings.HasPrefix(line, "TOKEN=") {
			token = strings.TrimPrefix(line, "TOKEN=")
		}
	}
	if keySet != "no" || token == "" || token == "old-token" {
		t.Fatalf("bridge token was not private/token-only: key_set=%q token=%q", keySet, token)
	}
	if !strings.Contains(text, "PGID="+strconv.Itoa(syscall.Getpgrp())+"\n") {
		t.Fatalf("Claude left the interactive foreground process group: %s", text)
	}
}

func TestRunAdapterLogsInSignedOutChatGPTUser(t *testing.T) {
	dir := t.TempDir()
	codex := filepath.Join(dir, "codex")
	claude := filepath.Join(dir, "claude")
	launched := filepath.Join(dir, "launched")
	codexScript := `#!/bin/sh
set -eu
read init
printf '{"id":1,"result":{"userAgent":"fake","codexHome":"/tmp","platformFamily":"unix","platformOs":"macos"}}\n'
read initialized
read account_before_login
printf '{"id":2,"result":{"account":null,"requiresOpenaiAuth":true}}\n'
read models_before_login
printf '{"id":3,"result":{"data":[]}}\n'
read login_start
printf '{"id":4,"result":{"type":"chatgpt","loginId":"login-42","authUrl":"https://chatgpt.com/auth/wisp"}}\n'
printf '{"method":"account/login/completed","params":{"loginId":"login-42","success":true,"error":null}}\n'
read account_after_login
printf '{"id":5,"result":{"account":{"type":"chatgpt","email":"user@example.com","planType":"plus"},"requiresOpenaiAuth":true}}\n'
read models_after_login
printf '{"id":6,"result":{"data":[{"id":"gpt-test","model":"gpt-test","displayName":"GPT Test","description":"test","hidden":false,"isDefault":true,"defaultReasoningEffort":"medium","supportedReasoningEfforts":[]}]}}\n'
while read line; do :; done
`
	claudeScript := `#!/bin/sh
set -eu
touch "$CLAUDE_LAUNCHED"
`
	if err := os.WriteFile(codex, []byte(codexScript), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claude, []byte(claudeScript), 0700); err != nil {
		t.Fatal(err)
	}

	var openedURL string
	var stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := RunAdapter(ctx, AdapterOptions{
		CodexPath: codex, ClaudeArgv: []string{claude},
		Environment:   append(os.Environ(), "CLAUDE_LAUNCHED="+launched),
		ClientVersion: "test",
		Stderr:        &stderr,
		OpenURL: func(authURL string) error {
			openedURL = authURL
			return errors.New("browser unavailable")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("result = %+v", result)
	}
	if openedURL != "https://chatgpt.com/auth/wisp" {
		t.Fatalf("opened URL = %q", openedURL)
	}
	if !strings.Contains(stderr.String(), "https://chatgpt.com/auth/wisp") {
		t.Fatalf("login URL was not printed:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Could not open the browser automatically") {
		t.Fatalf("browser failure did not preserve the manual URL path:\n%s", stderr.String())
	}
	if _, err := os.Stat(launched); err != nil {
		t.Fatalf("Claude did not launch after login: %v", err)
	}
}

func TestOpenChatGPTAuthURLWaitsForBrowserOpener(t *testing.T) {
	dir := t.TempDir()
	opener := filepath.Join(dir, "open")
	logPath := filepath.Join(dir, "opened-url")
	script := `#!/bin/sh
sleep 0.05
printf '%s' "$1" > "$OPEN_URL_LOG"
`
	if err := os.WriteFile(opener, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OPEN_URL_LOG", logPath)

	if err := OpenChatGPTAuthURL("https://chatgpt.com/auth/wisp"); err != nil {
		t.Fatal(err)
	}
	opened, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("browser opener was not reaped after completion: %v", err)
	}
	if string(opened) != "https://chatgpt.com/auth/wisp" {
		t.Fatalf("browser opener URL = %q", opened)
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
