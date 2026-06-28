#!/bin/bash
# Claude Code subagentStatusLine entrypoint (referenced by settings.json).
#
# Reads the subagent panel JSON on stdin and emits one {"id","content"} override
# line per visible subagent, so the row for whichever subagent the user switches
# to shows that subagent's own info (name when set, description, token count).
#
# The renderer lives in subagent-statusline.sh / subagent-statusline-helpers.sh;
# source it from the repo checkout first, then the installed copy in ~/.claude.
# shellcheck source=../lib/subagent-statusline.sh
source "$(dirname "$0")/../lib/subagent-statusline.sh" 2>/dev/null \
  || source ~/.claude/subagent-statusline-helpers.sh 2>/dev/null \
  || true

if type render_subagent_rows &>/dev/null; then
  render_subagent_rows
fi
