#!/bin/bash
# Initialize Claude plugins and settings for cspace devcontainers.
# Configurable via CSPACE_CLAUDE_MODEL and CSPACE_CLAUDE_EFFORT env vars.

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
            if [ -z "$(eval echo "\${$req:-}")" ]; then
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
            # Expand ${VAR} references — eval is intentional for ${...} expansion
            expanded=$(eval echo "$val")
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

touch "$MARKER_FILE"
chown -R dev:dev "$PLUGINS_DIR"

echo "Claude plugins initialized"
