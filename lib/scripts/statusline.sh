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
# Read .cspace.json container.ports = { "<port>": "<label>", ... }, probe
# each port for a listening socket inside the sandbox, and emit a clickable
# URL via the sandbox's daemon-DNS hostname. Skipped if jq or .cspace.json
# is missing, or no labeled port is listening.
label_color() {
    case "$1" in
        dev)        printf '%s' "$GRN" ;;
        preview)    printf '%s' "$YLW" ;;
        brainstorm) printf '%s' "$MAG" ;;
        *)          printf '%s' "$CYN" ;;
    esac
}
CSPACE_JSON="/workspace/.cspace.json"
if [ -n "$CONTAINER" ] && [ -f "$CSPACE_JSON" ] && command -v jq >/dev/null 2>&1; then
    while IFS=$'\t' read -r internal_port label; do
        [ -z "$internal_port" ] && continue
        # Only show URLs for ports actually listening right now.
        ss -tlnp 2>/dev/null | grep -q ":${internal_port} " || continue
        URL="http://${CONTAINER}.cspace2.local:${internal_port}"
        printf "%s" "$DIV"
        printf "%s● " "$(label_color "$label")"
        link "$URL" "$label"
        printf "%s" "$RST"
    done < <(jq -r '.container.ports // {} | to_entries[] | "\(.key)\t\(.value)"' "$CSPACE_JSON" 2>/dev/null)
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
