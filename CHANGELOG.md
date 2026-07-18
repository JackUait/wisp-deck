# Changelog

## Unreleased

### Added

- Added a responsive **Subscriptions** overlay in Settings. It inventories
  Standard Claude and every configured profile, exposes all available
  providers, authentication status, endpoints, and Opus/Sonnet/Haiku/Fable
  routing, and supports explicit use/save plus add, rename, and delete actions.
- Added an **OpenAI GPT** Claude Code subscription backed by the user's existing
  `codex login` ChatGPT authentication. Claude Code remains the interface,
  permission boundary, and tool executor; Wisp Deck does not read or persist
  Codex credentials and rejects OpenAI API-key authentication for this path.
