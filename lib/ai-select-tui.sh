#!/bin/bash
# AI tool selection TUI wrapper using ghost-tab-tui

# Interactive AI tool multi-selection
# Returns 0 if selected, 1 if cancelled
# Sets: _selected_ai_tool (first tool by priority)
# Sets: _selected_ai_tools (space-separated list of all selected tools)
select_ai_tool_interactive() {
  if ! command -v ghost-tab-tui &>/dev/null; then
    error "ghost-tab-tui binary not found. Please reinstall."
    return 1
  fi

  local result
  if ! result=$(ghost-tab-tui multi-select-ai-tool 2>/dev/null); then
    return 1
  fi

  local confirmed
  if ! confirmed=$(echo "$result" | jq -r '.confirmed' 2>/dev/null); then
    error "Failed to parse AI tool selection response"
    return 1
  fi

  # Validate against null/empty
  if [[ -z "$confirmed" || "$confirmed" == "null" ]]; then
    error "TUI returned invalid confirmation status"
    return 1
  fi

  if [[ "$confirmed" != "true" ]]; then
    return 1
  fi

  local tools_json
  if ! tools_json=$(echo "$result" | jq -r '.tools[]' 2>/dev/null); then
    error "Failed to parse selected tools"
    return 1
  fi

  # Validate against empty selection
  if [[ -z "$tools_json" ]]; then
    error "TUI returned empty tool selection"
    return 1
  fi

  # Set space-separated list of all selected tools
  _selected_ai_tools="$tools_json"

  # Pick first tool by priority: claude > opencode
  local priority_order=("claude" "opencode")
  _selected_ai_tool=""
  for priority_tool in "${priority_order[@]}"; do
    if echo "$tools_json" | grep -qx "$priority_tool"; then
      _selected_ai_tool="$priority_tool"
      break
    fi
  done

  # Fallback to first tool if none matched priority
  if [[ -z "$_selected_ai_tool" ]]; then
    _selected_ai_tool=$(echo "$tools_json" | head -n1)
  fi

  return 0
}
