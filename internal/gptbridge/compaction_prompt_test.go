package gptbridge

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Claude Code 2.1.263 builds every summarization prompt as preamble + template
// + reminder (`oPn` and `B0e` in the bundle). Reproduced verbatim so the guard
// fails on the shape the client actually sends, not on the bare template.
const claudeCompactionPreamble = `CRITICAL: Respond with TEXT ONLY. Do NOT call any tools.

- Do NOT use Read, Bash, Grep, Glob, Edit, Write, or ANY other tool.
- You already have all the context you need in the conversation above.
- Tool calls will be REJECTED and will waste your only turn — you will fail the task.
- Your entire response must be plain text: an <analysis> block followed by a <summary> block.

`

const claudeCompactionReminder = `

REMINDER: Do NOT call any tools. Respond with plain text only — an <analysis> block followed by a <summary> block. Tool calls will be rejected and you will fail the task.`

// The bare templates, as they appear in the bundle.
var claudeCompactionTemplates = []string{
	"Your task is to create a detailed summary of the conversation so far, paying close attention to the user's explicit requests and your previous actions.",
	"Your task is to create a detailed summary of the RECENT portion of the conversation — the messages that follow earlier retained context.",
	"Your task is to create a detailed summary of this conversation. This summary will be placed at the start of a continuing session; newer messages that build on this context will follow after your summary (you do not see them here).",
}

const analysisTagAnchor = "Before providing your final summary, wrap your analysis in <analysis> tags to organize your thoughts and ensure you've covered all necessary points."

// A vendor preamble in front of the template must not disable the downgrade.
// It did: the check was anchored with HasPrefix, 2.1.263 prepended "CRITICAL:
// Respond with TEXT ONLY", and compaction silently began running at the
// session's full reasoning effort — measured as
// codex.turn.reasoning_effort=ultra on a real 14.5-minute compaction.
func TestTranslateRunsClaudeCompactionAtLowEffort_behindAVendorPreamble(t *testing.T) {
	for index, template := range claudeCompactionTemplates {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			prompt := claudeCompactionPreamble + template + " " +
				analysisTagAnchor + claudeCompactionReminder
			body := fmt.Sprintf(`{
				"model":"gpt-6-astra",
				"max_tokens":32000,
				"thinking":{"type":"enabled","budget_tokens":32768},
				"messages":[
					{"role":"user","content":"work on the feature"},
					{"role":"assistant","content":"done"},
					{"role":"user","content":%q}
				]
			}`, prompt)
			got := parseAndTranslate(t, body)
			if got.Effort != "low" {
				t.Fatalf("effort = %q, want low", got.Effort)
			}
		})
	}
}

// A trailing reminder is as fatal to a suffix check as a preamble is to a
// prefix one, so neither end may be anchored.
func TestTranslateRunsClaudeCompactionAtLowEffort_behindATrailingReminder(t *testing.T) {
	prompt := claudeCompactionTemplates[0] + " " + analysisTagAnchor + claudeCompactionReminder
	body := fmt.Sprintf(`{
		"model":"gpt-6-astra",
		"max_tokens":32000,
		"thinking":{"type":"enabled","budget_tokens":32768},
		"messages":[{"role":"user","content":%q}]
	}`, prompt)
	if got := parseAndTranslate(t, body); got.Effort != "low" {
		t.Fatalf("effort = %q, want low", got.Effort)
	}
}

// The downgrade reaches Codex only through turn/start, which Execute skips for
// a request carrying tool results. A compaction request is a plain user turn,
// so this pins the shape the effort actually travels on.
func TestTranslateClaudeCompactionCarriesNoToolResults(t *testing.T) {
	prompt := claudeCompactionPreamble + claudeCompactionTemplates[0] + " " + analysisTagAnchor
	body := fmt.Sprintf(`{
		"model":"gpt-6-astra",
		"max_tokens":32000,
		"thinking":{"type":"enabled","budget_tokens":32768},
		"messages":[
			{"role":"user","content":"work on the feature"},
			{"role":"assistant","content":"done"},
			{"role":"user","content":%q}
		]
	}`, prompt)
	got := parseAndTranslate(t, body)
	if len(got.ToolResults) != 0 {
		t.Fatalf("compaction request carries %d tool results, so the turn would resume instead of starting", len(got.ToolResults))
	}
	if len(got.Input) == 0 {
		t.Fatal("compaction prompt did not reach the turn input")
	}
}

// installedClaudeBundles returns every Claude Code binary on this machine.
func installedClaudeBundles(t *testing.T) []string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(home, ".local", "share", "claude", "versions"))
	if err != nil {
		return nil
	}
	var bundles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		bundles = append(bundles, filepath.Join(home, ".local", "share", "claude", "versions", entry.Name()))
	}
	return bundles
}

// The downgrade recognizes a vendor-controlled prompt, so a reworded template
// disables it silently — the session simply gets slower, and the cost is paid
// once per compaction. This reads the installed client and fails instead.
//
// It skips where no client is installed, which is every CI runner.
func TestClaudeCompactionTemplatesStillMatchTheInstalledClient(t *testing.T) {
	bundles := installedClaudeBundles(t)
	if len(bundles) == 0 {
		t.Skip("no Claude Code binary installed")
	}
	anchor := []byte("Before providing your final summary, wrap your analysis in <analysis> tags")
	for _, bundle := range bundles {
		t.Run(filepath.Base(bundle), func(t *testing.T) {
			data, err := os.ReadFile(bundle)
			if err != nil {
				t.Skipf("read %s: %v", bundle, err)
			}
			found := 0
			for offset := 0; ; {
				at := bytes.Index(data[offset:], anchor)
				if at < 0 {
					break
				}
				at += offset
				offset = at + len(anchor)
				// The template precedes the anchor in the same prompt; a
				// generous window keeps a longer preamble from hiding it.
				from := at - 4096
				if from < 0 {
					from = 0
				}
				window := data[from:at]
				matched := false
				for _, template := range claudeCompactionTemplates {
					if bytes.Contains(window, []byte(template)) {
						matched = true
						break
					}
				}
				if matched {
					found++
				}
			}
			if found == 0 {
				t.Errorf("%s builds a summarization prompt no template in isClaudeCompactionInput matches — compaction will run at full effort", filepath.Base(bundle))
			}
		})
	}
}
