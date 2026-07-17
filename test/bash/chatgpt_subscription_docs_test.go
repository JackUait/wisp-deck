package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatGPTSubscriptionDocumentationCoversSetupAndSafety(t *testing.T) {
	root := projectRoot(t)
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	changelog, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	readmeText := string(readme)
	for _, want := range []string{
		"OpenAI GPT",
		"codex login",
		"ChatGPT",
		"OpenAI Platform API-key",
		"never reads or stores",
		"not mirrored into OpenCode",
		"relaunch",
	} {
		if !strings.Contains(readmeText, want) {
			t.Errorf("README is missing %q", want)
		}
	}
	for _, want := range []string{"Unreleased", "OpenAI GPT", "codex login"} {
		if !strings.Contains(string(changelog), want) {
			t.Errorf("CHANGELOG is missing %q", want)
		}
	}
}
