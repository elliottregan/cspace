#!/bin/bash
# Initialize Claude plugins and settings for cspace devcontainers.
# Configurable via CSPACE_CLAUDE_MODEL and CSPACE_CLAUDE_EFFORT env vars.

# Propagate exit codes through pipes so `if ! cmd | sed ...` detects cmd's
# failure rather than sed's (sed nearly always succeeds).
set -o pipefail

# Ensure claude CLI is in PATH (installed to ~/.local/bin by Claude Code installer,
# but entrypoint.sh runs this script before profile is sourced).
export PATH="/home/dev/.local/bin:$PATH"

PLUGINS_DIR="/home/dev/.claude/plugins"
HOST_PLUGINS_DIR="/tmp/host-claude-plugins"
MARKER_FILE="$PLUGINS_DIR/.initialized"
# CSPACE_HOME is set in the image (ENV in Dockerfile = /opt/cspace).
# The fallback exists only for sanity if someone runs this script standalone.
CSPACE_HOME="${CSPACE_HOME:-/opt/cspace}"

MODEL="${CSPACE_CLAUDE_MODEL:-claude-opus-4-6[1m]}"
EFFORT="${CSPACE_CLAUDE_EFFORT:-max}"

# Determine hook script paths — prefer project overrides, fall back to cspace defaults
progress_hook="/workspace/.cspace/hooks/claude-progress-logger.sh"
[ -x "$progress_hook" ] || progress_hook="$CSPACE_HOME/lib/hooks/claude-progress-logger.sh"

transcript_hook="/workspace/.cspace/hooks/claude-transcript-copy.sh"
[ -x "$transcript_hook" ] || transcript_hook="$CSPACE_HOME/lib/hooks/claude-transcript-copy.sh"

block_self_destruct_hook="/workspace/.cspace/hooks/block-self-destruct.sh"
[ -x "$block_self_destruct_hook" ] || block_self_destruct_hook="$CSPACE_HOME/lib/hooks/block-self-destruct.sh"

# Status line — project override takes precedence over the cspace default
statusline_cmd="/workspace/.cspace/scripts/statusline.sh"
[ -x "$statusline_cmd" ] || statusline_cmd="$CSPACE_HOME/lib/scripts/statusline.sh"

# Always write hooks (they reference workspace scripts that may change between runs)
USER_SETTINGS="/home/dev/.claude/settings.json"
mkdir -p /home/dev/.claude
cat > "$USER_SETTINGS" <<HOOKS_EOF
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{ "type": "command", "command": "$block_self_destruct_hook" }]
      }
    ],
    "SessionStart": [
      {
        "matcher": "",
        "hooks": [{ "type": "command", "command": "$progress_hook" }]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "",
        "hooks": [{ "type": "command", "command": "$progress_hook" }]
      }
    ],
    "Notification": [
      {
        "matcher": "",
        "hooks": [{ "type": "command", "command": "$progress_hook" }]
      }
    ],
    "SessionEnd": [
      {
        "matcher": "",
        "hooks": [
          { "type": "command", "command": "$progress_hook" },
          { "type": "command", "command": "$transcript_hook" }
        ]
      }
    ]
  },
  "statusLine": {
    "type": "command",
    "command": "$statusline_cmd"
  },
  "model": "$MODEL",
  "effortLevel": "$EFFORT"
}
HOOKS_EOF
chown dev:dev "$USER_SETTINGS"

# --- Built-in MCP servers ---
# Always registered (runs every startup, not gated by marker).
# Run as dev user so claude writes to /home/dev/.claude.json, not /root/.
CLAUDE_BIN="/home/dev/.local/bin/claude"

# Browser MCPs run inside the browser sidecar via docker exec, which has
# unrestricted network access (no firewall). The agent container communicates
# with them over stdio through the docker exec pipe.
BROWSER_CONTAINER="${CSPACE_CONTAINER_NAME}.browser"
if [ -n "$CSPACE_CONTAINER_NAME" ]; then
    echo "Registering browser MCP servers..."

    # Playwright MCP — browser automation via the sidecar's Chrome instance
    echo "  - playwright: registering"
    if ! sudo -u dev "$CLAUDE_BIN" mcp add --scope user playwright -- \
        docker exec -i "$BROWSER_CONTAINER" \
        npx --yes @playwright/mcp@latest \
        --cdp-endpoint http://localhost:9222 --no-sandbox 2>&1 | sed 's/^/      /'; then
        echo "  - playwright: registration failed (continuing)"
    fi

    # Chrome DevTools MCP — page inspection via CDP
    echo "  - chrome-devtools: registering"
    if ! sudo -u dev "$CLAUDE_BIN" mcp add --scope user chrome-devtools -- \
        docker exec -i "$BROWSER_CONTAINER" \
        npx --yes chrome-devtools-mcp@latest \
        --browserUrl http://localhost:9222 2>&1 | sed 's/^/      /'; then
        echo "  - chrome-devtools: registration failed (continuing)"
    fi
fi

# cspace-context MCP — project context brain (docs/context/)
# Independent of the browser sidecar; runs as a subprocess in the agent container.
echo "  - cspace-context: registering"
if ! sudo -u dev "$CLAUDE_BIN" mcp add --scope user cspace-context -- \
    cspace context-server --root /workspace 2>&1 | sed 's/^/      /'; then
    echo "  - cspace-context: registration failed (continuing)"
fi

# Skip plugin init if already done
if [ -f "$MARKER_FILE" ]; then
    exit 0
fi

# Fix ownership of plugins directory (volume may be owned by root)
sudo chown -R dev:dev "$PLUGINS_DIR" 2>/dev/null || true

mkdir -p "$PLUGINS_DIR"

# Copy and transform known_marketplaces.json if it exists
if [ -f "$HOST_PLUGINS_DIR/known_marketplaces.json" ]; then
    sed 's|/Users/[^/]*/\.claude|/home/dev/.claude|g' \
        "$HOST_PLUGINS_DIR/known_marketplaces.json" > "$PLUGINS_DIR/known_marketplaces.json"
    echo "Copied and transformed known_marketplaces.json"
fi

# Copy config.json if it exists
if [ -f "$HOST_PLUGINS_DIR/config.json" ]; then
    cp "$HOST_PLUGINS_DIR/config.json" "$PLUGINS_DIR/config.json"
    echo "Copied config.json"
fi

# Register MCP servers declared in .cspace.json (mcpServers map).
# Schema per server: { command, args[], env{}, scope, requiredEnv[] }
# - ${VAR} in env values is expanded from container env at registration time.
# - If any var in requiredEnv is unset, the server is silently skipped.
# - scope maps to `claude mcp add --scope`; defaults to "user".
if [ -n "${CSPACE_MCP_SERVERS:-}" ] && [ "$CSPACE_MCP_SERVERS" != "{}" ] && [ "$CSPACE_MCP_SERVERS" != "null" ]; then
    echo "Registering MCP servers from cspace config..."
    # shellcheck disable=SC2016
    echo "$CSPACE_MCP_SERVERS" | jq -c 'to_entries[]' | while read -r entry; do
        name=$(echo "$entry" | jq -r '.key')
        spec=$(echo "$entry" | jq -c '.value')

        # requiredEnv check — skip silently if any are missing
        skip=0
        while read -r req; do
            [ -z "$req" ] && continue
            if [ -z "$(printenv "$req" 2>/dev/null)" ]; then
                echo "  - $name: skipping (missing $req)"
                skip=1
                break
            fi
        done < <(echo "$spec" | jq -r '.requiredEnv // [] | .[]')
        [ "$skip" = "1" ] && continue

        scope=$(echo "$spec" | jq -r '.scope // "user"')
        command=$(echo "$spec" | jq -r '.command')

        # Build -e KEY=VAL args, expanding ${VAR} from container env
        env_args=()
        while IFS=$'\t' read -r key val; do
            [ -z "$key" ] && continue
            # Expand a single ${VAR} reference safely via printenv
            if [[ "$val" =~ ^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$ ]]; then
                expanded=$(printenv "${BASH_REMATCH[1]}" 2>/dev/null || true)
            else
                expanded="$val"
            fi
            env_args+=(-e "$key=$expanded")
        done < <(echo "$spec" | jq -r '.env // {} | to_entries[] | "\(.key)\t\(.value)"')

        # Build positional args array
        cmd_args=()
        while read -r arg; do
            [ -z "$arg" ] && continue
            cmd_args+=("$arg")
        done < <(echo "$spec" | jq -r '.args // [] | .[]')

        echo "  - $name: registering (scope=$scope)"
        # Newer claude CLI (2.1+) requires -e flags AFTER the name
        if ! claude mcp add --scope "$scope" "$name" "${env_args[@]}" -- "$command" "${cmd_args[@]}" 2>&1 | sed 's/^/      /'; then
            echo "  - $name: registration failed (continuing)"
        fi
    done
fi

# --- Copy shipped slash commands into user's claude commands dir ---
# Idempotent: always overwrite so updates to shipped commands take effect.
SHIPPED_COMMANDS="$CSPACE_HOME/lib/commands"
USER_COMMANDS="/home/dev/.claude/commands"
if [ -d "$SHIPPED_COMMANDS" ]; then
    mkdir -p "$USER_COMMANDS"
    # Copy only regular *.md files. Use -exec (not xargs) so a failing cp
    # surfaces a nonzero exit status directly.
    copied=0
    while IFS= read -r -d '' f; do
        if cp "$f" "$USER_COMMANDS/"; then
            copied=$((copied + 1))
        else
            echo "warning: failed to copy $f to $USER_COMMANDS" >&2
        fi
    done < <(find "$SHIPPED_COMMANDS" -maxdepth 1 -type f -name '*.md' -print0)
    chown -R dev:dev "$USER_COMMANDS"
    if [ "$copied" -gt 0 ]; then
        echo "Installed $copied shipped slash command(s)."
    fi
fi

touch "$MARKER_FILE"
chown -R dev:dev "$PLUGINS_DIR"

echo "Claude plugins initialized"
