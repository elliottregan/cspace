#!/usr/bin/env bash
# Install Claude Code plugins for the sandbox.
#
# Two sources, merged + deduplicated:
#
#   1. CSPACE_PLUGINS_CONFIG env var — JSON of cspace's merged plugins
#      config (defaults.json layered with project .cspace.json):
#         { "enabled": bool, "install": ["plugin", "plugin@market", ...] }
#      Bare plugin names default to @claude-plugins-official.
#      Toggle off by setting "enabled": false in .cspace.json.
#
#   2. /workspace/.claude/settings.json — Claude Code's native
#      enabledPlugins map: { "<plugin>@<marketplace>": true|false }.
#      Useful when a project already declares plugins for IDE / hosted
#      claude usage; the sandbox installs them without duplicate config.
#
# Both sources contribute to the install set. The cspace toggle disables
# both. Idempotent — already-installed plugins are skipped via
# `claude plugins list`.

set -uo pipefail

LOG="${HOME}/.claude/cspace-install-plugins.log"
mkdir -p "$(dirname "$LOG")"
exec > >(tee -a "$LOG") 2>&1

echo "[$(date -Iseconds)] cspace-install-plugins: start"

# Run a `claude plugins ...` invocation with a bounded timeout and one
# retry. These calls fetch from GitHub (marketplace add) or a marketplace's
# backing repo (plugin install) and can hang indefinitely on a slow/stalled
# network — see cs-finding
# 2026-07-19-plugins-marketplace-add-can-stall-boot-past-health-wait, where
# an unbounded `marketplace add` held up boot for minutes. Plugins are an
# enhancement, not boot-critical: a persistent failure (timeout or exit
# error, twice in a row) logs a warning and lets the script continue rather
# than stalling — or failing — the sandbox boot.
run_bounded() {
    local desc="$1"
    shift
    if timeout 120 "$@"; then
        return 0
    fi
    echo "[install-plugins] retrying ${desc} after timeout/failure"
    if timeout 120 "$@"; then
        return 0
    fi
    echo "[install-plugins] WARNING: ${desc} failed after retry; continuing without ${desc}"
    return 0
}

if ! command -v jq >/dev/null 2>&1; then
    echo "  jq missing; cannot parse plugin config — skipping"
    exit 0
fi
if ! command -v claude >/dev/null 2>&1; then
    echo "  claude missing; nothing to install — skipping"
    exit 0
fi

# Honor the cspace-side toggle. plugins.enabled is a tri-state
# (true / false / unset); only an explicit "false" disables.
cspace_cfg="${CSPACE_PLUGINS_CONFIG:-}"
if [ -n "$cspace_cfg" ]; then
    if [ "$(echo "$cspace_cfg" | jq -r '.enabled // true')" = "false" ]; then
        echo "  plugins.enabled = false in .cspace.json — skipping"
        exit 0
    fi
fi

# --- cspace-browser plugin (image-local marketplace) ---
# Register + install cspace's browser MCP plugin from the marketplace baked
# into the image. Gated on a browser sidecar being present
# (CSPACE_BROWSER_CDP_URL set) — this reproduces the old entrypoint behavior of
# not exposing browser tools that can't reach a CDP endpoint. Self-contained:
# does NOT go through the WANT/MARKETPLACES loop (that loop assumes GitHub
# owner/repo marketplaces and would mishandle a local filesystem path).
# Idempotent: `marketplace list` grep skips re-add; `plugins install` no-ops if
# already installed. CSPACE_BROWSER_MARKET_DIR is overridable for tests.
CSPACE_BROWSER_MARKET_DIR="${CSPACE_BROWSER_MARKET_DIR:-/opt/cspace/plugins}"
if [ -n "${CSPACE_BROWSER_CDP_URL:-}" ] && [ -f "${CSPACE_BROWSER_MARKET_DIR}/.claude-plugin/marketplace.json" ]; then
    if ! claude plugins marketplace list 2>/dev/null | grep -q "^[ *]*cspace\b"; then
        echo "[install-plugins] adding image-local marketplace cspace from ${CSPACE_BROWSER_MARKET_DIR}"
        run_bounded "marketplace cspace" claude plugins marketplace add "${CSPACE_BROWSER_MARKET_DIR}"
    fi
    echo "[install-plugins] installing cspace-browser@cspace"
    run_bounded "cspace-browser@cspace plugin" claude plugins install --scope user "cspace-browser@cspace"
fi
# --- end cspace-browser ---

# Build the combined enable list. Each entry is normalized to
# "<plugin>@<marketplace>"; bare names from cspace's plugins.install
# get @claude-plugins-official appended.
declare -A WANT=()

# Source 1: cspace plugins.install
if [ -n "$cspace_cfg" ]; then
    while IFS= read -r p; do
        [ -z "$p" ] && continue
        case "$p" in
            *@*) WANT["$p"]=1 ;;
            *)   WANT["${p}@claude-plugins-official"]=1 ;;
        esac
    done < <(echo "$cspace_cfg" | jq -r '.install // [] | .[]' 2>/dev/null || true)
fi

# Source 2: /workspace/.claude/settings.json enabledPlugins
SETTINGS="/workspace/.claude/settings.json"
if [ -f "$SETTINGS" ]; then
    while IFS= read -r p; do
        [ -z "$p" ] && continue
        WANT["$p"]=1
    done < <(jq -r '
        (.enabledPlugins // {})
        | to_entries[]
        | select(.value == true)
        | .key
    ' "$SETTINGS" 2>/dev/null || true)
fi

if [ "${#WANT[@]}" -eq 0 ]; then
    echo "  no plugins to install"
    exit 0
fi

# Group by marketplace so each is registered exactly once before
# installing its plugins. Non-official marketplaces are guessed as
# anthropics/<name> on GitHub; failures are bounded + warned (run_bounded),
# not silent.
declare -A MARKETPLACES=()
for entry in "${!WANT[@]}"; do
    MARKETPLACES["${entry##*@}"]=1
done
already_markets=$(claude plugins marketplace list 2>/dev/null || true)
for marketplace in "${!MARKETPLACES[@]}"; do
    if echo "$already_markets" | grep -q "^[ *]*${marketplace}\b"; then
        continue
    fi
    case "$marketplace" in
        claude-plugins-*) repo="anthropics/${marketplace}" ;;
        *)                repo="${marketplace}" ;;
    esac
    echo "[install-plugins] adding marketplace ${marketplace} from ${repo}"
    run_bounded "marketplace ${marketplace}" claude plugins marketplace add "$repo"
done

# Install. claude plugins install is idempotent — already-installed
# plugins skip with a notice, so we don't pre-filter against the list.
#
# Per-plugin progress to /sessions/cspace-init.status as we go, so the
# host overlay's polling loop can show "installing 3/12: github" type
# sub-labels under the "installing claude plugins" phase line. Format:
#   plugins:<i>/<total>:<plugin-name>
TOTAL=${#WANT[@]}
i=0
for entry in "${!WANT[@]}"; do
    i=$((i + 1))
    short="${entry%@*}"  # strip @marketplace for compact display
    echo "plugins:${i}/${TOTAL}:${short}" > /sessions/cspace-init.status 2>/dev/null || true
    echo "[install-plugins] installing (${i}/${TOTAL}): ${entry}"
    run_bounded "plugin ${entry}" claude plugins install --scope user "$entry"
done

echo "[$(date -Iseconds)] cspace-install-plugins: done (${TOTAL} plugins requested)"
