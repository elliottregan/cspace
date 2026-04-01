#!/usr/bin/env bash
# Copies the transcript to the shared log volume on exit.
# Called as an EXIT trap when the agent process is killed before SessionEnd fires.
set -euo pipefail

SID=$(cat /tmp/claude-session-id.txt 2>/dev/null) || exit 0
TPATH="$HOME/.claude/projects/-workspace/${SID}.jsonl"
[ -f "$TPATH" ] || exit 0
LOG_DIR="${CLAUDE_LOG_DIR:-/logs}"
[ -d "$LOG_DIR" ] || exit 0

# Skip if the SessionEnd hook already copied it
[ -f "${LOG_DIR}/${SID}.jsonl" ] && exit 0

cp "$TPATH" "${LOG_DIR}/${SID}.jsonl" 2>/dev/null || true
jq -n \
  --arg sid "$SID" \
  --arg instance "${CLAUDE_INSTANCE:-}" \
  --arg issue "${CLAUDE_ISSUE:-}" \
  --arg container "$(hostname)" \
  --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  '{session_id: $sid, instance: $instance, issue: $issue, container_id: $container, ended_at: $ts, exit: "killed"}' \
  > "${LOG_DIR}/${SID}.meta.json" 2>/dev/null || true
