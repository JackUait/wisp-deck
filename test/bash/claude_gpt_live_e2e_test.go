package bash_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveGPTSubscriptionInClaude(t *testing.T) {
	if os.Getenv("WISP_DECK_LIVE_GPT_CLAUDE_E2E") != "1" {
		t.Skip("set WISP_DECK_LIVE_GPT_CLAUDE_E2E=1 to use local Codex and Claude logins")
	}
	codexPath, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal("codex is not installed")
	}
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		t.Fatal("claude is not installed")
	}
	status := exec.Command(codexPath, "login", "status")
	statusOutput, err := status.CombinedOutput()
	statusText := strings.ToLower(string(statusOutput))
	if strings.Contains(statusText, "api key") {
		t.Fatalf("Codex is using API-key authentication; ChatGPT subscription auth is required: %s", statusOutput)
	}
	if err != nil && !strings.Contains(statusText, "not logged in") {
		t.Fatalf("could not determine Codex login status: %v: %s", err, statusOutput)
	}

	root := projectRoot(t)
	binary := filepath.Join(t.TempDir(), "wisp-deck-tui")
	build := exec.Command("go", "build", "-o", binary, "./cmd/wisp-deck-tui")
	build.Dir = root
	if output, buildErr := build.CombinedOutput(); buildErr != nil {
		t.Fatalf("build live adapter: %v\n%s", buildErr, output)
	}

	const marker = "WISP_TOOL_BRIDGE_OK"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		binary,
		"claude-gpt-adapter",
		"--codex", codexPath,
		"--",
		claudePath,
		"--bare",
		"--print",
		"--no-session-persistence",
		"--output-format", "stream-json",
		"--verbose",
		"--tools", "Bash",
		"--allowedTools", "Bash",
		"--settings", filepath.Join(root, "defaults", "claude-configs", "openai-gpt.json"),
		"Use the Bash tool exactly once to run: printf '"+marker+
			"'. Then reply with exactly the command stdout and nothing else.",
	)
	command.Dir = t.TempDir()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("live Claude GPT bridge: %v\n%s", err, output)
	}

	type contentBlock struct {
		Type      string `json:"type"`
		Name      string `json:"name"`
		Text      string `json:"text"`
		Content   string `json:"content"`
		IsError   bool   `json:"is_error"`
		ToolUseID string `json:"tool_use_id"`
		Input     struct {
			Command string `json:"command"`
		} `json:"input"`
	}
	type streamLine struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Result  string `json:"result"`
		Message struct {
			Content []contentBlock `json:"content"`
		} `json:"message"`
	}
	var sawToolCall, sawToolResult, sawFinalText, sawSuccess bool
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		var event streamLine
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("invalid Claude stream JSON: %v\n%s", err, scanner.Bytes())
		}
		for _, block := range event.Message.Content {
			if event.Type == "assistant" && block.Type == "tool_use" &&
				block.Name == "Bash" && block.Input.Command == "printf '"+marker+"'" {
				sawToolCall = true
			}
			if event.Type == "user" && block.Type == "tool_result" &&
				block.ToolUseID != "" && block.Content == marker && !block.IsError {
				sawToolResult = true
			}
			if event.Type == "assistant" && block.Type == "text" && block.Text == marker {
				sawFinalText = true
			}
		}
		if event.Type == "result" && event.Subtype == "success" && event.Result == marker {
			sawSuccess = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !sawToolCall || !sawToolResult || !sawFinalText || !sawSuccess {
		t.Fatalf(
			"live bridge incomplete: tool_call=%t tool_result=%t final_text=%t success=%t\n%s",
			sawToolCall, sawToolResult, sawFinalText, sawSuccess, output,
		)
	}
}
