#!/usr/bin/env bash
# Spike: verify ANTHROPIC_API_KEY → secrets.env → sandbox → Claude SDK auth path.
#
# Goal: prove that putting ANTHROPIC_API_KEY in ~/.cspace/secrets.env (or the
# project's .cspace/secrets.env) actually lets Claude authenticate inside a
# cspace sandbox. We confirm by:
#   1. apiKeySource in the SDK init event is NOT "none"
#   2. The assistant message contains a probe phrase we asked for
#   3. The result event reports is_error=false
#
# Setup expected:
#   - ANTHROPIC_API_KEY exists in ONE of:
#       ~/.cspace/secrets.env       (preferred — works across all projects)
#       .cspace/secrets.env         (project-local; will pick up automatically)
#       host shell env              (export ANTHROPIC_API_KEY=…)
#
# ── POC concessions (acknowledged, fix during polish phase) ────────────────
#   - Hardcoded sandbox name "auth-test"; concurrent runs collide.
#   - Probe phrase is a single fixed string ("CSPACE-AUTH-OK"); a real test
#     would randomize it to defeat caching.
#   - No retry / backoff. Single attempt; if Claude API is rate-limited or
#     slow, the test reports failure rather than retrying.
#   - Trusts events.ndjson formatting; doesn't validate envelope structure.
# ───────────────────────────────────────────────────────────────────────────
set -euo pipefail

CSPACE="${CSPACE:-./bin/cspace-go}"
SANDBOX="${SANDBOX:-auth-test}"
PROBE="CSPACE-AUTH-OK"
TIMEOUT_SECS="${TIMEOUT_SECS:-30}"

if [[ ! -x "$CSPACE" ]]; then
  echo "build cspace-go first: make build" >&2
  exit 1
fi

# Precheck: at least one of the recognized Claude auth credentials must be
# reachable from somewhere the secrets layer will see. Claude Code accepts:
#   - ANTHROPIC_API_KEY       (direct API key)
#   - CLAUDE_CODE_OAUTH_TOKEN (subscription-backed OAuth token from `claude /login`)
have_key="no"
for var in ANTHROPIC_API_KEY CLAUDE_CODE_OAUTH_TOKEN; do
  for f in "$HOME/.cspace/secrets.env" ".cspace/secrets.env"; do
    if [[ -f "$f" ]] && grep -q "^${var}=" "$f"; then
      have_key="yes ($var in $f)"
      break 2
    fi
  done
  if [[ -n "${!var:-}" ]]; then
    have_key="yes ($var in host shell env)"
    break
  fi
done
echo "==> Claude auth credential available: $have_key"
if [[ "$have_key" == "no" ]]; then
  echo "FAIL: no ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN in" >&2
  echo "      ~/.cspace/secrets.env, .cspace/secrets.env, or shell env." >&2
  exit 1
fi

container="cspace-cspace-${SANDBOX}"

cleanup_on_error() {
  if [[ "$?" -ne 0 ]]; then
    echo "FAIL — leaving $container up for debugging." >&2
    echo "    container exec $container cat /sessions/primary/events.ndjson" >&2
    echo "    $CSPACE prototype-down $SANDBOX" >&2
  fi
}
trap cleanup_on_error EXIT

echo "==> bringing up sandbox $SANDBOX"
"$CSPACE" prototype-up "$SANDBOX" >/dev/null
sleep 3

echo "==> sending probe turn"
"$CSPACE" prototype-send "$SANDBOX" "Reply with exactly the phrase $PROBE and nothing else." >/dev/null

# Poll events.ndjson for a result event. Bail out after TIMEOUT_SECS.
echo "==> waiting up to ${TIMEOUT_SECS}s for SDK result"
deadline=$(( $(date +%s) + TIMEOUT_SECS ))
while [[ $(date +%s) -lt $deadline ]]; do
  if container exec "$container" grep -q '"type":"result"' /sessions/primary/events.ndjson 2>/dev/null; then
    break
  fi
  sleep 1
done

echo "==> inspecting events.ndjson"
parser="$(dirname "$0")/2026-05-01-claude-auth.py"
verdict=$(container exec "$container" cat /sessions/primary/events.ndjson | python3 "$parser" "$PROBE")
echo "$verdict"

# Verdict via parser exit code (0 = pass, non-zero = fail).
if container exec "$container" cat /sessions/primary/events.ndjson | python3 "$parser" "$PROBE" >/dev/null; then
  echo "==> PASS: Claude auth works end-to-end inside sandbox"
  trap - EXIT
  "$CSPACE" prototype-down "$SANDBOX" >/dev/null
  echo "==> sandbox $SANDBOX torn down"
else
  exit 1
fi
