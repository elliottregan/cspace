#!/usr/bin/env bash
# Tests the service-URL section of statusline.sh with a stubbed `ss` and a
# devcontainer.json supplied through CSPACE_STATUSLINE_DEVCONTAINER.
#
# statusline.sh needs bash 4+ (associative arrays) and jq, which the sandbox
# image has and a stock macOS host does not — so this skips rather than fails
# on a host that cannot run its subject. To run it against the real image:
#   container run --rm -v "$PWD:/repo" -w /repo cspace:latest \
#     bash lib/runtime/scripts/statusline.test.sh
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$HERE/statusline.sh"
fail() { echo "FAIL: $1"; exit 1; }

if [ "${BASH_VERSINFO[0]}" -lt 4 ]; then
  echo "SKIP: statusline.sh needs bash 4+ (this is ${BASH_VERSION}); run it in the sandbox image"
  exit 0
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "SKIP: jq not installed"
  exit 0
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin" "$TMP/work"

# Stub `ss` with one labeled port (5173) and one unlabeled (24678), plus an
# internal port (6201) that must never render.
cat > "$TMP/bin/ss" <<'EOF'
#!/usr/bin/env bash
cat <<'OUT'
State  Recv-Q Send-Q Local Address:Port Peer Address:Port
LISTEN 0      511          0.0.0.0:5173       0.0.0.0:*
LISTEN 0      511        127.0.0.1:24678      0.0.0.0:*
LISTEN 0      511          0.0.0.0:6201       0.0.0.0:*
OUT
EOF
chmod +x "$TMP/bin/ss"

cat > "$TMP/devcontainer.json" <<'EOF'
{
  "portsAttributes": {
    "5173": { "label": "dev" }
  }
}
EOF

INPUT='{"model":{"display_name":"Opus"},"cost":{"total_cost_usd":0},
"context_window":{"context_window_size":200000,"used_percentage":10},
"session_id":"s","transcript_path":"","cwd":"'"$TMP/work"'"}'

run_statusline() {
  printf '%s' "$INPUT" | env PATH="$TMP/bin:$PATH" \
    CSPACE_SANDBOX_NAME=sand CSPACE_PROJECT=proj \
    CSPACE_STATUSLINE_DEVCONTAINER="${CSPACE_STATUSLINE_DEVCONTAINER:-$TMP/devcontainer.json}" \
    CSPACE_STATUSLINE_CSPACE_JSON="$TMP/none.json" \
    "$@" bash "$SCRIPT" 2>/dev/null
}

# visible strips OSC 8 hyperlink wrappers and SGR color codes, leaving what a
# reader actually sees.
visible() {
  sed -e $'s/\033]8;;[^\033]*\033\\\\//g' -e $'s/\033\\[[0-9;]*m//g'
}

out="$(run_statusline)"
vis="$(printf '%s' "$out" | visible)"

# ── the label is the visible text ─────────────────────────────────────────
echo "$vis" | grep -q "dev" || fail "label 'dev' not shown: $vis"

# ── the URL is not ────────────────────────────────────────────────────────
echo "$vis" | grep -q "http://" \
  && fail "URL still visible after hiding it: $vis"
echo "$vis" | grep -q "cspace.test" \
  && fail "hostname still visible after hiding the URL: $vis"

# ── the label carries the link ────────────────────────────────────────────
expected=$'\033]8;;http://sand.proj.cspace.test:5173\033\\dev\033]8;;\033\\'
printf '%s' "$out" | grep -qF "$expected" \
  || fail "label is not an OSC 8 link to the service URL"

# ── labels present means unlabeled ports stay curated out ─────────────────
echo "$vis" | grep -q "24678" \
  && fail "unlabeled port shown despite the project declaring labels: $vis"

# ── cspace-internal ports stay hidden ─────────────────────────────────────
echo "$vis" | grep -q "6201" && fail "supervisor control port leaked into the statusline: $vis"

# ── with no labels declared, every port shows and the number is the text ──
# Hiding the URL cannot leave a bare bullet with nothing to read or click, so
# the port number stands in as the link text.
out_bare="$(CSPACE_STATUSLINE_DEVCONTAINER="$TMP/absent.json" run_statusline)"
vis_bare="$(printf '%s' "$out_bare" | visible)"
echo "$vis_bare" | grep -q "24678" || fail "unlabeled port lost its only visible text: $vis_bare"
echo "$vis_bare" | grep -q "http://" && fail "URL visible on the unlabeled path: $vis_bare"
expected_bare=$'\033]8;;http://sand.proj.cspace.test:24678\033\\24678\033]8;;\033\\'
printf '%s' "$out_bare" | grep -qF "$expected_bare" \
  || fail "unlabeled port number is not an OSC 8 link"

# ── escape hatch restores full URLs ───────────────────────────────────────
# Some renderers strip OSC 8; the visible URL is what makes those linkify by
# pattern match. Two earlier attempts at short text were reverted for exactly
# that, so the old behavior stays one env var away.
out_urls="$(run_statusline CSPACE_STATUSLINE_PORT_URLS=1)"
vis_urls="$(printf '%s' "$out_urls" | visible)"
echo "$vis_urls" | grep -q "http://sand.proj.cspace.test:5173" \
  || fail "CSPACE_STATUSLINE_PORT_URLS=1 did not restore the visible URL: $vis_urls"

echo "PASS"
