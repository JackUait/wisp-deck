package claudeconfig

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jackuait/wisp-deck/internal/claudeaccount"
)

func writeColorsFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestColorForReadsExistingAssignment(t *testing.T) {
	dir := t.TempDir()
	colors := writeColorsFixture(t, dir, "claude-config-colors", "openai-chatgpt.json:205\n")

	if got := ColorFor(colors, "openai-chatgpt.json"); got != 205 {
		t.Fatalf("color = %d, want 205", got)
	}
}

func TestColorForAssignsPaletteMemberAndPersists(t *testing.T) {
	dir := t.TempDir()
	colors := filepath.Join(dir, "claude-config-colors")

	got := ColorFor(colors, "glm.json")

	inPalette := false
	for _, c := range claudeaccount.Palette {
		if c == got {
			inPalette = true
		}
	}
	if !inPalette {
		t.Fatalf("assigned color %d is not a palette member", got)
	}
	data, err := os.ReadFile(colors)
	if err != nil {
		t.Fatalf("assignment was not persisted: %v", err)
	}
	if !strings.Contains(string(data), "glm.json:") {
		t.Fatalf("colors file missing assignment:\n%s", string(data))
	}
	if again := ColorFor(colors, "glm.json"); again != got {
		t.Fatalf("color changed across calls: %d then %d", got, again)
	}
}

func TestColorForAvoidsColorsFromOtherFiles(t *testing.T) {
	dir := t.TempDir()
	// Accounts already wear every palette color except one — the new
	// subscription must take the remaining one so identities stay distinct.
	free := claudeaccount.Palette[len(claudeaccount.Palette)-1]
	var lines []string
	for i, c := range claudeaccount.Palette[:len(claudeaccount.Palette)-1] {
		lines = append(lines, "acct"+strconv.Itoa(i)+":"+strconv.Itoa(c))
	}
	accountColors := writeColorsFixture(t, dir, "claude-account-colors", strings.Join(lines, "\n")+"\n")
	colors := filepath.Join(dir, "claude-config-colors")

	if got := ColorFor(colors, "glm.json", accountColors); got != free {
		t.Fatalf("color = %d, want the only free palette member %d", got, free)
	}
}
