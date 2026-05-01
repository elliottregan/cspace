#!/usr/bin/env bash
# Spike: long-idle session survival.
#
# Goal: prove that a Claude session in a cspace sandbox survives a long
# idle period (default 30 min) and still responds correctly when a new
# turn arrives. Confirms:
#   - Supervisor process doesn't time out or die between turns.
#   - SDK's for-await loop on the prompt stream stays parked indefinitely.
#   - Auth state (env-injected ANTHROPIC_API_KEY) doesn't go stale.
#   - Conversational memory persists across idle (turn 2 references turn 1).
#
# ── POC concessions (acknowledged, polished in P1) ────────────────────────
#   - Hardcoded sandbox name "idle-test"; concurrent runs collide.
#   - IDLE_SECS defaults to 1800 (30 min). Override via env. Use 60-120 for
#     a fast smoke; 1800+ to actually exercise long-idle survival.
#   - When STREAM=1 (default), tails events.ndjson live so you can watch
#     the agent's SDK events as they land. Set STREAM=0 for a quiet run.
#   - On failure leaves the sandbox up for inspection; on success tears down.
#   - No sub-checks for memory pressure / tcp keepalive — just "did the
#     final turn produce a real response with the magic word?"
# ───────────────────────────────────────────────────────────────────────────
set -euo pipefail

CSPACE="${CSPACE:-./bin/cspace-go}"
SANDBOX="${SANDBOX:-idle-test}"
IDLE_SECS="${IDLE_SECS:-1800}"
STREAM="${STREAM:-1}"
MAGIC_WORD="purple-banjo-spirograph"   # rare phrase to anchor turn-1 memory
container="cspace-cspace-${SANDBOX}"
stream_pid=""

if [[ ! -x "$CSPACE" ]]; then
  echo "build cspace-go first: make build" >&2
  exit 1
fi

stop_stream() {
  if [[ -n "$stream_pid" ]] && kill -0 "$stream_pid" 2>/dev/null; then
    kill "$stream_pid" 2>/dev/null || true
    wait "$stream_pid" 2>/dev/null || true
  fi
  stream_pid=""
}

# Start a live tail of events.ndjson, prefixing each line with [ev].
# Backgrounded so the rest of the script proceeds. stop_stream() reaps it.
start_stream() {
  [[ "$STREAM" != "1" ]] && return 0
  ( container exec "$container" sh -c '
      while [ ! -f /sessions/primary/events.ndjson ]; do sleep 0.5; done
      exec tail -n+1 -F /sessions/primary/events.ndjson
    ' 2>/dev/null | python3 "$(dirname "$0")/2026-05-01-long-idle-stream.py"
  ) &
  stream_pid=$!
}

cleanup_on_error() {
  local rc="$?"
  stop_stream
  if [[ "$rc" -ne 0 ]]; then
    echo "FAIL — leaving $container up for inspection." >&2
    echo "    container exec $container cat /sessions/primary/events.ndjson | tail -30" >&2
    echo "    $CSPACE prototype-down $SANDBOX" >&2
  fi
}
trap cleanup_on_error EXIT

# Reset any prior sandbox of the same name.
"$CSPACE" prototype-down "$SANDBOX" >/dev/null 2>&1 || true

echo "==> [$(date -u +%FT%TZ)] bringing up sandbox $SANDBOX"
"$CSPACE" prototype-up "$SANDBOX" >/dev/null
sleep 4

start_stream

echo "==> [$(date -u +%FT%TZ)] turn 1: anchor the magic word"
"$CSPACE" prototype-send "$SANDBOX" \
  "Please remember this exact phrase for later: '$MAGIC_WORD'. Acknowledge with just the word READY." >/dev/null

# Give turn 1 time to land + Claude time to respond.
sleep 12

echo "==> [$(date -u +%FT%TZ)] sleeping $IDLE_SECS seconds (idle window)"
sleep "$IDLE_SECS"

echo "==> [$(date -u +%FT%TZ)] turn 2: ask the agent for the magic word"
"$CSPACE" prototype-send "$SANDBOX" \
  "What was the exact phrase I asked you to remember earlier? Reply with just the phrase, nothing else." >/dev/null

# Give turn 2 a generous window — model can be slow after a long idle.
sleep 30

echo "==> [$(date -u +%FT%TZ)] inspecting events.ndjson"
stop_stream
parser="$(dirname "$0")/2026-05-01-long-idle.py"
verdict=$(container exec "$container" cat /sessions/primary/events.ndjson | python3 "$parser" "$MAGIC_WORD")
echo "$verdict"

if container exec "$container" cat /sessions/primary/events.ndjson | python3 "$parser" "$MAGIC_WORD" >/dev/null; then
  trap - EXIT
  "$CSPACE" prototype-down "$SANDBOX" >/dev/null
  echo "==> sandbox $SANDBOX torn down"
  echo "==> PASS — session survived ${IDLE_SECS}s idle and recalled the magic word"
else
  exit 1
fi
