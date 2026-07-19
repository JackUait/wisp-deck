# ChatGPT Connector Warning Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Remove Claude Code's claude.ai connector warning from OpenAI / ChatGPT bridge sessions without changing native Claude or other providers.

**Architecture:** Encode `disableClaudeAiConnectors: true` in default and newly created ChatGPT provider profiles. For existing profiles, enforce the same provider-specific setting in Wisp Deck's private generation-local settings overlay, identified only by the trusted `WISP_DECK_SUBSCRIPTION_PROVIDER` marker; never mutate the source profile.

**Tech Stack:** Bash, embedded Python 3 JSON transformation, Go tests, Claude Code settings JSON, macOS code signing

---

### Task 1: Encode the ChatGPT Provider Constraint

**Files:**
- Modify: `test/npx/distribution_test.go`
- Modify: `internal/claudeconfig/claudeconfig_test.go`
- Modify: `defaults/claude-configs/openai-gpt.json`
- Modify: `internal/claudeconfig/claudeconfig.go`

**Step 1: Write the failing default-profile test**

Extend the local settings fixture in
`TestDefaults_include_keyless_OpenAI_GPT_subscription`:

```go
var settings struct {
	Model                     string            `json:"model"`
	DisableClaudeAiConnectors bool              `json:"disableClaudeAiConnectors"`
	Env                       map[string]string `json:"env"`
}
```

Then add:

```go
if !settings.DisableClaudeAiConnectors {
	t.Error("OpenAI GPT default must disable unavailable claude.ai connectors")
}
```

**Step 2: Write the failing generated-profile test**

Extend the settings fixture in
`TestAddForProvider_writesInitializedProfile` with the same boolean field.
After unmarshalling, assert that only the ChatGPT-authenticated provider has
the setting:

```go
wantDisableConnectors := provider.Auth == AuthCodexChatGPT
if settings.DisableClaudeAiConnectors != wantDisableConnectors {
	t.Errorf("disableClaudeAiConnectors = %v, want %v",
		settings.DisableClaudeAiConnectors, wantDisableConnectors)
}
```

**Step 3: Run the focused tests to verify RED**

Run:

```bash
go test ./internal/claudeconfig ./test/npx \
  -run 'Test(AddForProvider_writesInitializedProfile|Defaults_include_keyless_OpenAI_GPT_subscription)' \
  -count=1 -v
```

Expected: both ChatGPT assertions fail because the setting is absent.

**Step 4: Update the shipped default profile**

Add the top-level setting to `defaults/claude-configs/openai-gpt.json`:

```json
"disableClaudeAiConnectors": true
```

Keep it outside `env`, adjacent to `model`.

**Step 5: Update generated ChatGPT profiles**

In `AddForProvider`, add the setting only inside the existing ChatGPT branch:

```go
if provider.Auth == AuthCodexChatGPT {
	settings["model"] = provider.DefaultModels[1]
	settings["disableClaudeAiConnectors"] = true
}
```

Do not add it to API-key providers.

**Step 6: Format and verify GREEN**

Run:

```bash
gofmt -w internal/claudeconfig/claudeconfig.go \
  internal/claudeconfig/claudeconfig_test.go \
  test/npx/distribution_test.go
go test ./internal/claudeconfig ./test/npx \
  -run 'Test(AddForProvider_writesInitializedProfile|Defaults_include_keyless_OpenAI_GPT_subscription)' \
  -count=1 -v
```

Expected: PASS.

**Step 7: Commit**

Commit only:

```text
defaults/claude-configs/openai-gpt.json
internal/claudeconfig/claudeconfig.go
internal/claudeconfig/claudeconfig_test.go
test/npx/distribution_test.go
```

Commit message:

```text
fix(config): disable Claude connectors for ChatGPT
```

### Task 2: Cover Existing Profiles at the Launch Boundary

**Files:**
- Modify: `test/bash/claude_launch_settings_test.go`
- Modify: `lib/settings-json.sh`

**Step 1: Write the failing legacy-profile test**

Add a test that writes an existing profile without the new top-level setting:

```json
{
  "model": "gpt-5.6-terra",
  "env": {
    "WISP_DECK_SUBSCRIPTION_PROVIDER": "openai-chatgpt"
  }
}
```

Call `write_claude_launch_settings`, then assert:

```go
if got["disableClaudeAiConnectors"] != true {
	t.Fatalf("disableClaudeAiConnectors = %#v, want true",
		got["disableClaudeAiConnectors"])
}
```

Read the source before and after and require byte-for-byte equality.

**Step 2: Write the failing provider-isolation test**

Create a profile with another trusted provider and an explicit false value:

```json
{
  "disableClaudeAiConnectors": false,
  "env": {
    "WISP_DECK_SUBSCRIPTION_PROVIDER": "mimo"
  }
}
```

Generate the overlay and assert the false value is preserved. Also retain the
existing no-profile test requiring only the notification override.

**Step 3: Run the focused tests to verify RED**

Run:

```bash
go test ./test/bash \
  -run 'TestSettingsJsonClaudeLaunchSettings' \
  -count=1 -v
```

Expected: the legacy ChatGPT profile test fails because the overlay does not
yet add `disableClaudeAiConnectors`.

**Step 4: Implement provider-aware overlay generation**

Inside the embedded Python in `write_claude_launch_settings`, after validating
the source is a JSON object and before writing the output, add:

```python
env = settings.get("env")
if (
    isinstance(env, dict)
    and env.get("WISP_DECK_SUBSCRIPTION_PROVIDER") == "openai-chatgpt"
):
    settings["disableClaudeAiConnectors"] = True
```

Keep the existing notification override. Do not filter output, mutate the
source, or infer provider identity from filenames or model names.

**Step 5: Run focused tests to verify GREEN**

Run:

```bash
go test ./test/bash \
  -run 'TestSettingsJsonClaudeLaunchSettings' \
  -count=1 -v
```

Expected: PASS.

**Step 6: Run shell-facing configuration tests**

Run:

```bash
go test ./test/bash \
  -run 'Test(SettingsJsonClaudeLaunchSettings|ClaudeGPTAdapterLaunch|ClaudeConfigs)' \
  -count=1 -v
```

Expected: PASS.

**Step 7: Commit**

Commit only:

```text
lib/settings-json.sh
test/bash/claude_launch_settings_test.go
```

Commit message:

```text
fix(launch): suppress unavailable connector warning
```

### Task 3: Verify the Integrated Launch and Install

**Files:**
- No source changes expected.

**Step 1: Run affected package tests**

Run:

```bash
go test ./internal/claudeconfig ./internal/gptbridge ./test/npx -count=1
go test ./test/bash \
  -run 'Test(SettingsJsonClaudeLaunchSettings|ClaudeGPTAdapterLaunch|LiveGPTSubscriptionInClaude)' \
  -count=1
```

Expected: PASS; the live test is skipped unless its opt-in variable is set.

**Step 2: Run the opted-in live bridge test**

Run:

```bash
WISP_DECK_LIVE_GPT_CLAUDE_E2E=1 \
  go test ./test/bash \
  -run TestLiveGPTSubscriptionInClaude \
  -count=1 -v
```

Expected: PASS with a completed Claude tool continuation through the ChatGPT
subscription bridge.

**Step 3: Install the local binary**

Run:

```bash
make install
```

Expected: build, signing, and installation succeed.

**Step 4: Verify the installed artifact**

Run:

```bash
test "$(command -v wisp-deck-tui)" = "$HOME/.local/bin/wisp-deck-tui"
test "$(shasum -a 256 bin/wisp-deck-tui | awk '{print $1}')" = \
     "$(shasum -a 256 "$HOME/.local/bin/wisp-deck-tui" | awk '{print $1}')"
codesign --verify --verbose=2 "$HOME/.local/bin/wisp-deck-tui"
```

Expected: the path check and hash comparison return zero, and `codesign`
reports a valid signature.

**Step 5: Verify an installed warning-free bridge launch**

Launch the installed adapter with the active OpenAI / ChatGPT profile through
Wisp Deck's generated settings overlay and request an exact marker.

Expected:

- the marker is returned;
- the custom API-key prompt is absent;
- the claude.ai connector warning is absent;
- Claude local/project MCP behavior remains unchanged.

**Step 6: Inspect the final diff**

Run:

```bash
git diff --check HEAD^
git status --short
```

Expected: no whitespace errors; only pre-existing unrelated user changes may
remain.
