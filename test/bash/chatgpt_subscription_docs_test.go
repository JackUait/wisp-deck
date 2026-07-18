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
		"ChatGPT",
		"opens ChatGPT sign-in in your browser automatically",
		"copy the printed URL",
		"OpenAI Platform API-key",
		"never reads or stores",
		"not mirrored into OpenCode",
		"relaunch",
	} {
		if !strings.Contains(readmeText, want) {
			t.Errorf("README is missing %q", want)
		}
	}
	for _, want := range []string{
		"Unreleased",
		"OpenAI GPT",
		"opens the ChatGPT browser login automatically",
	} {
		if !strings.Contains(string(changelog), want) {
			t.Errorf("CHANGELOG is missing %q", want)
		}
	}
}

func TestSubscriptionModalDocumentation(t *testing.T) {
	root := projectRoot(t)
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	readmeText := strings.Join(strings.Fields(string(readme)), " ")
	for _, want := range []string{
		"Settings → Subscription",
		"every configured profile",
		"Use profile",
		"Save changes",
		"add a profile",
		"rename or delete",
	} {
		if !strings.Contains(readmeText, want) {
			t.Errorf("README subscription manager documentation is missing %q", want)
		}
	}
}
