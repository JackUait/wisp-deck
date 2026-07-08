# Agent-Spawned Process CPU/RAM Metrics — Design

Date: 2026-07-08
Status: approved (autonomous goal session)

## Problem

The statusline already measures the **whole** Claude session tree (claude + every
descendant) via `get_tree_footprint_kb` / `get_tree_rss_kb` / `get_tree_cpu_pct`.
That number cannot answer "how much are the processes the agent *started*
costing me?" — the agent's own runtime dominates it. Goal: also measure the
CPU/RAM of just the agent-spawned processes.

## Definition

"Processes the agent starts" = all descendants of the `claude` process,
**excluding**:

- the `claude` process itself, and
- the statusline's own measurement subtree (the statusline-wrapper bash and its
  children — `statusline-command.sh`, `npx ccstatusline`, `ps`/`pgrep` calls —
  are wisp-deck overhead, not agent work; they run as claude children and would
  otherwise pollute the reading whenever the agent is idle).

## Design

### New helpers in `lib/statusline.sh` (pure, mirror the `get_tree_*` trio)

- `get_spawned_pids <root_pid> [skip_pid]` — BFS the descendants of `root_pid`
  (root NOT included). When a child equals `skip_pid`, neither it nor its
  subtree is visited. Echoes one pid per line; echoes nothing when the agent
  has spawned nothing.
- `get_spawned_rss_kb <root_pid> [skip_pid]` — summed RSS (KB) of those pids.
- `get_spawned_footprint_kb <root_pid> [skip_pid]` — summed `phys_footprint`
  (KB) via macOS `footprint`, same locale-safe parse as the tree variant;
  echoes nothing when `footprint` is unavailable/empty so callers fall back to
  RSS. Called with the collected pid list in one `footprint` invocation.
- `get_spawned_cpu_pct <root_pid> [skip_pid]` — summed `ps -o %cpu` of those
  pids, rounded to an integer, locale-safe; echoes nothing when no pid yields
  a reading.

### Rendering in `templates/statusline-wrapper.sh`

Inside the existing claude-ancestor block (after the session mem/CPU segments):
compute `get_spawned_pids "$pid" "$$"`; when non-empty, render ONE extra
segment combining spawned memory and CPU, e.g. ` |  120M 7%` (nf-fa-sitemap
glyph U+F0E8, cyan), memory formatted M/G exactly like the session segment,
footprint preferred with RSS fallback. When the agent has spawned nothing the
segment is absent entirely (no clutter on idle sessions). If only one of
mem/CPU resolves, render just that half.

## Error handling

Same graceful degradation as the tree metrics: disappeared pids are skipped
mid-walk; empty readings hide the segment rather than showing stale/zero noise.

## Testing (TDD, `test/bash/statusline_test.go` patterns)

Lib level (mock `ps`/`pgrep`/`footprint`): spawned pids exclude root; skip-pid
subtree pruned; empty on no children; rss/cpu sums cover descendants only;
footprint receives exactly the descendant pids. Wrapper level (hermetic env):
segment renders with the glyph when descendants exist; segment absent when the
agent spawned nothing.

## Rollout

`lib/statusline.sh` → `~/.claude/statusline-helpers.sh` and
`templates/statusline-wrapper.sh` → `~/.claude/statusline-wrapper.sh` are
install-time COPIES — cp both after the change so the live statusline picks it
up.
