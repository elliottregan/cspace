#!/bin/bash
# Initialize Claude plugins and settings for cspace devcontainers.
# Configurable via CSPACE_CLAUDE_MODEL and CSPACE_CLAUDE_EFFORT env vars.

PLUGINS_DIR="/home/dev/.claude/plugins"
HOST_PLUGINS_DIR="/tmp/host-claude-plugins"
MARKER_FILE="$PLUGINS_DIR/.initialized"
CSPACE_HOME="${CSPACE_HOME:-/workspace/.cspace}"

MODEL="${CSPACE_CLAUDE_MODEL:-claude-opus-4-6[1m]}"
EFFORT="${CSPACE_CLAUDE_EFFORT:-max}"

# Determine hook script paths — prefer project overrides, fall back to cspace defaults
progress_hook="/workspace/.cspace/hooks/claude-progress-logger.sh"
[ -x "$progress_hook" ] || progress_hook="$CSPACE_HOME/lib/hooks/claude-progress-logger.sh"

transcript_hook="/workspace/.cspace/hooks/claude-transcript-copy.sh"
[ -x "$transcript_hook" ] || transcript_hook="$CSPACE_HOME/lib/hooks/claude-transcript-copy.sh"

# Always write hooks (they reference workspace scripts that may change between runs)
USER_SETTINGS="/home/dev/.claude/settings.json"
mkdir -p /home/dev/.claude
cat > "$USER_SETTINGS" <<HOOKS_EOF
{
  "hooks": {
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

touch "$MARKER_FILE"
chown -R dev:dev "$PLUGINS_DIR"

echo "Claude plugins initialized"
