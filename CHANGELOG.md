# Changelog

## Unreleased

### Added

- Added an **OpenAI GPT** Claude Code subscription backed by the user's existing
  `codex login` ChatGPT authentication. Claude Code remains the interface,
  permission boundary, and tool executor; Wisp Deck does not read or persist
  Codex credentials and rejects OpenAI API-key authentication for this path.
