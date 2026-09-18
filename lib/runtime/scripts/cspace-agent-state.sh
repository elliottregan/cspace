#!/usr/bin/env bash
# cspace agent-state hook — records what the sandbox's interactive Claude
# session is doing, so the host can render it without asking Claude anything.
#
# Invoked from the hooks block that cspace-entrypoint.sh writes into
# ~/.claude/settings.json:
#
#     cspace-agent-state.sh <state>
#
# with <state> one of: starting working needs-input idle exited.
# Claude Code pipes the hook payload as JSON on stdin; session_id and
# hook_event_name are read from there.
#
# The file is /sessions/agent-state.json, which is the host's
# ~/.cspace/sessions/<project>/<sandbox>/agent-state.json through the existing
# bind mount — so the host reads it directly, with no control-port round trip.
# The write is atomic (temp file in the same directory, then rename) because
# the host polls it about once a second and must never read half a record.
#
# This script NEVER exits non-zero. A PreToolUse hook that exits 2 blocks the
# tool call, and a state file is not worth failing an agent's turn over.
set -u

STATE="${1:-}"
STATE_FILE="${CSPACE_AGENT_STATE_FILE:-/sessions/agent-state.json}"

case "$STATE" in
    starting|working|needs-input|idle|exited) ;;
    *)
        echo "cspace-agent-state: ignoring unknown state '${STATE}'" >&2
        exit 0
        ;;
esac

# Read the hook payload only when stdin is a pipe. Run by hand from a
# terminal, `cat` would block forever waiting for EOF.
payload=""
if [ ! -t 0 ]; then
    payload="$(cat)"
fi

session_id=""
event=""
if [ -n "$payload" ] && command -v jq >/dev/null 2>&1; then
    session_id="$(printf '%s' "$payload" | jq -r '.session_id // ""' 2>/dev/null)"
    event="$(printf '%s' "$payload" | jq -r '.hook_event_name // ""' 2>/dev/null)"
fi
[ -n "$event" ] || event="${2:-}"

now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
dir="$(dirname "$STATE_FILE")"
mkdir -p "$dir" 2>/dev/null || true
tmp="${STATE_FILE}.tmp.$$"

if command -v jq >/dev/null 2>&1; then
    jq -n --arg state "$STATE" --arg at "$now" \
          --arg session_id "$session_id" --arg event "$event" \
          '{state: $state, at: $at, session_id: $session_id, event: $event}' \
          > "$tmp" 2>/dev/null
else
    # No jq (a project image that trimmed it). The fields are ours and
    # contain no quotes, so a printf is safe enough for the fallback.
    printf '{"state":"%s","at":"%s","session_id":"%s","event":"%s"}\n' \
        "$STATE" "$now" "$session_id" "$event" > "$tmp" 2>/dev/null
fi

# Only publish a non-empty render; a failed one must leave the previous
# state in place rather than truncate it.
if [ -s "$tmp" ]; then
    mv -f "$tmp" "$STATE_FILE" 2>/dev/null || rm -f "$tmp"
else
    rm -f "$tmp"
fi

exit 0
