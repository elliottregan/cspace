#!/usr/bin/env bash
# Install Claude Code plugins declared in /workspace/.claude/settings.json.
#
# Reads enabledPlugins of the form "<plugin>@<marketplace>", groups by
# marketplace, and runs `claude plugins install` for each. Idempotent:
# already-installed plugins are skipped. Marketplaces not pre-baked into
# the image are added on demand if their name matches a known
# convention (org/repo-name on GitHub).
#
# Designed to be quick when the marketplace clones are pre-baked into
# the image — install becomes a local file-copy under ~/.claude/plugins/.
# First boot of a sandbox takes a few seconds; subsequent boots are
# near-instant because installed_plugins.json is in the image's dev
# user home (re-created on every container, but local copies are fast).

set -uo pipefail
# Don't `set -e` — we want best-effort behavior across plugins; one
# failure shouldn't abort the rest. Errors get teed to a log file the
# user can inspect with `container logs <sandbox>` or directly via
# /var/log/cspace-install-plugins.log.

LOG="${HOME}/.claude/cspace-install-plugins.log"
mkdir -p "$(dirname "$LOG")"
exec > >(tee -a "$LOG") 2>&1

echo "[$(date -Iseconds)] cspace-install-plugins: start"

SETTINGS="/workspace/.claude/settings.json"

# Skip silently when the project doesn't declare any plugins.
if [ ! -f "$SETTINGS" ]; then
    exit 0
fi
if ! command -v jq >/dev/null 2>&1; then
    exit 0
fi
if ! command -v claude >/dev/null 2>&1; then
    exit 0
fi

# enabledPlugins is { "<plugin>@<marketplace>": true|false, ... }.
# Filter to true-valued entries; emit one "<plugin>@<marketplace>" per line.
mapfile -t ENABLED < <(jq -r '
    (.enabledPlugins // {})
    | to_entries[]
    | select(.value == true)
    | .key
' "$SETTINGS" 2>/dev/null || true)

if [ "${#ENABLED[@]}" -eq 0 ]; then
    exit 0
fi

# Group by marketplace so we can ensure each marketplace is registered
# exactly once before installing its plugins.
declare -A MARKETPLACES=()
for entry in "${ENABLED[@]}"; do
    marketplace="${entry##*@}"
    [ -n "$marketplace" ] && MARKETPLACES["$marketplace"]=1
done

# Ensure each marketplace is known. The image pre-bakes
# claude-plugins-official under /opt/cspace/marketplaces/; for others
# we attempt a guess via the github.com/<marketplace>/<marketplace>
# convention and silently skip on failure — better to install partial
# plugins than to refuse to boot.
already=$(claude plugins marketplace list 2>/dev/null || true)
for marketplace in "${!MARKETPLACES[@]}"; do
    if echo "$already" | grep -q "^[ *]*${marketplace}\b"; then
        continue
    fi
    # claude-plugins-official auto-registers; non-official marketplaces
    # need an explicit add. The reserved-name rule (verified empirically:
    # `claude plugins marketplace add /path` fails with "The name
    # 'claude-plugins-official' is reserved...") means the pre-baked
    # local clone can't be the source for the official marketplace.
    # Use a github source guess.
    case "$marketplace" in
        claude-plugins-*) repo="anthropics/${marketplace}" ;;
        *)                repo="${marketplace}" ;;
    esac
    echo "[install-plugins] adding marketplace ${marketplace} from ${repo}"
    claude plugins marketplace add "$repo" || true
done

# Install each enabled plugin. claude plugins install is idempotent —
# already-installed plugins are skipped with a brief notice. Run with
# --scope user so installs land in the dev-user plugins dir, not the
# (read-only-ish) project workspace.
installed=$(claude plugins list 2>/dev/null || true)
for entry in "${ENABLED[@]}"; do
    if echo "$installed" | grep -q "^[ *]*${entry}\b"; then
        echo "[install-plugins] already installed: ${entry}"
        continue
    fi
    echo "[install-plugins] installing: ${entry}"
    claude plugins install --scope user "$entry" || true
done

echo "[$(date -Iseconds)] cspace-install-plugins: done"
