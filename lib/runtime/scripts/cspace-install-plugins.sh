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

# Drop plugins cspace itself provides via the supervisor's --mcp-config.
# Otherwise the agent ends up with two `playwright` MCP servers — cspace's
# (correctly pointed at the browser sidecar) and the @claude-plugins-official
# plugin's (no connect args, tries to launch a local Chromium that isn't
# installed). The plugin loses every time, so just don't install it.
if [ -n "${CSPACE_BROWSER_CDP_URL:-}" ]; then
    unset 'WANT[playwright@claude-plugins-official]'
fi

# Group by marketplace so each is registered exactly once before
# installing its plugins. Non-official marketplaces are guessed as
# anthropics/<name> on GitHub; failures are silent.
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
    claude plugins marketplace add "$repo" || true
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
    claude plugins install --scope user "$entry" || true
done

echo "[$(date -Iseconds)] cspace-install-plugins: done (${TOTAL} plugins requested)"

# When cspace's browser sidecar is providing Chrome (CSPACE_BROWSER_CDP_URL
# set), the official @claude-plugins-official `playwright` plugin is
# strictly redundant: cspace's supervisor (claude-runner.ts) already
# registers `playwright` via `--mcp-config` with the right
# `--cdp-endpoint` argv pointing at the sidecar. The plugin's stock
# .mcp.json runs `npx @playwright/mcp@latest` with no connect args, so
# its MCP shows up under a `mcp__plugin_playwright_playwright__*`
# namespace and tries to launch a local Chromium the lean sandbox image
# doesn't ship — agents that grab the plugin tools fail with "browser
# not found". Two `playwright` MCP servers is exactly the surprise we
# don't want.
#
# Uninstall it. cspace's claude-runner registration becomes the single
# `playwright` namespace the agent sees, which is wired correctly.
if [ -n "${CSPACE_BROWSER_CDP_URL:-}" ]; then
    pw_entry="playwright@claude-plugins-official"
    if [ -f "$HOME/.claude/plugins/installed_plugins.json" ] && \
       grep -q "\"$pw_entry\"" "$HOME/.claude/plugins/installed_plugins.json"; then
        echo "[install-plugins] cspace browser sidecar active; uninstalling redundant playwright plugin"
        # Both scopes can show up: --scope user from a prior cspace install,
        # --scope project auto-registered by claude when it scans the
        # marketplace at startup. Run both unconditionally; each is a no-op
        # when not present.
        for scope in user project; do
            ( cd /workspace 2>/dev/null || true
              claude plugins uninstall "$pw_entry" --scope "$scope" 2>&1 | sed 's/^/  /'
            ) || true
        done
    fi
fi
