#!/usr/bin/env bash
# Tests the cspace-browser block in cspace-install-plugins.sh using a `claude` stub.
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$HERE/cspace-install-plugins.sh"
fail() { echo "FAIL: $1"; exit 1; }

run_case() {  # $1=label  $2=cdp_url ("" = unset)  ; echoes the captured claude calls
  local tmp; tmp="$(mktemp -d)"
  mkdir -p "$tmp/bin" "$tmp/market/.claude-plugin"
  echo '{"name":"cspace","owner":{"name":"cspace"},"plugins":[{"name":"cspace-browser","source":"./cspace-browser"}]}' > "$tmp/market/.claude-plugin/marketplace.json"
  # stub `claude`: record args, print nothing for `marketplace list`
  cat > "$tmp/bin/claude" <<EOF
#!/usr/bin/env bash
echo "claude \$*" >> "$tmp/calls.log"
exit 0
EOF
  chmod +x "$tmp/bin/claude"
  HOME="$tmp" PATH="$tmp/bin:$PATH" \
    CSPACE_BROWSER_MARKET_DIR="$tmp/market" \
    CSPACE_BROWSER_CDP_URL="$2" \
    bash "$SCRIPT" >/dev/null 2>&1
  cat "$tmp/calls.log" 2>/dev/null || true
}

present="$(run_case present 'http://10.0.0.5:9222')"
echo "$present" | grep -q 'plugins marketplace add .*/market' || fail "present: marketplace not added"
echo "$present" | grep -q 'plugins install --scope user cspace-browser@cspace' || fail "present: plugin not installed"

absent="$(run_case absent '')"
echo "$absent" | grep -q 'cspace-browser@cspace' && fail "absent: plugin installed despite no CDP url"

echo "PASS"
