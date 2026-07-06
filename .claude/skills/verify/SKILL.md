---
name: verify
description: Drive wisp-deck's bash account-switch/draft helpers against a REAL claude pane on an isolated tmux server, instead of the mocked go tests — use when verifying changes to lib/account-switch.sh, draft stash/replay, or anything that reads claude's live pane frames.
---

# Verifying against a live claude pane

The go tests mock tmux and claude; real claude's frames differ in ways the
mocks can't catch (placeholder text, U+00A0 padding, streaming echoes). To
observe the real surface:

```bash
# Isolated tmux server — NEVER the user's live server
printf '#!/bin/bash\nexec tmux -L wispverify "$@"\n' > /tmp/tmuxw && chmod +x /tmp/tmuxw
W=/tmp/tmuxw
"$W" new-session -d -s verify -x 100 -y 30 -c /Users/jackuait/Packages/wisp-deck
"$W" send-keys -t verify "$HOME/.local/bin/claude" Enter   # real binary, alias adds flags
# poll capture-pane for a line starting with ❯ → ready (takes a few seconds)

# Call the bash functions exactly as the ledger does, passing the wrapper as tmux_cmd:
bash -c 'source lib/statusline.sh; source lib/claude-accounts.sh
  source lib/tmux-session.sh; source lib/account-switch.sh
  stash_ai_draft /tmp/tmuxw verify "$HOME/.claude/history.jsonl" "$PWD"'

# Baseline against the pre-change lib: git show HEAD~1:lib/account-switch.sh > old.sh; source old.sh
# Cleanup: "$W" kill-server
```

Gotchas learned from real frames (2026-07, claude 2.1.x):

- **Empty idle input** renders as a bare `❯` (sometimes + space/U+00A0) as the
  LAST ❯-prefixed row; sent messages echo in the transcript as `❯ <text>`
  rows ABOVE it — never key off "any ❯ line".
- **Fresh session** may briefly show a placeholder (`❯ Try "fix lint errors"`)
  in the first frame only; it's gone within a second.
- **While streaming**, the empty input box does NOT render a bare ❯ row — the
  last ❯ row is the just-sent message's echo, so empty-input detection can't
  fire mid-stream (falls back to Esc-Esc, which interrupts the stream and
  stashes the in-flight message as the draft — intended).
- Typing a draft: `send-keys -t <pane> -l "text"`; clear input with `C-u`.
- Launch claude with cwd = this repo (already trusted); an untrusted scratch
  dir stalls on the trust dialog.
