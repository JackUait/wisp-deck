package bash_test

import (
	"fmt"
	"strings"
	"testing"
)

// --- AI Select Tests (from test/ai-select.bats) ---

// buildAiSelectScript builds a bash script that mocks ghost-tab-tui, jq, and
// the error function, then sources ai-select-tui.sh and calls select_ai_tool_interactive.
// After the function call, it prints the values of _selected_ai_tool and _selected_ai_tools
// so we can assert on them from Go.
func buildAiSelectScript(t *testing.T, tmpDir string, ghostTabTuiBody string, jqBody string, extraSetup string) string {
	t.Helper()
	root := projectRoot(t)

	// Create mock ghost-tab-tui
	mockCommand(t, tmpDir, "ghost-tab-tui", ghostTabTuiBody)

	// Create mock jq - use stateful counter approach
	mockCommand(t, tmpDir, "jq", jqBody)

	return fmt.Sprintf(`
set -euo pipefail
export PATH="%s/bin:$PATH"
export TEST_TMP="%s"

# Provide error function since tui.sh may not be sourced
error() { echo "ERROR: $*" >&2; }

%s

source %q

select_ai_tool_interactive
result=$?

echo "EXIT_CODE=$result"
echo "SELECTED_TOOL=$_selected_ai_tool"
echo "SELECTED_TOOLS=$_selected_ai_tools"
exit $result
`, tmpDir, tmpDir, extraSetup, root+"/lib/ai-select-tui.sh")
}

func TestAiSelect_CallsGhostTabTuiMultiSelectAiTool(t *testing.T) {
	tmpDir := t.TempDir()

	ghostTabTuiBody := `
if [ "$1" = "multi-select-ai-tool" ]; then
    echo '{"tools":["claude","opencode"],"confirmed":true}'
    exit 0
fi
exit 1
`
	jqBody := fmt.Sprintf(`#!/bin/bash
counter_file="%s/jq_calls"
count=$(cat "$counter_file" 2>/dev/null || echo 0)
count=$((count + 1))
echo "$count" > "$counter_file"

# Parse args - $2 is the jq filter, stdin is the input
if [ "$2" = ".confirmed" ]; then
    echo "true"
elif [ "$2" = ".tools[]" ]; then
    printf "claude\nopencode\n"
fi
exit 0
`, tmpDir)

	// Write the jq mock directly since buildAiSelectScript uses mockCommand
	mockCommand(t, tmpDir, "jq", jqBody)
	mockCommand(t, tmpDir, "ghost-tab-tui", ghostTabTuiBody)

	script := fmt.Sprintf(`
set -euo pipefail
export PATH="%s/bin:$PATH"
export TEST_TMP="%s"

error() { echo "ERROR: $*" >&2; }

source %q

select_ai_tool_interactive
result=$?

echo "SELECTED_TOOL=${_selected_ai_tool:-}"
echo "SELECTED_TOOLS=${_selected_ai_tools:-}"
exit $result
`, tmpDir, tmpDir, projectRoot(t)+"/lib/ai-select-tui.sh")

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "SELECTED_TOOL=claude")
}

func TestAiSelect_SetsSelectedAiTools(t *testing.T) {
	tmpDir := t.TempDir()

	ghostTabTuiBody := `
if [ "$1" = "multi-select-ai-tool" ]; then
    echo '{"tools":["claude","opencode"],"confirmed":true}'
    exit 0
fi
exit 1
`
	jqBody := fmt.Sprintf(`#!/bin/bash
counter_file="%s/jq_calls"
count=$(cat "$counter_file" 2>/dev/null || echo 0)
count=$((count + 1))
echo "$count" > "$counter_file"

if [ "$2" = ".confirmed" ]; then
    echo "true"
elif [ "$2" = ".tools[]" ]; then
    printf "claude\nopencode\n"
fi
exit 0
`, tmpDir)

	mockCommand(t, tmpDir, "ghost-tab-tui", ghostTabTuiBody)
	mockCommand(t, tmpDir, "jq", jqBody)

	script := fmt.Sprintf(`
set -euo pipefail
export PATH="%s/bin:$PATH"
export TEST_TMP="%s"

error() { echo "ERROR: $*" >&2; }

source %q

select_ai_tool_interactive
result=$?

echo "SELECTED_TOOL=${_selected_ai_tool:-}"
echo "SELECTED_TOOLS=${_selected_ai_tools:-}"
exit $result
`, tmpDir, tmpDir, projectRoot(t)+"/lib/ai-select-tui.sh")

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "claude")
	assertContains(t, out, "opencode")
}

func TestAiSelect_PicksClaudeByPriority(t *testing.T) {
	tmpDir := t.TempDir()

	ghostTabTuiBody := `
if [ "$1" = "multi-select-ai-tool" ]; then
    echo '{"tools":["opencode","claude"],"confirmed":true}'
    exit 0
fi
exit 1
`
	jqBody := fmt.Sprintf(`#!/bin/bash
counter_file="%s/jq_calls"
count=$(cat "$counter_file" 2>/dev/null || echo 0)
count=$((count + 1))
echo "$count" > "$counter_file"

if [ "$2" = ".confirmed" ]; then
    echo "true"
elif [ "$2" = ".tools[]" ]; then
    printf "opencode\nclaude\n"
fi
exit 0
`, tmpDir)

	mockCommand(t, tmpDir, "ghost-tab-tui", ghostTabTuiBody)
	mockCommand(t, tmpDir, "jq", jqBody)

	script := fmt.Sprintf(`
set -euo pipefail
export PATH="%s/bin:$PATH"
export TEST_TMP="%s"

error() { echo "ERROR: $*" >&2; }

source %q

select_ai_tool_interactive
result=$?

echo "SELECTED_TOOL=${_selected_ai_tool:-}"
exit $result
`, tmpDir, tmpDir, projectRoot(t)+"/lib/ai-select-tui.sh")

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	// Should pick claude despite opencode being first in list
	assertContains(t, out, "SELECTED_TOOL=claude")
}

func TestAiSelect_PicksOpencodeWhenClaudeNotSelected(t *testing.T) {
	tmpDir := t.TempDir()

	ghostTabTuiBody := `
if [ "$1" = "multi-select-ai-tool" ]; then
    echo '{"tools":["opencode"],"confirmed":true}'
    exit 0
fi
exit 1
`
	jqBody := fmt.Sprintf(`#!/bin/bash
counter_file="%s/jq_calls"
count=$(cat "$counter_file" 2>/dev/null || echo 0)
count=$((count + 1))
echo "$count" > "$counter_file"

if [ "$2" = ".confirmed" ]; then
    echo "true"
elif [ "$2" = ".tools[]" ]; then
    printf "opencode\n"
fi
exit 0
`, tmpDir)

	mockCommand(t, tmpDir, "ghost-tab-tui", ghostTabTuiBody)
	mockCommand(t, tmpDir, "jq", jqBody)

	script := fmt.Sprintf(`
set -euo pipefail
export PATH="%s/bin:$PATH"
export TEST_TMP="%s"

error() { echo "ERROR: $*" >&2; }

source %q

select_ai_tool_interactive
result=$?

echo "SELECTED_TOOL=${_selected_ai_tool:-}"
exit $result
`, tmpDir, tmpDir, projectRoot(t)+"/lib/ai-select-tui.sh")

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "SELECTED_TOOL=opencode")
}

func TestAiSelect_ReturnsFailureWhenCancelled(t *testing.T) {
	tmpDir := t.TempDir()

	ghostTabTuiBody := `
if [ "$1" = "multi-select-ai-tool" ]; then
    echo '{"confirmed":false}'
    exit 0
fi
exit 1
`
	jqBody := `
if [ "$2" = ".confirmed" ]; then
    echo "false"
fi
exit 0
`
	mockCommand(t, tmpDir, "ghost-tab-tui", ghostTabTuiBody)
	mockCommand(t, tmpDir, "jq", jqBody)

	script := fmt.Sprintf(`
export PATH="%s/bin:$PATH"

error() { echo "ERROR: $*" >&2; }

source %q

select_ai_tool_interactive
`, tmpDir, projectRoot(t)+"/lib/ai-select-tui.sh")

	_, code := runBashSnippet(t, script, nil)
	if code == 0 {
		t.Error("expected non-zero exit code when cancelled, got 0")
	}
}

func TestAiSelect_HandlesBinaryMissing(t *testing.T) {
	tmpDir := t.TempDir()

	// Don't create a ghost-tab-tui mock - simulate it being missing
	// We need PATH to NOT contain ghost-tab-tui, so we override PATH to only contain essentials
	script := fmt.Sprintf(`
export PATH="%s/bin:/usr/bin:/bin"

error() { echo "ERROR: $*" >&2; }

source %q

select_ai_tool_interactive
`, tmpDir, projectRoot(t)+"/lib/ai-select-tui.sh")

	out, code := runBashSnippet(t, script, nil)
	if code == 0 {
		t.Error("expected non-zero exit code when binary missing, got 0")
	}
	assertContains(t, out, "ghost-tab-tui binary not found")
}

func TestAiSelect_HandlesJqParseFailureForConfirmed(t *testing.T) {
	tmpDir := t.TempDir()

	ghostTabTuiBody := `
echo '{"tools":["claude"],"confirmed":true}'
exit 0
`
	// jq always fails
	jqBody := `exit 1`

	mockCommand(t, tmpDir, "ghost-tab-tui", ghostTabTuiBody)
	mockCommand(t, tmpDir, "jq", jqBody)

	script := fmt.Sprintf(`
export PATH="%s/bin:$PATH"

error() { echo "ERROR: $*" >&2; }

source %q

select_ai_tool_interactive
`, tmpDir, projectRoot(t)+"/lib/ai-select-tui.sh")

	out, code := runBashSnippet(t, script, nil)
	if code == 0 {
		t.Error("expected non-zero exit code when jq parse fails, got 0")
	}
	assertContains(t, out, "Failed to parse AI tool selection response")
}

func TestAiSelect_HandlesJqParseFailureForTools(t *testing.T) {
	tmpDir := t.TempDir()

	ghostTabTuiBody := `
echo '{"tools":["claude"],"confirmed":true}'
exit 0
`
	// jq: first call succeeds (returns "true"), second fails
	jqBody := fmt.Sprintf(`#!/bin/bash
counter_file="%s/jq_calls"
count=$(cat "$counter_file" 2>/dev/null || echo 0)
count=$((count + 1))
echo "$count" > "$counter_file"

if [ "$count" -eq 1 ]; then
    echo "true"
    exit 0
fi
exit 1
`, tmpDir)

	mockCommand(t, tmpDir, "ghost-tab-tui", ghostTabTuiBody)
	mockCommand(t, tmpDir, "jq", jqBody)

	script := fmt.Sprintf(`
export PATH="%s/bin:$PATH"
export TEST_TMP="%s"

error() { echo "ERROR: $*" >&2; }

source %q

select_ai_tool_interactive
`, tmpDir, tmpDir, projectRoot(t)+"/lib/ai-select-tui.sh")

	out, code := runBashSnippet(t, script, nil)
	if code == 0 {
		t.Error("expected non-zero exit code when jq tools parse fails, got 0")
	}
	assertContains(t, out, "Failed to parse selected tools")
}

func TestAiSelect_ValidatesAgainstNullConfirmed(t *testing.T) {
	tmpDir := t.TempDir()

	ghostTabTuiBody := `
echo '{"confirmed":"null"}'
exit 0
`
	jqBody := `
if [ "$2" = ".confirmed" ]; then
    echo "null"
fi
exit 0
`
	mockCommand(t, tmpDir, "ghost-tab-tui", ghostTabTuiBody)
	mockCommand(t, tmpDir, "jq", jqBody)

	script := fmt.Sprintf(`
export PATH="%s/bin:$PATH"

error() { echo "ERROR: $*" >&2; }

source %q

select_ai_tool_interactive
`, tmpDir, projectRoot(t)+"/lib/ai-select-tui.sh")

	out, code := runBashSnippet(t, script, nil)
	if code == 0 {
		t.Error("expected non-zero exit code for null confirmed, got 0")
	}
	assertContains(t, out, "TUI returned invalid confirmation status")
}

func TestAiSelect_ValidatesAgainstEmptyTools(t *testing.T) {
	tmpDir := t.TempDir()

	ghostTabTuiBody := `
echo '{"tools":[],"confirmed":true}'
exit 0
`
	// jq: first call returns "true", second returns empty
	jqBody := fmt.Sprintf(`#!/bin/bash
counter_file="%s/jq_calls"
count=$(cat "$counter_file" 2>/dev/null || echo 0)
count=$((count + 1))
echo "$count" > "$counter_file"

if [ "$count" -eq 1 ]; then
    echo "true"
else
    echo ""
fi
exit 0
`, tmpDir)

	mockCommand(t, tmpDir, "ghost-tab-tui", ghostTabTuiBody)
	mockCommand(t, tmpDir, "jq", jqBody)

	script := fmt.Sprintf(`
export PATH="%s/bin:$PATH"
export TEST_TMP="%s"

error() { echo "ERROR: $*" >&2; }

source %q

select_ai_tool_interactive
`, tmpDir, tmpDir, projectRoot(t)+"/lib/ai-select-tui.sh")

	out, code := runBashSnippet(t, script, nil)
	if code == 0 {
		t.Error("expected non-zero exit code for empty tools, got 0")
	}
	assertContains(t, out, "TUI returned empty tool selection")
}

// --- Helper: verify no line in output matches prefix with wrong value ---
func assertLineEquals(t *testing.T, output string, key string, expected string) {
	t.Helper()
	prefix := key + "="
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, prefix) {
			got := strings.TrimPrefix(line, prefix)
			got = strings.TrimSpace(got)
			if got != expected {
				t.Errorf("expected %s=%s, got %s=%s", key, expected, key, got)
			}
			return
		}
	}
	t.Errorf("key %s not found in output:\n%s", key, output)
}
