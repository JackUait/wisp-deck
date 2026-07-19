# Claude Bridge Auth Token Design

## Problem

Claude Code 2.1.214 displays an interactive confirmation when an OpenAI GPT
subscription session starts:

```text
Detected a custom API key in your environment
Do you want to use this API key?
```

Wisp Deck causes the prompt by injecting the private loopback bridge secret as
both `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN`. Claude treats
`ANTHROPIC_API_KEY` as an explicit custom Anthropic API-billing choice, even
though the value is only an ephemeral credential for Wisp Deck's local bridge.

The confirmation is therefore not a login flow. It is a credential-semantics
mismatch at the Claude process boundary.

## Requirements

- OpenAI GPT subscription sessions must not show Claude's custom API-key
  confirmation.
- Claude requests must remain authenticated to the private loopback bridge.
- Inherited Anthropic routes and credentials must not leak into the bridge
  launch.
- Native Claude sessions and other configured providers must not change.
- The fix must not automate Claude's UI or persist a response in user
  configuration.

## Considered Approaches

### 1. Use only `ANTHROPIC_AUTH_TOKEN`

Strip inherited `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, and
`ANTHROPIC_BASE_URL`, then inject the bridge URL and secret only as
`ANTHROPIC_AUTH_TOKEN`.

This matches Claude's LLM-gateway credential path. Claude sends the token in
the Authorization header, which the local bridge already authenticates.

### 2. Persist Claude's "No" response

This suppresses the symptom for one Claude profile but mutates user state,
does not protect new profiles, and leaves the incorrect environment contract
in place.

### 3. Automate the confirmation prompt

Driving an interactive terminal prompt would be timing-sensitive,
terminal-dependent, and coupled to Claude's rendered UI.

## Decision

Use approach 1.

`BuildClaudeEnvironment` will continue treating `ANTHROPIC_API_KEY` as an
overridden key so any ambient value is removed. It will no longer append a
replacement `ANTHROPIC_API_KEY`. It will append exactly one
`ANTHROPIC_AUTH_TOKEN` containing the ephemeral bridge secret.

No server change is required. The bridge already accepts either `x-api-key`
or `Authorization: Bearer`, and its bearer-token behavior has direct test
coverage.

## Data Flow

1. Wisp Deck creates a random bridge secret and starts the authenticated
   loopback server.
2. The Claude child environment removes inherited Anthropic credentials and
   routes.
3. Wisp Deck injects:
   - `ANTHROPIC_BASE_URL=http://127.0.0.1:<ephemeral-port>`
   - `ANTHROPIC_AUTH_TOKEN=<ephemeral-bridge-secret>`
4. Claude sends the secret as a Bearer token.
5. The loopback bridge authenticates it and forwards translated work through
   the Codex ChatGPT-subscription app server.

Because `ANTHROPIC_API_KEY` is absent, Claude does not offer to use a custom
Anthropic API key.

## Testing

- Update environment unit tests to require:
  - exactly one bridge base URL;
  - exactly one bridge auth token;
  - no `ANTHROPIC_API_KEY`, including when one is inherited;
  - preserved unrelated environment and loopback proxy bypass.
- Update the adapter boundary test so its fake Claude child requires
  `ANTHROPIC_API_KEY` to be unset and verifies the private auth token.
- Run the bridge package and race suites.
- Run the gated live Claude/GPT subscription test to prove Bearer-token
  requests still reach the local bridge.
- Reproduce interactive startup with a temporary Claude config and confirm
  the custom API-key prompt is absent.
- Run `make install`, then verify the installed path, matching SHA-256, and
  valid code signature.

