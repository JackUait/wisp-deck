#!/bin/bash
# Subagent status line helpers — pure, no side effects on source.
#
# Claude Code's `subagentStatusLine` command runs once per refresh tick with all
# visible subagent rows passed as ONE JSON object on stdin: the base hook fields
# plus `columns` (usable row width) and a `tasks` array, where each task has
# `id`, `name`, `type`, `status`, `description`, `label`, `startTime`,
# `tokenCount`, `tokenSamples`, and `cwd`. The command must write one JSON line
# per row it wants to override, of the form {"id":"<task id>","content":"<body>"}.
#
# render_subagent_rows turns each task into such an override line, so the row for
# whichever subagent the user is on shows that subagent's own distinguishing
# info: its name (only when set), description, and a compact token count. The
# status word and the internal type are deliberately omitted — they read the
# same on every active row and crowd out the description. Reads stdin, writes the
# override lines to stdout.
render_subagent_rows() {
  # Without jq the tasks array can't be parsed safely; stay silent so Claude
  # keeps its default `name · description · token count` rendering.
  command -v jq >/dev/null 2>&1 || return 0

  # A single jq program builds every override line. jq owns the JSON escaping of
  # `content`, so the emitted lines are always valid. The ESC byte for ANSI
  # colors is produced with `[27]|implode` to keep raw control characters out of
  # this source file. Invalid/empty input degrades to no output (|| true) rather
  # than erroring the whole agent panel.
  jq -c '
    def esc: ([27] | implode);
    # Wrap text in an ANSI SGR color so each row reads at a glance.
    def paint($code; $text): esc + "[" + $code + "m" + $text + esc + "[0m";

    # Compact a token count: 0 when absent, "N.Nk" once it reaches 1000.
    def fmt_tokens($n):
      if $n == null then "0"
      elif $n >= 1000 then (((($n / 100) | floor) / 10) | tostring) + "k"
      else ($n | tostring) end;

    (.columns // 80) as $cols
    | .tasks[]?
    # Only the real name labels the row — no type fallback, so unnamed local
    # agents render description-first instead of a repeated "local_agent".
    | (.name // "") as $name
    | (.description // "") as $desc
    | fmt_tokens(.tokenCount) as $tok
    # Width spent by the name and token segments (each joined by " · "), so the
    # description can be truncated to keep the row inside $cols (no wrapping).
    | ( (if $name != "" then ($name | length) + 3 else 0 end)
        + 3 + ($tok | length) + 4 ) as $fixed
    | ($cols - $fixed) as $budget
    | ( if $desc == "" then ""
        elif ($desc | length) <= $budget then $desc
        elif $budget <= 1 then ""
        else ($desc[0:($budget - 1)] + "…") end ) as $desc2
    # Join the present segments with a dim middot, name → description → tokens.
    | { id: .id,
        content: ([
            (if $name != "" then paint("1;36"; $name) else empty end),
            (if $desc2 != "" then $desc2 else empty end),
            paint("35"; $tok + " tok")
          ] | join(" " + paint("90"; "·") + " ")) }
  ' 2>/dev/null || true
}
