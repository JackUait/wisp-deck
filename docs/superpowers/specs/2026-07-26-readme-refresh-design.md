# Wisp Deck README Refresh Design

**Date:** 2026-07-26

## Goal

Restructure `README.md` as a progressive introduction that explains Wisp Deck's value before its advanced features, adds the supplied ghost logo, and preserves every important verified operating detail without inventing claims.

## Scope

- Copy `/Users/jackuait/Downloads/Redesign Ghost Logo Jul 26.png` to `docs/wisp-deck-logo.png`.
- Replace the opening selector/session screenshot pair with the centered logo.
- Rewrite and reorder `README.md`.
- Keep the existing screenshot files unchanged and unreferenced.
- Do not change application code, scripts, configuration, or package metadata.

## Opening Block

The README will begin with:

1. A centered `docs/wisp-deck-logo.png` image, approximately 240 pixels wide, with descriptive alt text.
2. `# Wisp Deck`.
3. One conversational paragraph that states:
   - what Wisp Deck does: launches a managed macOS workspace for AI coding tools;
   - the user benefit: the AI assistant, live git changes, and spare shell are ready together;
   - the differentiator: Wisp Deck manages the complete tmux workspace, restoration, and cleanup rather than only launching an AI CLI.
4. A short fact list led by bold labels: one command, three panes, three supported AI tools, live changes, session restore, and clean shutdown.
5. A compact shell example showing `npx wisp-deck`, first-run choices, and the resulting workspace.
6. A table of contents because the README is longer than two screens.

The opening will contain no badges, benchmarks, adoption numbers, or unsupported compatibility claims.

## Information Architecture

The remaining content will follow the reader's likely journey:

1. Getting started
2. The workspace
3. Project selector
4. Settings
5. Claude accounts and subscriptions
6. Stats
7. Screenshots, videos, and file input
8. Status line
9. Hotkeys
10. Session restore
11. Updating
12. Credits

## Content Rules

- Preserve verified capabilities already documented in the repository.
- Convert dense paragraphs into short paragraphs or scan-friendly lists with bold lead-ins.
- Keep exact operational constraints prominent:
  - macOS and Node.js 16+ requirements;
  - Ghostty is the required terminal and may need manual installation;
  - closing a Wisp Deck window force-stops its session processes;
  - usage history is local-only;
  - the OpenAI GPT path accepts Codex-managed ChatGPT authentication and rejects API-key authentication;
  - restore behavior must not imply recovery guarantees beyond those already documented.
- Retain the selector key reference, session hotkeys, status-line explanation, account switching, subscription setup, stats storage details, media drop flow, and update command.
- Do not introduce facts that are absent from the repository or supplied by the user.

## Accessibility and Formatting

- Give the logo meaningful alt text.
- Keep command examples in fenced `sh` blocks.
- Keep descriptive table headers.
- Use headings, lists, bold lead-ins, horizontal rules, and callouts so headings plus emphasized text still communicate the story.
- Avoid decorative badges and excessive prose.

## Verification

Because the implementation is prose plus one image asset and does not change behavior, it does not require a new behavior test. Verification will:

1. Confirm `docs/wisp-deck-logo.png` exists and is referenced from `README.md`.
2. Review all README links, file paths, commands, requirements, and feature claims against repository sources.
3. Run `git diff --check`.
4. Run the repository's required full verification suite before completion.
5. Run shellcheck only if a shell script is modified; it is not applicable to the planned README/image-only change.
6. Commit and push the completed change as required by repository instructions.

## Files

- Add: `docs/wisp-deck-logo.png`
- Modify: `README.md`
- Add: `docs/superpowers/specs/2026-07-26-readme-refresh-design.md`
- Add later during planning: `docs/superpowers/plans/2026-07-26-readme-refresh.md`
