#!/usr/bin/env bash
# Reads Claude Code hook payload from stdin, emits a structured JSON log line.
# Only fires for autonomous sessions (CLAUDE_AUTONOMOUS=1).
set -euo pipefail

[ "${CLAUDE_AUTONOMOUS:-}" = "1" ] || exit 0

INPUT=$(cat)

jq -c '
  . as $d |
  {
    ts: (now | todate),
    sid: (.session_id // "" | .[0:8]),
    instance: (env.CLAUDE_INSTANCE // ""),
    issue: (env.CLAUDE_ISSUE // "")
  } +
  if .hook_event_name == "SessionStart" then
    { event: "session_start", cwd: .cwd }
  elif .hook_event_name == "PostToolUse" then
    { event: "tool", tool: .tool_name, ok: (if .tool_response.error then false else true end) }
  elif .hook_event_name == "Notification" then
    { event: "notify", msg: .message }
  elif .hook_event_name == "SessionEnd" then
    { event: "session_end" }
  else empty end
' <<< "$INPUT"
