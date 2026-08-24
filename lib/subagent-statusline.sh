#!/bin/bash
# Subagent status line helpers — pure, no side effects on source.
#
# Claude Code's `subagentStatusLine` command runs once per refresh tick with all
# visible subagent rows passed as ONE JSON object on stdin: the base hook fields
# plus `columns` (usable row width) and a `tasks` array, where each task has
# `id`, `name`, `type`, `status`, `description`, `label`, `startTime`, `model`,
# `effort`, `contextWindowSize`, `tokenCount`, `tokenSamples`, and `cwd`. The
# command must write one JSON line per row it wants to override, of the form
# {"id":"<task id>","content":"<body>"}.
#
# `model` and `contextWindowSize` are the per-agent figures the bottom status
# line CANNOT show: that one is computed from the main conversation's messages
# and is not even re-run when the user switches agents, so this row is the only
# place a subagent's own model and context fill can appear. Spend them.
#
# render_subagent_rows turns each task into such an override line, so the row for
# whichever subagent the user is on shows that subagent's own distinguishing
# info: its name (only when set), description, used context, model and a compact
# token count. Context and model are presented exactly as the main status line
# presents them — same glyph, same colors, same "45.0%" and "Fable 5 [xhigh]"
# shapes — so a glance between the bar and the panel needs no translation. The
# status word and the internal type are deliberately omitted: they read the same
# on every active row and crowd out the description. Reads stdin, writes the
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

    def is_digits($s): ($s | length) > 0
      and ($s | explode | all(. >= 48 and . <= 57));
    def cap($w): if $w == "" then "" else (($w[0:1] | ascii_upcase) + $w[1:]) end;

    # The context glyph the status bar prints (U+F09D1), so the row wears it too.
    def ctxglyph: ([985553] | implode);

    # Name a model the way the status bar does. That bar prints the
    # `display_name` plus the reasoning effort in brackets ("Fable 5 [xhigh]").
    # Tasks carry only the raw id, so rebuild that name from it —
    # "claude-opus-4-5-20260101" → "Opus 4.5". Split on "-" rather than matched
    # with a regex so the program still runs on a jq built without oniguruma; a
    # `sub` there would throw and blank out EVERY row, not just this one.
    def display_model($m; $effort):
      if $m == null or $m == "" then ""
      # Only Anthropic ids carry a friendly display_name; every other provider
      # id reaches the bar verbatim. Rebuilding a title-cased name here renders
      # "Gpt 5.6 Sol" beside a bar reading "gpt-5.6-sol" — a translation the
      # reader should never have to make. A dotted version defeats is_digits
      # regardless, so such an id cannot be split correctly anyway.
      # NOTE: no apostrophes in this jq program; it is single-quoted in bash.
      elif ($m | startswith("claude-") | not) then
        (if $effort != null and $effort != ""
         then $m + " [" + $effort + "]" else $m end)
      else
        ($m[7:] | split("-")) as $parts
        | ($parts | map(select(is_digits(.) | not)) | map(cap(.)) | join(" ")) as $words
        # An 8-digit run is the release date — a build stamp, not a version.
        | ($parts | map(select(is_digits(.) and (length != 8))) | join(".")) as $vers
        | ([$words, $vers] | map(select(. != "")) | join(" ")) as $name
        | if $effort != null and $effort != ""
          then $name + " [" + $effort + "]" else $name end
      end;

    # Used context to one decimal, matching the "45.0%" the bar prints. Empty
    # when the window is unknown or zero — a task that has not picked a model
    # yet has none — which also guards the division, since dividing by zero
    # aborts the whole jq program.
    def fmt_pct($tok; $size):
      if $size == null or ($size | type) != "number" or $size <= 0 then ""
      else ((($tok // 0) * 1000 / $size) | floor) as $t
           | (($t / 10) | floor | tostring) + "." + (($t % 10) | tostring) + "%"
      end;

    (.columns // 80) as $cols
    | .tasks[]?
    # Only the real name labels the row — no type fallback, so unnamed local
    # agents render description-first instead of a repeated "local_agent".
    | (.name // "") as $name
    | (.description // "") as $desc
    | display_model(.model; .effort) as $model
    | fmt_pct(.tokenCount; .contextWindowSize) as $pct
    | fmt_tokens(.tokenCount) as $tok
    # Width spent by every segment other than the description (each joined by
    # " · ", the token segment carrying a trailing " tok", and the context
    # segment a double-width glyph plus its space), so the description can be
    # truncated to keep the row inside $cols (no wrapping). The stats are what
    # the row exists for, so the description is what yields to them.
    | ( (if $name != "" then ($name | length) + 3 else 0 end)
        + (if $pct != "" then ($pct | length) + 6 else 0 end)
        + (if $model != "" then ($model | length) + 3 else 0 end)
        + 3 + ($tok | length) + 4 ) as $fixed
    | ($cols - $fixed) as $budget
    | ( if $desc == "" then ""
        elif ($desc | length) <= $budget then $desc
        elif $budget <= 1 then ""
        else ($desc[0:($budget - 1)] + "…") end ) as $desc2
    # Join the present segments with a dim middot: name → description → used
    # context → model → tokens. Context before model mirrors the status bar,
    # and both wear the colors used by that bar (glyph 01;33, pct 38;5;178,
    # model 01;34) so the panel and the bar read as one system. Absent segments
    # drop out entirely rather than leaving an empty slot between separators.
    | { id: .id,
        content: ([
            (if $name != "" then paint("1;36"; $name) else empty end),
            (if $desc2 != "" then $desc2 else empty end),
            (if $pct != ""
             then paint("01;33"; ctxglyph) + " " + paint("1;38;5;178"; $pct)
             else empty end),
            (if $model != "" then paint("01;34"; $model) else empty end),
            paint("35"; $tok + " tok")
          ] | join(" " + paint("90"; "·") + " ")) }
  ' 2>/dev/null || true
}
