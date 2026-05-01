#!/usr/bin/env bash
# Spike: browser sidecar pattern for Playwright MCP under Apple Container.
#
# Goal: prove the sidecar topology works end-to-end. Two containers per
# sandbox: the cspace2 container (lean, no browser baked in) and a
# Playwright base image running headless Chromium with remote debugging
# exposed. The supervisor's playwright-mcp connects via CDP across the
# Apple Container default bridge.
#
# Verifies:
#   1. Browser sidecar starts and exposes CDP at <ip>:9222.
#   2. Sandbox can reach the sidecar (cross-container routing on
#      192.168.64.0/24, no name DNS needed).
#   3. The agent inside the sandbox can call Playwright MCP tools that
#      operate the sidecar's Chrome.
#   4. The agent observes real DOM content (page title) — proof the
#      whole CDP path round-trips.
#
# ── POC concessions (acknowledged, polished in P1) ────────────────────────
#   - Hardcoded sandbox name "browser-test"; concurrent runs collide.
#   - Hardcoded sidecar image (mcr.microsoft.com/playwright:v1.58.0-noble).
#     Build a custom lean image in P2 if image size matters.
#   - Sidecar launches with --no-sandbox and --disable-gpu (standard for
#     containerized Chrome). --headless=new for the modern headless mode.
#   - No --user-data-dir → Chrome uses /tmp; per-session profile isolation
#     is a P2 concern.
#   - Wildcard glob on /ms-playwright/chromium-*/chrome-linux/chrome path;
#     would break if Microsoft restructures their image layout.
#   - Sidecar lifecycle is manual in this script. P1's cspace2-up should
#     own this end-to-end (start sidecar → capture IP → bring up sandbox →
#     teardown both on cspace2-down).
# ───────────────────────────────────────────────────────────────────────────
set -euo pipefail

CSPACE="${CSPACE:-./bin/cspace-go}"
SANDBOX="${SANDBOX:-browser-test}"
PROJECT_ROOT="$(git rev-parse --show-toplevel)"
PROJECT_NAME="$(basename "$PROJECT_ROOT")"
sandbox_container="cspace-${PROJECT_NAME}-${SANDBOX}"
sidecar_container="cspace-${PROJECT_NAME}-${SANDBOX}-browser"

if [[ ! -x "$CSPACE" ]]; then
  echo "build cspace-go first: make build" >&2
  exit 1
fi

cleanup_on_error() {
  if [[ "$?" -ne 0 ]]; then
    echo "FAIL — leaving containers up for inspection." >&2
    echo "    container ls --all" >&2
    echo "    container exec $sandbox_container cat /sessions/primary/events.ndjson | tail -40" >&2
    echo "    container logs $sidecar_container | tail -20" >&2
    echo "    $CSPACE prototype-down $SANDBOX" >&2
    echo "    container stop $sidecar_container && container rm $sidecar_container" >&2
  fi
}
trap cleanup_on_error EXIT

# Idempotent: torch any prior containers from a previous run.
"$CSPACE" prototype-down "$SANDBOX" >/dev/null 2>&1 || true
container stop "$sidecar_container" 2>/dev/null || true
container rm   "$sidecar_container" 2>/dev/null || true

echo "==> [$(date -u +%FT%TZ)] starting browser sidecar"
# Modern Chromium ignores --remote-debugging-address=0.0.0.0 and force-binds
# to 127.0.0.1, so we run Chrome on :9223 internally and use socat to forward
# 0.0.0.0:9222 -> 127.0.0.1:9223. Same workaround the legacy compose stack
# uses (lib/templates/docker-compose.shared.yml).
container run -d \
  --name "$sidecar_container" \
  --dns 1.1.1.1 --dns 8.8.8.8 \
  mcr.microsoft.com/playwright:v1.58.0-noble \
  bash -c '
    set -e
    apt-get update -qq && apt-get install -y -qq socat >/dev/null 2>&1
    /ms-playwright/chromium-*/chrome-linux/chrome \
      --headless=new --no-sandbox --disable-gpu \
      --remote-debugging-port=9223 \
      about:blank &
    until curl -sf http://127.0.0.1:9223/json/version >/dev/null 2>&1; do sleep 0.5; done
    exec socat TCP-LISTEN:9222,fork,reuseaddr TCP:127.0.0.1:9223
  ' >/dev/null

# Capture sidecar IP. Apple Container's inspect returns JSON with
# networks[].ipv4Address as "X.Y.Z.W/24"; strip the suffix.
echo "==> [$(date -u +%FT%TZ)] waiting for sidecar IP"
sidecar_ip=""
for _ in $(seq 1 30); do
  sidecar_ip=$(container inspect "$sidecar_container" 2>/dev/null \
    | python3 -c '
import json,sys
data = json.load(sys.stdin)
for net in data[0].get("networks", []):
    addr = net.get("ipv4Address", "")
    if addr:
        print(addr.split("/")[0])
        break
' || true)
  if [[ -n "$sidecar_ip" ]]; then break; fi
  sleep 1
done
if [[ -z "$sidecar_ip" ]]; then
  echo "FAIL: could not get sidecar IP" >&2
  exit 1
fi
echo "==> sidecar IP: $sidecar_ip"

# Wait for Chrome to bind :9222 by polling its standard endpoint from the host.
echo "==> [$(date -u +%FT%TZ)] waiting for CDP endpoint"
cdp_url="http://${sidecar_ip}:9222"
for _ in $(seq 1 30); do
  if curl -fs --max-time 2 "${cdp_url}/json/version" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
chrome_version=$(curl -fs --max-time 2 "${cdp_url}/json/version" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("Browser",""))' || true)
if [[ -z "$chrome_version" ]]; then
  echo "FAIL: CDP endpoint $cdp_url did not respond" >&2
  exit 1
fi
echo "==> CDP up: $chrome_version"

echo "==> [$(date -u +%FT%TZ)] bringing up sandbox with CDP env injected"
"$CSPACE" prototype-up "$SANDBOX" \
  --env "CSPACE_BROWSER_CDP_URL=${cdp_url}" \
  >/dev/null
sleep 5

echo "==> sending agent task"
"$CSPACE" prototype-send "$SANDBOX" \
  "Use the Playwright MCP browser_navigate tool to navigate to https://example.com, then report the exact <title> text of the page. Reply with ONLY the title, nothing else. If anything fails, stop and explain what went wrong." \
  >/dev/null

# Wait for result event.
echo "==> [$(date -u +%FT%TZ)] waiting up to 60s for SDK result"
deadline=$(( $(date +%s) + 60 ))
while [[ $(date +%s) -lt $deadline ]]; do
  if container exec "$sandbox_container" grep -q '"type":"result"' /sessions/primary/events.ndjson 2>/dev/null; then
    break
  fi
  sleep 2
done

echo "==> inspecting events.ndjson"
parser="$(dirname "$0")/2026-05-01-browser-sidecar.py"
verdict=$(container exec "$sandbox_container" cat /sessions/primary/events.ndjson | python3 "$parser")
echo "$verdict"

# Verdict via parser exit code.
if container exec "$sandbox_container" cat /sessions/primary/events.ndjson | python3 "$parser" >/dev/null; then
  trap - EXIT
  "$CSPACE" prototype-down "$SANDBOX" >/dev/null
  container stop "$sidecar_container" >/dev/null
  container rm "$sidecar_container" >/dev/null
  echo "==> sandbox + sidecar torn down"
  echo "==> PASS — agent drove Chromium in sidecar via Playwright MCP"
else
  exit 1
fi
