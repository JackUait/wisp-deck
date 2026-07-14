# Repository Agent Instructions

## Local Installation

After finishing any work in this repository, always update the local
installation before the final handoff:

```bash
make install
```

Verify that `command -v wisp-deck-tui` resolves to
`~/.local/bin/wisp-deck-tui`, the installed binary's SHA-256 matches
`bin/wisp-deck-tui`, and its code signature is valid. Mention when a running
ledger pane or session must be relaunched to pick up the new binary.
