#!/usr/bin/env bash
# Tests the settings.json seed in cspace-entrypoint.sh without running the
# entrypoint itself (which wants sudo, iptables, dnsmasq and a network).
#
# The two heredocs are extracted from the script and re-rendered in a fresh
# bash with the variables the entrypoint would have set, then asserted with
# jq. That catches the thing editing a JSON heredoc actually breaks: a stray
# comma, a missing brace, a hook pointed at the wrong path.
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$HERE/cspace-entrypoint.sh"
fail() { echo "FAIL: $1"; exit 1; }

if ! command -v jq >/dev/null 2>&1; then
  echo "SKIP: jq not installed"
  exit 0
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# extract_heredoc <terminator> — the body between `<<TERM` and the closing
# `TERM`, exclusive.
extract_heredoc() {
  sed -n "/<<$1\$/,/^$1\$/p" "$SCRIPT" | sed '1d;$d'
}

# render <terminator> — re-run the heredoc in a fresh shell so ${vars} expand
# exactly as they would at boot. Variables come from the environment.
render() {
  {
    echo "cat <<$1"
    extract_heredoc "$1"
    echo "$1"
  } > "$TMP/render-$1.sh"
  bash "$TMP/render-$1.sh"
}

AGENT_STATE_CMD=/usr/local/bin/cspace-agent-state.sh
export AGENT_STATE_CMD
hooks="$(render HOOKS)"
[ -n "$hooks" ] || fail "no HOOKS heredoc found in the entrypoint"

export statusline_cmd=/usr/local/bin/cspace-statusline.sh
export hooks_block="$hooks"
settings="$(render JSON)"

printf '%s' "$settings" > "$TMP/settings.json"
jq -e . "$TMP/settings.json" >/dev/null \
  || fail "settings.json is not valid JSON with the hooks block: $settings"

# ── the pre-existing gate-suppression keys survive ────────────────────────
jq -e '.statusLine.command == "/usr/local/bin/cspace-statusline.sh"' "$TMP/settings.json" >/dev/null \
  || fail "statusLine lost"
jq -e '.permissions.defaultMode == "bypassPermissions"' "$TMP/settings.json" >/dev/null \
  || fail "permissions.defaultMode lost"
jq -e '.skipDangerousModePermissionPrompt == true' "$TMP/settings.json" >/dev/null \
  || fail "skipDangerousModePermissionPrompt lost"
jq -e '.enableAllProjectMcpServers == true' "$TMP/settings.json" >/dev/null \
  || fail "enableAllProjectMcpServers lost"

# ── every event in the design's table, and only those ─────────────────────
want_events='["Notification","PermissionRequest","PostToolUse","PreToolUse","SessionEnd","SessionStart","Stop","StopFailure","UserPromptSubmit"]'
jq -e --argjson want "$want_events" '(.hooks | keys) == $want' "$TMP/settings.json" >/dev/null \
  || fail "hook events are $(jq -c '.hooks | keys' "$TMP/settings.json"), want $want_events"

# ── no event has two entries, so no two hooks race to write one event ─────
jq -e '[.hooks[] | length] | max == 1' "$TMP/settings.json" >/dev/null \
  || fail "an event has more than one hook entry"

# ── the state each event records ──────────────────────────────────────────
check_state() {  # $1=event  $2=expected state
  jq -e --arg e "$1" --arg s "$2" \
    '.hooks[$e][0].hooks[0].command == "/usr/local/bin/cspace-agent-state.sh " + $s' \
    "$TMP/settings.json" >/dev/null \
    || fail "$1 does not record '$2': $(jq -c --arg e "$1" '.hooks[$e]' "$TMP/settings.json")"
}
check_state SessionStart starting
check_state UserPromptSubmit working
check_state PostToolUse working
check_state PreToolUse needs-input
check_state PermissionRequest needs-input
check_state Notification idle
check_state Stop idle
check_state StopFailure idle
check_state SessionEnd exited

# ── every hook is a command hook ──────────────────────────────────────────
jq -e '[.hooks[][] | .hooks[] | .type] | unique == ["command"]' "$TMP/settings.json" >/dev/null \
  || fail "a hook is not type=command"

# ── matchers: PreToolUse only on AskUserQuestion, Notification on idle ────
# Hooks matching the same event run in parallel, which is why the generic
# "working" comes from PostToolUse and PreToolUse is narrowed to the one
# tool that asks the user something.
jq -e '.hooks.PreToolUse[0].matcher == "AskUserQuestion"' "$TMP/settings.json" >/dev/null \
  || fail "PreToolUse is not narrowed to AskUserQuestion"
jq -e '.hooks.Notification[0].matcher == "idle_prompt"' "$TMP/settings.json" >/dev/null \
  || fail "Notification is not narrowed to idle_prompt"
jq -e '.hooks.PostToolUse[0].matcher == "*"' "$TMP/settings.json" >/dev/null \
  || fail "PostToolUse does not match every tool"

# ── with no state script in the image, the file is still valid JSON ───────
# An older/project image has no cspace-agent-state.sh; hooks pointed at a
# missing binary would fail on every single turn and show a banner.
export hooks_block=""
render JSON > "$TMP/settings-nohooks.json"
jq -e . "$TMP/settings-nohooks.json" >/dev/null \
  || fail "settings.json is invalid JSON with an empty hooks block: $(cat "$TMP/settings-nohooks.json")"
jq -e '.hooks == null' "$TMP/settings-nohooks.json" >/dev/null \
  || fail "empty hooks block still produced a hooks key"
jq -e '.statusLine.command == "/usr/local/bin/cspace-statusline.sh"' "$TMP/settings-nohooks.json" >/dev/null \
  || fail "statusLine lost on the no-hooks path"

echo "PASS"
