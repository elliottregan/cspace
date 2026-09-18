#!/usr/bin/env bash
# Tests the settings.json seed in cspace-entrypoint.sh without running the
# entrypoint itself (which wants sudo, iptables, dnsmasq and a network).
#
# The JSON heredoc is extracted from the script and re-rendered in a fresh
# bash with the variables the entrypoint would have set, then asserted with
# jq. That catches the thing editing a JSON heredoc actually breaks: a stray
# comma, a missing brace, a hook pointed at the wrong path. The
# cspace_hooks_block function is extracted separately and called directly, so
# its own executable gate — not just the JSON it emits when the gate passes —
# is exercised for real.
#
# Both extractions key off exact text staying unique in the entrypoint: the
# HOOKS/JSON heredoc terminators, and the "cspace_hooks_block() {" / "}"
# function boundary. If any of those stop being unique, extraction silently
# grabs the wrong span instead of failing loudly.
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$HERE/cspace-entrypoint.sh"
fail() { echo "FAIL: $1"; exit 1; }

if ! command -v jq >/dev/null 2>&1; then
  echo "SKIP: jq not installed"
  exit 0
fi

# ── the AGENT_STATE_CMD assignment stays literal ───────────────────────────
# So this constant and the Dockerfile's COPY destination
# (/usr/local/bin/cspace-agent-state.sh) can't drift apart silently.
grep -q '^AGENT_STATE_CMD=/usr/local/bin/cspace-agent-state.sh$' "$SCRIPT" \
  || fail "cspace-entrypoint.sh's AGENT_STATE_CMD assignment is missing or changed"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# extract_heredoc <terminator> — the body between `<<TERM` and the closing
# `TERM`, exclusive.
extract_heredoc() {
  sed -n "/<<$1\$/,/^$1\$/p" "$SCRIPT" | sed '1d;$d'
}

# extract_function <name> — the function body from "<name>() {" through the
# matching closing "}" at column 0.
extract_function() {
  sed -n "/^$1() {\$/,/^}\$/p" "$SCRIPT"
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

FUNC_SRC="$(extract_function cspace_hooks_block)"
[ -n "$FUNC_SRC" ] || fail "cspace_hooks_block function not found in the entrypoint"

# call_hooks_block <cmd-arg> — defines the extracted cspace_hooks_block in a
# fresh bash and calls it with $1 as the command path, so the function's own
# "[ -x ]" gate runs for real rather than being assumed by the test.
call_hooks_block() {
  {
    printf '%s\n' "$FUNC_SRC"
    printf 'cspace_hooks_block %q\n' "$1"
  } > "$TMP/call-hooks.sh"
  bash "$TMP/call-hooks.sh"
}

# ── the executable branch: full nine-event hooks JSON ─────────────────────
EXEC_CMD="$TMP/fake-agent-state.sh"
: > "$EXEC_CMD"
chmod +x "$EXEC_CMD"

hooks="$(call_hooks_block "$EXEC_CMD")"
[ -n "$hooks" ] || fail "cspace_hooks_block produced nothing for an executable command"

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
  jq -e --arg e "$1" --arg s "$2" --arg cmd "$EXEC_CMD" \
    '.hooks[$e][0].hooks[0].command == $cmd + " " + $s' \
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
# SessionStart fires on compact and fork too; without this narrowing either
# would flip an already-working session's state back to "starting".
jq -e '.hooks.SessionStart[0].matcher == "startup|resume|clear"' "$TMP/settings.json" >/dev/null \
  || fail "SessionStart is not narrowed to startup|resume|clear"

# ── the five non-matcher events carry no matcher key at all ───────────────
jq -e '[.hooks.UserPromptSubmit[0], .hooks.PermissionRequest[0], .hooks.Stop[0], .hooks.StopFailure[0], .hooks.SessionEnd[0]] | all(has("matcher") | not)' "$TMP/settings.json" >/dev/null \
  || fail "a non-matcher event carries a matcher key"

# ── the gate closed: a non-executable file ─────────────────────────────────
# An older/project image has no cspace-agent-state.sh; hooks pointed at a
# missing binary would fail on every single turn and show a banner. Exercise
# the function's actual "[ -x ]" check, not a stand-in for it.
NONEXEC_CMD="$TMP/fake-agent-state-nonexec.sh"
: > "$NONEXEC_CMD"
chmod -x "$NONEXEC_CMD"
nonexec_out="$(call_hooks_block "$NONEXEC_CMD")"
[ -z "$nonexec_out" ] || fail "cspace_hooks_block emitted something for a non-executable command: $nonexec_out"

export hooks_block="$nonexec_out"
render JSON > "$TMP/settings-nonexec.json"
jq -e . "$TMP/settings-nonexec.json" >/dev/null \
  || fail "settings.json is invalid JSON with a non-executable state script: $(cat "$TMP/settings-nonexec.json")"
jq -e '.hooks == null' "$TMP/settings-nonexec.json" >/dev/null \
  || fail "a non-executable state script still produced a hooks key"
jq -e '.statusLine.command == "/usr/local/bin/cspace-statusline.sh"' "$TMP/settings-nonexec.json" >/dev/null \
  || fail "statusLine lost on the non-executable path"

# ── the gate closed: a path that doesn't exist at all ──────────────────────
MISSING_CMD="$TMP/does-not-exist.sh"
missing_out="$(call_hooks_block "$MISSING_CMD")"
[ -z "$missing_out" ] || fail "cspace_hooks_block emitted something for a nonexistent command: $missing_out"

export hooks_block="$missing_out"
render JSON > "$TMP/settings-missing.json"
jq -e . "$TMP/settings-missing.json" >/dev/null \
  || fail "settings.json is invalid JSON with a missing state script: $(cat "$TMP/settings-missing.json")"
jq -e '.hooks == null' "$TMP/settings-missing.json" >/dev/null \
  || fail "a missing state script still produced a hooks key"
jq -e '.statusLine.command == "/usr/local/bin/cspace-statusline.sh"' "$TMP/settings-missing.json" >/dev/null \
  || fail "statusLine lost on the missing-script path"

echo "PASS"
