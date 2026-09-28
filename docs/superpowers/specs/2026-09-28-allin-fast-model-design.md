# All-In: the router picks the fast model

## Problem

Claude Code sends its background calls (the session title, the startup quota
check, and per its docs things like WebFetch page summaries) to a "small fast"
model. With a custom `ANTHROPIC_BASE_URL` and no `ANTHROPIC_SMALL_FAST_MODEL`,
it uses the main model instead.

Measured on 2026-09-28 with Claude Code 2.1.283 and the live All-In overlay,
pointed at a capture server:

- without the key: the quota check and the title went to
  `wisp/acct.default/claude-opus-5-5`;
- with `ANTHROPIC_SMALL_FAST_MODEL` set: both went to that model; the main
  turns and the prompt suggestion stayed on the session model.

So every All-In session pays Opus rates for work Claude Code means to run on
Haiku.

## Goal

Background calls run on the fast model of the source the session is using
right now — for every source: Claude accounts, API-key providers, the ChatGPT
subscription. They never move to another source, so they never bill a
subscription the session did not pick.

## Design

### A marker instead of a model

`routerEnv` sets `ANTHROPIC_SMALL_FAST_MODEL=wisp/fast`. A static id cannot
work: the env is fixed at launch, and the session's source changes with
`/model`.

### Route

`Route("wisp/fast")` returns a new kind, `KindFast`. It carries no source.

### The router remembers the session's row

One router serves one pane. The handler keeps the last row it saw on a
non-fast POST (under a mutex). A `KindFast` request is then turned into a
normal target on that row's source, with the fast model swapped in:

| last row | fast target |
|---|---|
| `wisp/acct.<login>/…` | `wisp/acct.<login>/claude-haiku-4-5-20251001` |
| `wisp/cfg.<profile>/…` | `wisp/cfg.<profile>/<its ANTHROPIC_DEFAULT_HAIKU_MODEL>` |
| a `cfg` profile with no haiku mapping | the last row's own model |
| a bare id (the session's own login) | `claude-haiku-4-5-20251001` on the session login |

The `[1m]` marker is never carried to the fast target.

### Before the first main request

The title is requested before the first main turn, so the router has not seen
a row yet. It then uses the `model` key of `$CLAUDE_CONFIG_DIR/settings.json`,
or `~/.claude/settings.json` when that variable is unset. Every account folder
links its `settings.json` to `~/.claude/settings.json`, so both name the same
file. It does not parse `--model` from the child command: in a live pane that
command is one `bash -c` string. If no row is found, it uses the bare Haiku id,
which goes to the session's own login.

### Errors

A fast target is resolved like any other target, so a stale login or an
unready profile fails the same way (400). The fast lookup itself never fails:
every branch ends in a model.

## Testing

- `Route` maps `wisp/fast` to `KindFast`.
- The handler routes a fast request to the right source and model for each
  row kind, and follows a switch to another row.
- Before any main request, it uses the starting model, then the bare Haiku id.
- `routerEnv` carries the key, and an existing profile gains it on refresh.
- Live check: repeat the capture run and confirm the title goes to the fast
  model of the session's source.

## Not in scope

The prompt-suggestion call stays on the session model: Claude Code sends it
there with or without the key.
