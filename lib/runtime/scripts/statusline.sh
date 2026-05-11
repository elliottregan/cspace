#!/bin/bash
# cspace statusline — context bar, planet glyph, repo + branch + PR + cost.
# Receives JSON context from Claude Code via stdin.
#
# v1: container detection uses $CSPACE_SANDBOX_NAME (set by cspace up).
# Traefik / docker-port service URLs are dropped — that wiring belongs to
# v0; v1 routes via cspace dns + direct microVM IPs and per-project
# service support hasn't landed yet. When it does, restore the URL line
# from the v0 statusline (lib/scripts/statusline.sh in the cspace
# pre-cutover history).

input=$(cat)

# --- Parse JSON input ---
MODEL=$(echo "$input" | jq -r '.model.display_name // "?"')
COST=$(echo "$input" | jq -r '.cost.total_cost_usd // 0 | "$\(. * 100 | round / 100 | tostring | if test("\\.") then . else . + ".00" end)"')
CTX_SIZE=$(echo "$input" | jq -r '.context_window.context_window_size // 0')
SESSION_ID=$(echo "$input" | jq -r '.session_id // ""')
TRANSCRIPT=$(echo "$input" | jq -r '.transcript_path // ""')
AGENT_NAME=$(echo "$input" | jq -r '.agent.name // ""')
CWD=$(echo "$input" | jq -r '.cwd // ""')
if [ -n "$SESSION_ID" ] && [ -f "$TRANSCRIPT" ]; then
    TURNS=$(grep "\"sessionId\":\"$SESSION_ID\"" "$TRANSCRIPT" | jq -r 'select(.type == "user") | .type' 2>/dev/null | wc -l | tr -d ' ')
else
    TURNS=0
fi
PCT=$(echo "$input" | jq -r '.context_window.used_percentage // 0' | cut -d. -f1)
CTX_USED=$((PCT * CTX_SIZE / 100))

# Autocompact buffer reserve: 20% of total context is unavailable
# Effective usable context = total - reserved; percentage is relative to that
AUTOCOMPACT_RESERVE_PCT=200  # 20% in tenths-of-a-percent for integer math
EFFECTIVE_CTX=$((CTX_SIZE * (1000 - AUTOCOMPACT_RESERVE_PCT) / 1000))
[ "$EFFECTIVE_CTX" -le 0 ] && EFFECTIVE_CTX=1
EFF_PCT=$((CTX_USED * 100 / EFFECTIVE_CTX))
[ "$EFF_PCT" -gt 100 ] && EFF_PCT=100

# --- Colors ---
# NOTE: $'...' (ANSI-C quoting) is REQUIRED — it stores a real ESC byte (0x1B).
# Plain '\033[...]' stores the 4-char literal \033, which prints as text whenever
# the var is passed to printf '%s' (e.g. the "$DIV" separator below).
# Do NOT let a shell formatter rewrite these to single quotes.
RST=$'\033[0m'; GRY=$'\033[90m'; DIM=$'\033[38;5;238m'; CYN=$'\033[36m'
GRN=$'\033[32m'; YLW=$'\033[33m'; RED=$'\033[31m'; MAG=$'\033[35m'
ORG=$'\033[38;2;218;119;86m' # Claude orange #DA7756

# --- NerdFont icons (hex escapes so bytes survive editor round-trips) ---
NF_BRANCH=$(printf '\xf3\xb0\x98\xac') # U+F062C 󰘬
NF_PR=$(printf '\xee\xa9\xa4')         # U+EA64
NF_FOLDER=$(printf '\xef\x81\xbc')     # U+F07C

# --- Context bar (color by threshold, relative to effective usable context) ---
if [ "$EFF_PCT" -ge 90 ]; then BC="$RED"
elif [ "$EFF_PCT" -ge 70 ]; then BC="$YLW"
else BC="$GRN"; fi
FILLED=$((EFF_PCT * 6 / 100))
BAR=""; i=0; while [ "$i" -lt 6 ]; do
    [ "$i" -lt "$FILLED" ] && BAR="${BAR}▰" || BAR="${BAR}▱"
    i=$((i + 1))
done

# --- Git info (run from Claude's CWD so branch/dirty state match) ---
GIT="git"
[ -n "$CWD" ] && GIT="git -C $CWD"
BRANCH=$($GIT branch --show-current 2>/dev/null)
DIRTY=$($GIT diff --quiet 2>/dev/null && $GIT diff --cached --quiet 2>/dev/null || echo "*")
STAGED=$($GIT diff --cached --numstat 2>/dev/null | wc -l | tr -d ' ')
UNSTAGED=$($GIT diff --numstat 2>/dev/null | wc -l | tr -d ' ')
UNTRACKED=$($GIT ls-files --others --exclude-standard 2>/dev/null | wc -l | tr -d ' ')
AHEAD=$($GIT rev-list --count @{u}..HEAD 2>/dev/null || echo 0)
BEHIND=$($GIT rev-list --count HEAD..@{u} 2>/dev/null || echo 0)

# Default branch detection: prefer the remote HEAD, fall back to "main".
DEFAULT_BRANCH=$($GIT symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's|^refs/remotes/origin/||')
[ -z "$DEFAULT_BRANCH" ] && DEFAULT_BRANCH=main
BEHIND_DEFAULT=0
if [ -n "$BRANCH" ] && [ "$BRANCH" != "$DEFAULT_BRANCH" ]; then
    BEHIND_DEFAULT=$($GIT rev-list --count "HEAD..origin/$DEFAULT_BRANCH" 2>/dev/null || echo 0)
fi

# --- PR link + check status (cached 2 min per branch) ---
CACHE_DIR="${TMPDIR:-/tmp}/cspace-statusline"
mkdir -p "$CACHE_DIR" 2>/dev/null
PR_LINK=""
PR_CHECK_STATUS=""
PR_LOOKUP_BRANCH="$BRANCH"
if [ -n "$PR_LOOKUP_BRANCH" ] && command -v gh >/dev/null 2>&1; then
    CACHE_KEY=$(echo "$PR_LOOKUP_BRANCH" | tr '/' '-')
    CACHE_FILE="$CACHE_DIR/$CACHE_KEY"
    NOW=$(date +%s)
    # stat -f %m = macOS, stat -c %Y = Linux
    MTIME=$(stat -c %Y "$CACHE_FILE" 2>/dev/null || stat -f %m "$CACHE_FILE" 2>/dev/null || echo 0)
    AGE=$((NOW - MTIME))

    if [ -f "$CACHE_FILE" ] && [ "$AGE" -lt 120 ]; then
        PR_LINK=$(cut -f1 "$CACHE_FILE")
        PR_CHECK_STATUS=$(cut -f2 "$CACHE_FILE")
    else
        PR_DATA=$(gh pr view "$PR_LOOKUP_BRANCH" \
            --json url,mergeStateStatus \
            -q '[.url, (
                  if   .mergeStateStatus == "CLEAN"    then "success"
                  elif .mergeStateStatus == "BLOCKED"  then "failure"
                  elif .mergeStateStatus == "DIRTY"    then "failure"
                  elif .mergeStateStatus == "BEHIND"   then "pending"
                  elif .mergeStateStatus == "UNSTABLE" then "pending"
                  else "none" end
                )] | @tsv' 2>/dev/null || true)
        PR_LINK=$(printf '%s' "$PR_DATA" | cut -f1)
        PR_CHECK_STATUS=$(printf '%s' "$PR_DATA" | cut -f2)
        printf '%s\t%s\n' "$PR_LINK" "$PR_CHECK_STATUS" > "$CACHE_FILE" 2>/dev/null
    fi
fi

# --- Helper: clickable terminal hyperlink (OSC 8) ---
link() { printf '\033]8;;%s\033\\%s\033]8;;\033\\' "$1" "$2"; }

# --- Sandbox / planet detection (v1: use cspace env vars, not /.dockerenv) ---
CONTAINER="${CSPACE_SANDBOX_NAME:-}"
PROJECT="${CSPACE_PROJECT:-}"
PLANET_SYMBOL=""
PLANET_COLOR=""
case "$CONTAINER" in
    mercury)  PLANET_SYMBOL="☿"; PLANET_COLOR=$'\033[38;2;169;169;169m' ;;
    venus)    PLANET_SYMBOL="♀"; PLANET_COLOR=$'\033[38;2;237;214;153m' ;;
    earth)    PLANET_SYMBOL="♁"; PLANET_COLOR=$'\033[38;2;78;159;222m'  ;;
    mars)     PLANET_SYMBOL="♂"; PLANET_COLOR=$'\033[38;2;193;68;14m'   ;;
    jupiter)  PLANET_SYMBOL="♃"; PLANET_COLOR=$'\033[38;2;200;133;44m'  ;;
    saturn)   PLANET_SYMBOL="♄"; PLANET_COLOR=$'\033[38;2;212;180;131m' ;;
    uranus)   PLANET_SYMBOL="♅"; PLANET_COLOR=$'\033[38;2;127;223;223m' ;;
    neptune)  PLANET_SYMBOL="♆"; PLANET_COLOR=$'\033[38;2;63;84;186m'   ;;
    "")       ;;
    *)        PLANET_SYMBOL="●"; PLANET_COLOR=$'\033[38;2;128;128;128m' ;; # custom names get a neutral dot
esac

# --- Line 1: [planet] sandbox (project) | dir | branch | PR ---
DISPLAY_CWD="${CWD:-$PWD}"
DIR=${DISPLAY_CWD/#$HOME/\~}
DIV=" ${DIM}|${RST} "
if [ -n "$CONTAINER" ]; then
    printf "${PLANET_COLOR}%s %s${RST}" "$PLANET_SYMBOL" "$CONTAINER"
    [ -n "$PROJECT" ] && printf " ${GRY}(%s)${RST}" "$PROJECT"
    printf "%s" "$DIV"
fi
printf "${GRY}%s${RST} %s" "$NF_FOLDER" "$DIR"
if [ -n "$BRANCH" ]; then
    printf "%s" "$DIV"
    printf "${GRY}${NF_BRANCH}${RST} %s%s" "$BRANCH" "$DIRTY"
    [ "$STAGED" -gt 0 ] 2>/dev/null && printf " ${GRN}+%s${RST}" "$STAGED"
    [ "$UNSTAGED" -gt 0 ] 2>/dev/null && printf " ${YLW}~%s${RST}" "$UNSTAGED"
    [ "$UNTRACKED" -gt 0 ] 2>/dev/null && printf " ${RED}?%s${RST}" "$UNTRACKED"
    [ "$AHEAD" -gt 0 ] 2>/dev/null && printf " ${CYN}↑%s${RST}" "$AHEAD"
    [ "$BEHIND" -gt 0 ] 2>/dev/null && printf " ${YLW}↓%s${RST}" "$BEHIND"
    [ "$BEHIND_DEFAULT" -gt 0 ] 2>/dev/null && printf " ${MAG}↓%s %s${RST}" "$BEHIND_DEFAULT" "$DEFAULT_BRANCH"
fi
if [ -n "$PR_LINK" ]; then
    PR_NUM=$(echo "$PR_LINK" | grep -o '[0-9]*$')
    case "$PR_CHECK_STATUS" in
        failure) PR_IC="$RED" ;;
        pending) PR_IC="$YLW" ;;
        success) PR_IC="$GRN" ;;
        *)       PR_IC="$GRY" ;;
    esac
    printf "%s" "$DIV"
    printf "${PR_IC}${NF_PR}${RST} "; link "$PR_LINK" "#${PR_NUM}"
fi

# --- Service URLs ---
# Project ownership model: .cspace.json's container.ports drives what
# shows up here. When the project declares ports, ONLY those are shown
# (with their labels). Without that section, we fall back to auto-
# discovery so a fresh project still sees its dev server URL — but the
# tradeoff is noise: dnsmasq's :53, Vite's :24678 HMR socket, etc. all
# leak through. Add the section to .cspace.json the first time a noisy
# port shows up.
#
# Colors: dev=green, preview=yellow, brainstorm=magenta; anything else
# is cyan. Labels are clickable terminal hyperlinks via OSC 8.
label_color() {
    case "$1" in
        dev)        printf '%s' "$GRN" ;;
        preview)    printf '%s' "$YLW" ;;
        brainstorm) printf '%s' "$MAG" ;;
        *)          printf '%s' "$CYN" ;;
    esac
}
# Ports cspace owns internally that should never show in the statusline.
# 6201: cspace-supervisor's control socket. 53: dnsmasq forwarder for
# *.cspace.local DNS routing. Both are cspace-managed plumbing, not the
# user's dev servers.
SUPERVISOR_PORT=6201
INTERNAL_PORTS="6201 53"
if [ -n "$CONTAINER" ] && command -v ss >/dev/null 2>&1; then
    # Build a port→label map. Devcontainer.json's portsAttributes is the
    # preferred source: it's the standard format and survives a future
    # deprecation of .cspace.json's container block. Fall back to
    # .cspace.json container.ports for older projects (still supported,
    # but cspace itself prints a warning when both .cspace.json container
    # and devcontainer.json are present).
    #
    # Bash 4+ associative array — sandbox image is debian-bookworm.
    declare -A PORT_LABELS=()
    DEVCONTAINER_JSON="/workspace/.devcontainer/devcontainer.json"
    CSPACE_JSON="/workspace/.cspace.json"
    if [ -f "$DEVCONTAINER_JSON" ] && command -v jq >/dev/null 2>&1; then
        # devcontainer.json is JSONC: strip // and /* ... */ comments before
        # piping to jq. portsAttributes uses { "<port>": { "label": "<x>" } };
        # we keep only entries that actually have a label string.
        DEVC=$(sed -E 's://[^"]*$::; s:/\*[^*]*\*+([^/*][^*]*\*+)*/::g' "$DEVCONTAINER_JSON" 2>/dev/null)
        while IFS=$'\t' read -r p lbl; do
            [ -n "$p" ] && [ -n "$lbl" ] && PORT_LABELS["$p"]="$lbl"
        done < <(echo "$DEVC" | jq -r '.portsAttributes // {} | to_entries[] | select(.value.label) | "\(.key)\t\(.value.label)"' 2>/dev/null)
    fi
    # Backwards-compat fallback: .cspace.json container.ports (legacy).
    # Only consulted when devcontainer.json didn't supply labels.
    if [ "${#PORT_LABELS[@]}" -eq 0 ] && [ -f "$CSPACE_JSON" ] && command -v jq >/dev/null 2>&1; then
        while IFS=$'\t' read -r p lbl; do
            [ -n "$p" ] && [ -n "$lbl" ] && PORT_LABELS["$p"]="$lbl"
        done < <(jq -r '.container.ports // {} | to_entries[] | "\(.key)\t\(.value)"' "$CSPACE_JSON" 2>/dev/null)
    fi
    # Curation: hide unlabeled ports only when the project actually
    # provided labels. An empty config object (e.g. legacy .cspace.json
    # with `"container": { "ports": {} }`) should NOT trigger the gate —
    # it provides no signal of what the user cares about, so we fall
    # back to "show all listening ports".
    HAS_PORT_CONFIG=0
    [ "${#PORT_LABELS[@]}" -gt 0 ] && HAS_PORT_CONFIG=1
    # Enumerate listening TCP ports. ss prints lines like:
    #   LISTEN 0 511 *:5173 *:*
    #   LISTEN 0 511 127.0.0.1:5174 0.0.0.0:*
    # We don't care about the bind address — the entrypoint's iptables
    # PREROUTING DNAT rewrites incoming traffic to 127.0.0.1 so loopback
    # listeners (vite's default) are reachable from outside the microVM.
    LISTENING_PORTS=$(ss -tln 2>/dev/null \
        | awk 'NR>1 {n=split($4,a,":"); print a[n]}' \
        | sort -un)
    # Project-qualified hostname so two projects can run a sandbox with
    # the same name without DNS collision. Falls back to sandbox-only
    # form when CSPACE_PROJECT is unset (older sandboxes pre-rc.5).
    if [ -n "$PROJECT" ]; then
        FQDN="${CONTAINER}.${PROJECT}.cspace.local"
    else
        FQDN="${CONTAINER}.cspace.local"
    fi
    for port in $LISTENING_PORTS; do
        [ -z "$port" ] && continue
        # Skip cspace-internal ports (supervisor, dnsmasq, …).
        skip=0
        for ip in $INTERNAL_PORTS; do
            [ "$port" = "$ip" ] && skip=1 && break
        done
        [ "$skip" -eq 1 ] && continue
        label="${PORT_LABELS[$port]:-}"
        # Curation gate: when port config is present, hide any listening
        # port that's not labeled. This is the user's explicit "I know
        # what URLs I care about" signal.
        if [ "$HAS_PORT_CONFIG" -eq 1 ] && [ -z "$label" ]; then
            continue
        fi
        URL="http://${FQDN}:${port}"
        # Visible text is the full URL (with http:// prefix) so terminals
        # that don't honor OSC 8 still auto-linkify via URL pattern
        # matching (cmd-click on URLs). When OSC 8 IS honored, the
        # surrounding hyperlink wrapper makes the same text clickable
        # directly. Either way the user can click. Earlier iterations
        # used short text ("dev", then host:port without scheme); both
        # failed to auto-link in renderers that strip OSC 8 (some
        # Claude-Code statusline contexts), forcing users to type the
        # URL by hand.
        printf "%s" "$DIV"
        if [ -n "$label" ]; then
            printf "%s● %s%s " "$(label_color "$label")" "$label" "$RST"
        else
            printf "%s● %s" "$CYN" "$RST"
        fi
        printf "%s" "$CYN"
        link "$URL" "$URL"
        printf "%s" "$RST"
    done
fi
echo

# --- Format token counts as K ---
fmt_k() { echo $(( ($1 + 500) / 1000 ))K; }

# --- Line 2: context bar | model | turns | cost ---
printf "${BC}%s${RST} %s/%s" "$BAR" "$(fmt_k "$CTX_USED")" "$(fmt_k "$CTX_SIZE")"
printf "%s" "$DIV"
printf "${ORG}%s${RST}" "$MODEL"
[ -n "$AGENT_NAME" ] && printf " ${GRY}(%s)${RST}" "$AGENT_NAME"
printf "%s" "$DIV"
printf "${GRY}turns:${RST}%s" "$TURNS"
printf "%s" "$DIV"
printf "%s" "$COST"
echo
