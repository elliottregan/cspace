#!/usr/bin/env bash
# Tests cspace-agent-state.sh: the JSON it writes, the states it refuses, and
# that the write is atomic (never truncates the target in place).
#
# Needs jq, which the sandbox image has and a stock macOS host may not — so
# this skips rather than fails when jq is absent, like statusline.test.sh.
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$HERE/cspace-agent-state.sh"
fail() { echo "FAIL: $1"; exit 1; }

if ! command -v jq >/dev/null 2>&1; then
  echo "SKIP: jq not installed"
  exit 0
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
STATE_FILE="$TMP/agent-state.json"

PAYLOAD='{"session_id":"sess-abc","hook_event_name":"UserPromptSubmit","cwd":"/workspace"}'

# ── a normal hook call records state, session and event ───────────────────
printf '%s' "$PAYLOAD" | CSPACE_AGENT_STATE_FILE="$STATE_FILE" bash "$SCRIPT" working \
  || fail "script exited non-zero on a normal call"
[ -f "$STATE_FILE" ] || fail "no state file written"

jq -e '.state == "working"' "$STATE_FILE" >/dev/null \
  || fail "state not recorded: $(cat "$STATE_FILE")"
jq -e '.session_id == "sess-abc"' "$STATE_FILE" >/dev/null \
  || fail "session_id not taken from the hook payload: $(cat "$STATE_FILE")"
jq -e '.event == "UserPromptSubmit"' "$STATE_FILE" >/dev/null \
  || fail "event not taken from the hook payload: $(cat "$STATE_FILE")"
jq -e '.at | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$")' "$STATE_FILE" >/dev/null \
  || fail "timestamp is not RFC3339 UTC: $(cat "$STATE_FILE")"

# ── every state the hooks table uses is accepted ──────────────────────────
for s in starting working needs-input idle exited; do
  printf '%s' "$PAYLOAD" | CSPACE_AGENT_STATE_FILE="$STATE_FILE" bash "$SCRIPT" "$s" \
    || fail "state '$s' rejected"
  jq -e --arg s "$s" '.state == $s' "$STATE_FILE" >/dev/null || fail "state '$s' not written"
done

# ── an unknown state leaves the last good value and still exits 0 ─────────
# A PreToolUse hook exiting non-zero BLOCKS the tool call, so this script may
# never fail a turn over its own bookkeeping.
printf '%s' "$PAYLOAD" | CSPACE_AGENT_STATE_FILE="$STATE_FILE" bash "$SCRIPT" bogus 2>/dev/null \
  || fail "unknown state exited non-zero — that would block a tool call"
jq -e '.state == "exited"' "$STATE_FILE" >/dev/null \
  || fail "unknown state overwrote the last good value: $(cat "$STATE_FILE")"

# ── no argument at all is also survivable ─────────────────────────────────
printf '%s' "$PAYLOAD" | CSPACE_AGENT_STATE_FILE="$STATE_FILE" bash "$SCRIPT" 2>/dev/null \
  || fail "missing state argument exited non-zero"

# ── the write is atomic: a failed render never truncates the target ───────
# Stub jq with one that fails, so the temp file comes out empty. A script
# writing straight to the target would leave it empty or half-written; the
# host polls this file once a second and must never read a torn one.
mkdir -p "$TMP/bin"
cat > "$TMP/bin/jq" <<'EOF'
#!/usr/bin/env bash
exit 1
EOF
chmod +x "$TMP/bin/jq"
before="$(cat "$STATE_FILE")"
printf '%s' "$PAYLOAD" | PATH="$TMP/bin:$PATH" CSPACE_AGENT_STATE_FILE="$STATE_FILE" \
  bash "$SCRIPT" idle 2>/dev/null || fail "failed render exited non-zero"
[ "$(cat "$STATE_FILE")" = "$before" ] \
  || fail "a failed render clobbered the target: $(cat "$STATE_FILE")"

# ── and it leaves no temp files behind ────────────────────────────────────
leftovers="$(find "$TMP" -maxdepth 1 -name 'agent-state.json.tmp.*' | wc -l | tr -d ' ')"
[ "$leftovers" = "0" ] || fail "left $leftovers temp file(s) behind"

echo "PASS"
