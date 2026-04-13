#!/usr/bin/env bash
# PreToolUse hook: prevent the agent from shutting down its own container.
# Uses CLAUDE_INSTANCE to identify self — allows commands targeting other instances.
set -euo pipefail

SELF="${CLAUDE_INSTANCE:-}"
[ -n "$SELF" ] || exit 0

INPUT=$(cat)

TOOL=$(jq -r '.tool_name // ""' <<< "$INPUT")
[ "$TOOL" = "Bash" ] || exit 0

CMD=$(jq -r '.tool_input.command // ""' <<< "$INPUT")
[ -n "$CMD" ] || exit 0

block() {
  jq -n --arg reason "$1" '{decision: "block", reason: $reason}'
  exit 0
}

# cspace down targeting self, --all, or --everywhere
if echo "$CMD" | grep -qE 'cspace\s+down'; then
  if echo "$CMD" | grep -qE "cspace\s+down\s+(--all|--everywhere)"; then
    block "Blocked: 'cspace down --all/--everywhere' would shut down your own container ($SELF)."
  fi
  if echo "$CMD" | grep -qE "cspace\s+down\s+(\S+\s+)*$SELF(\s|$)"; then
    block "Blocked: cannot shut down your own container ($SELF)."
  fi
fi

# docker stop/rm/kill targeting own container name
if echo "$CMD" | grep -qE "docker\s+(stop|rm|kill)\s+(\S+\s+)*$SELF(\s|$)"; then
  block "Blocked: cannot stop/remove/kill your own container ($SELF)."
fi

# docker compose down targeting own compose project
if echo "$CMD" | grep -qE "docker\s+compose\s+(-p\s+\S*$SELF\S*\s+)?down"; then
  # Allow if an explicit -p flag targets a different project
  if echo "$CMD" | grep -qE "docker\s+compose\s+-p\s+"; then
    if ! echo "$CMD" | grep -qE "docker\s+compose\s+-p\s+\S*$SELF"; then
      exit 0
    fi
  fi
  block "Blocked: 'docker compose down' would shut down your own container ($SELF)."
fi
