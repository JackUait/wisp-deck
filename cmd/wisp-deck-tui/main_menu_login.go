package main

import (
	"os"
	"os/exec"
	"strings"
)

// claudeLoginCommand signs one login in again: configDir is "" for the
// Default login. The menu hands it the terminal through tea.Exec.
func claudeLoginCommand(configDir, email string) *exec.Cmd {
	claude, err := exec.LookPath("claude")
	if err != nil {
		claude = "claude"
	}
	cmd := exec.Command(claude, claudeLoginArgs(email)...)
	cmd.Env = claudeLoginEnv(os.Environ(), configDir)
	return cmd
}

// claudeLoginAllowed refuses only tests. It does not use the host-effects
// decision: that one denies any process under /usr/bin/login, which runs as
// root, so a user cannot read its arguments.
func claudeLoginAllowed(testBinary bool, testEnvironment string) bool {
	return !testBinary && testEnvironment != "1"
}

func claudeLoginArgs(email string) []string {
	args := []string{"auth", "login", "--claudeai"}
	if email != "" {
		args = append(args, "--email", email)
	}
	return args
}

// claudeLoginEnv drops what a pane exports about its own session. Claude
// ranks ANTHROPIC_* tokens and CLAUDE_CODE_OAUTH_TOKEN above a /login, and
// CLAUDE_CONFIG_DIR picks the Keychain slot the login lands in.
func claudeLoginEnv(parent []string, configDir string) []string {
	env := make([]string, 0, len(parent)+1)
	for _, kv := range parent {
		if strings.HasPrefix(kv, "ANTHROPIC_") || strings.HasPrefix(kv, "CLAUDE_CODE_") ||
			strings.HasPrefix(kv, "CLAUDECODE=") || strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
			continue
		}
		env = append(env, kv)
	}
	if configDir != "" {
		env = append(env, "CLAUDE_CONFIG_DIR="+configDir)
	}
	return env
}
