# Changelog

## Unreleased

### Added

- Added a responsive **Subscriptions** overlay in Settings. It inventories
  Standard Claude and every configured profile, exposes all available
  providers, authentication status, endpoints, and Opus/Sonnet/Haiku/Fable
  routing, and supports explicit use/save plus add, rename, and delete actions.
- Added an **OpenAI GPT** Claude Code subscription backed by the user's existing
  Codex-managed ChatGPT authentication. The subscription modal shows live login
  state and a persistent **Sign in / switch account** action. Activating the
  profile while signed out opens the ChatGPT browser login automatically, waits
  for completion, and continues into Claude Code. Claude remains the interface,
  permission boundary, and tool executor; Wisp Deck does not read or persist
  Codex credentials and rejects OpenAI API-key authentication for this path.
