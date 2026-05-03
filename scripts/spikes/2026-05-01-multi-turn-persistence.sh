#!/usr/bin/env bash
# Spike: multi-turn persistence in a single Claude session via the Bun supervisor.
#
# Goal: verify that pushing N user turns into a live PromptStream keeps them
# inside ONE persistent Claude session (stable session_id), not N fresh
# sessions. Answers the question: "can a Claude session in persistent mode
# receive multiple messages?"
#
# ── POC concessions (acknowledged, fix during polish phase) ────────────────
#   - Hardcoded sandbox name "mt"; concurrent runs of this script will collide.
#   - Sleeps are fixed durations; not robust against slow SDK init / cold-pull.
#   - Tests plumbing only — does NOT require ANTHROPIC_API_KEY. With no auth,
#     each "assistant" message is the synthetic "Not logged in · Please run
#     /login" response from the SDK, but the user-turn → SDK-event cycle still
#     fires per turn and that's what we're verifying.
#   - Uses python3 to inspect events.ndjson (host dep — fine for a spike).
#   - Doesn't pin a count to TURNS env (defaults to 5); not a knob worth a
#     full --flag in POC.
#   - Cleans up the sandbox on success only. On failure, the sandbox is left
#     up so you can `container exec cspace-cspace-mt sh` to debug.
# ───────────────────────────────────────────────────────────────────────────
set -euo pipefail

CSPACE="${CSPACE:-./bin/cspace-go}"
SANDBOX="${SANDBOX:-mt}"
TURNS="${TURNS:-5}"

if [[ ! -x "$CSPACE" ]]; then
  echo "build cspace-go first: make build" >&2
  exit 1
fi

# Normalize project name for the container-name prefix the same way cspace2-up
# does. Project name comes from cfg.Project.Name (== "cspace" inside this repo).
container="cspace-cspace-${SANDBOX}"

cleanup_on_error() {
  if [[ "$?" -ne 0 ]]; then
    echo "FAIL — leaving $container up for debugging." >&2
    echo "    container exec $container sh" >&2
    echo "    ./$CSPACE prototype-down $SANDBOX" >&2
  fi
}
trap cleanup_on_error EXIT

echo "==> bringing up sandbox $SANDBOX"
"$CSPACE" prototype-up "$SANDBOX" >/dev/null

# Give the supervisor a moment to spawn the SDK child and bind /sessions/primary.
sleep 3

echo "==> sending $TURNS turns"
for i in $(seq 1 "$TURNS"); do
  "$CSPACE" prototype-send "$SANDBOX" "turn-$i: please reply with the number $i" >/dev/null
  sleep 4   # let each turn complete before sending the next
done

echo "==> inspecting events.ndjson"
parser="$(dirname "$0")/2026-05-01-multi-turn-persistence.py"
verdict=$(container exec "$container" cat /sessions/primary/events.ndjson | python3 "$parser")

echo "$verdict"

# Verdict: pass if N user-turns AND N init/assistant/result cycles AND 1 session_id AND 0 errors.
fail=0
if ! echo "$verdict" | grep -q "\"user_turns\": ${TURNS}"; then
  echo "FAIL: expected $TURNS user-turn lines" >&2; fail=1
fi
if ! echo "$verdict" | grep -q "\"sdk_inits\": ${TURNS}"; then
  echo "FAIL: expected $TURNS sdk init events" >&2; fail=1
fi
if ! echo "$verdict" | grep -q "\"sdk_assistants\": ${TURNS}"; then
  echo "FAIL: expected $TURNS sdk assistant events" >&2; fail=1
fi
if ! echo "$verdict" | grep -q "\"sdk_results\": ${TURNS}"; then
  echo "FAIL: expected $TURNS sdk result events" >&2; fail=1
fi
if ! echo "$verdict" | grep -q "\"sdk_errors\": 0"; then
  echo "FAIL: sdk-error count non-zero" >&2; fail=1
fi
if echo "$verdict" | python3 -c "
import json, sys
v = json.loads(sys.stdin.read())
ids = v['distinct_session_ids']
sys.exit(0 if len(ids) == 1 else 1)
"; then :; else
  echo "FAIL: expected exactly 1 distinct session_id (persistent mode)" >&2; fail=1
fi

if [[ "$fail" -eq 0 ]]; then
  echo "==> PASS: $TURNS turns, 1 session_id, no errors"
  trap - EXIT
  "$CSPACE" prototype-down "$SANDBOX" >/dev/null
  echo "==> sandbox $SANDBOX torn down"
else
  exit 1
fi
