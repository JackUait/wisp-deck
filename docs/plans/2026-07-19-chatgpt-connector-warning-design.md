# ChatGPT Connector Warning Design

## Problem

Claude Code 2.1.214 shows this warning when Wisp Deck launches Claude through
the OpenAI / ChatGPT subscription bridge:

```text
claude.ai connectors are disabled because ANTHROPIC_API_KEY or another auth
source is set and takes precedence over your claude.ai login
```

The bridge is healthy. Wisp Deck must supply `ANTHROPIC_AUTH_TOKEN` so Claude
can authenticate to its private loopback gateway, and that alternate
authentication necessarily prevents claude.ai account-synced connectors from
loading. Claude warns because the selected provider profile does not explicitly
acknowledge that those connectors are unavailable.

This is a provider-configuration mismatch, not an API failure.

## Requirements

- OpenAI / ChatGPT sessions must not show the claude.ai connector warning.
- Only claude.ai account-synced connectors may be disabled.
- Native Claude sessions and other subscription providers must remain
  unchanged.
- Existing OpenAI / ChatGPT profiles must receive the fix without rewriting
  user-owned settings files.
- New and default OpenAI / ChatGPT profiles must describe the provider
  constraint directly.
- Local and project MCP servers must remain available.
- The implementation must not filter terminal output or depend on an
  undocumented Claude environment variable.

## Considered Approaches

### 1. Provider settings plus launch-overlay compatibility

Set `disableClaudeAiConnectors: true` in new and default OpenAI / ChatGPT
profiles. When Wisp Deck builds its private generation-local Claude settings
overlay, detect the trusted `openai-chatgpt` provider marker and enforce the
same setting in the overlay.

This makes new profiles self-describing while covering existing profiles
without modifying them.

### 2. Launch overlay only

Enforce the setting only in the generation-local overlay.

This is the smallest production change and covers existing profiles, but the
shipped default and newly created provider profiles remain incomplete when
inspected or used directly.

### 3. Adapter environment flag or output filtering

An internal Claude feature flag may suppress connector loading, and the
adapter could alternatively remove the warning from terminal output.

The feature flag is undocumented and may change. Output filtering is coupled
to rendered warning text and hides a symptom instead of declaring the
provider's capability.

## Decision

Use approach 1.

The OpenAI / ChatGPT provider will explicitly disable claude.ai account-synced
connectors because they cannot authenticate while the loopback gateway token
is active. Wisp Deck will enforce the setting only when the source profile has
the trusted `WISP_DECK_SUBSCRIPTION_PROVIDER` value `openai-chatgpt`.

The source profile remains untouched. The effective setting is written into
the existing private `claude-settings.json` overlay for that attention
generation. Native Claude and all other providers retain their original
connector behavior.

## Data Flow

1. Wisp Deck resolves the active Claude settings profile.
2. The trusted provider marker is read from
   `env.WISP_DECK_SUBSCRIPTION_PROVIDER`.
3. Wisp Deck copies the source settings into a private, mode-`0600`,
   generation-local overlay.
4. It applies the existing notification override.
5. If and only if the provider is `openai-chatgpt`, it also sets
   `disableClaudeAiConnectors` to `true`.
6. Claude launches with that overlay and skips initializing claude.ai
   account-synced connectors, so the alternate-auth warning is unnecessary.
7. Local and project MCP configuration continues through Claude's normal MCP
   paths.

The same overlay builder is used for initial launches and agent/provider
relaunches, so the behavior remains consistent across switches.

## Error Handling

The existing overlay guarantees remain in force:

- Invalid or non-object source JSON fails before launch.
- The source settings file is never mutated.
- A temporary mode-`0600` file is atomically renamed over the generated
  target.
- Failed generation removes the temporary file and preserves any prior atomic
  target.

Unknown, missing, or malformed provider markers do not disable connectors.

## Testing

- Extend the default-distribution test to require
  `disableClaudeAiConnectors: true` in `openai-gpt.json`.
- Extend provider-profile tests to require the setting for
  `openai-chatgpt` and ensure it is absent for other providers.
- Add launch-overlay tests proving:
  - a legacy ChatGPT profile receives the effective setting;
  - the source profile remains byte-for-byte unchanged;
  - native Claude and other providers do not receive the override;
  - an explicit non-ChatGPT value is preserved.
- Run affected Go and Bash suites.
- Run an installed live ChatGPT-bridge request and confirm it completes
  without the connector warning.
- Run `make install`, then verify the installed path, SHA-256 equality, and
  code signature.
