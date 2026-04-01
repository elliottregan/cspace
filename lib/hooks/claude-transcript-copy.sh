#!/usr/bin/env bash
# Copies completed transcript + metadata to the shared log volume.
# Only fires for autonomous sessions (CLAUDE_AUTONOMOUS=1).
set -euo pipefail

[ "${CLAUDE_AUTONOMOUS:-}" = "1" ] || exit 0

INPUT=$(cat)
LOG_DIR="${CLAUDE_LOG_DIR:-/logs}"

SESSION_ID=$(jq -r '.session_id // empty' <<< "$INPUT")
TRANSCRIPT=$(jq -r '.transcript_path // empty' <<< "$INPUT")

[ -n "$SESSION_ID" ] || exit 0
[ -n "$TRANSCRIPT" ] || exit 0
[ -f "$TRANSCRIPT" ] || exit 0
[ -d "$LOG_DIR" ] || exit 0

# Atomic write: copy to temp, then rename
cp "$TRANSCRIPT" "${LOG_DIR}/${SESSION_ID}.jsonl.tmp" \
  && mv "${LOG_DIR}/${SESSION_ID}.jsonl.tmp" "${LOG_DIR}/${SESSION_ID}.jsonl"

# Write sidecar metadata for correlation
jq -n \
  --arg sid "$SESSION_ID" \
  --arg instance "${CLAUDE_INSTANCE:-}" \
  --arg issue "${CLAUDE_ISSUE:-}" \
  --arg container "$(hostname)" \
  --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  '{
    session_id: $sid,
    instance: $instance,
    issue: $issue,
    container_id: $container,
    started_at: $ts
  }' > "${LOG_DIR}/${SESSION_ID}.meta.json"
