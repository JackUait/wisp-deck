package claudeconfig

// MCPWindowFloor is the smallest declared window that can still hold a session
// with MCP servers loaded.
//
// Measured against a live pane on 2026-09-01: a bare `claude -p "say hi"` sends
// ~23k tokens under `--strict-mcp-config` and ~41k with this machine's MCP
// servers loaded, so the schemas alone cost ~18k. A window has to cover that
// prompt, a reply worth having, and room to keep talking — 49152 already fails
// the second of those, which is why the floor sits a step above it.
//
// This is a Claude Code property, not a provider one: the same arithmetic
// decides whether any endpoint's model can run with MCP on.
const MCPWindowFloor = 65536

// WindowFitsMCP reports whether a declared window can hold Claude Code's prompt
// with MCP servers loaded. A profile under the floor is not broken — it runs
// once MCP is off — so callers warn rather than refuse.
//
// A window of zero is undeclared, not small: nothing is known about it, and
// warning on an unknown would flag every model the catalog has no length for.
func WindowFitsMCP(window int) bool {
	if window <= 0 {
		return true
	}
	return window >= MCPWindowFloor
}
