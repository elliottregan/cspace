#!/usr/bin/env bash
# Provision a cspace devcontainer instance: shared services, container,
# workspace, env, and project-specific post-setup. Idempotent — safe to
# re-run on a partially configured instance.
#
# Called by `cspace up` and `cspace issue` with these env vars:
#   CSPACE_INSTANCE_NAME  — instance name (required)
#   CSPACE_INSTANCE_BRANCH — git branch to check out (optional)
#   CSPACE_HOME           — cspace installation directory
#   PROJECT_ROOT          — project root directory
#   CONFIG                — merged JSON config (from config.sh)
set -euo pipefail

# Source core libraries
source "$CSPACE_HOME/lib/core/config.sh"
source "$CSPACE_HOME/lib/core/ports.sh"
source "$CSPACE_HOME/lib/core/instance.sh"
source "$CSPACE_HOME/lib/core/compose.sh"

NAME="${CSPACE_INSTANCE_NAME:?setup-instance.sh requires CSPACE_INSTANCE_NAME}"
BRANCH="${CSPACE_INSTANCE_BRANCH:-}"

if [[ ! "$NAME" =~ ^[a-zA-Z0-9_-]+$ ]]; then
    echo "ERROR: Instance name must be alphanumeric (hyphens and underscores allowed)."
    exit 1
fi

# Load config if not already loaded
if [ -z "${CONFIG:-}" ]; then
    load_config
fi

# --- Shared services ---
ensure_shared

# --- Port assignment ---
assign_ports "$NAME"

# --- Marketplace helper ---
ensure_marketplace() {
    local MDIR="/home/dev/.claude/plugins/marketplaces/claude-plugins-official"
    if dc_exec "$NAME" test -d "$MDIR" 2>/dev/null; then
        echo "Plugin marketplace already present."
    else
        echo "Cloning plugin marketplace..."
        dc_exec "$NAME" bash -c \
            "mkdir -p \$(dirname $MDIR) && git clone --depth 1 https://github.com/anthropics/claude-plugins-official.git $MDIR && printf '{\"claude-plugins-official\":{\"source\":{\"source\":\"github\",\"repo\":\"anthropics/claude-plugins-official\"},\"installLocation\":\"%s\",\"lastUpdated\":\"%s\"}}' '$MDIR' \$(date -u +%Y-%m-%dT%H:%M:%S.000Z) > /home/dev/.claude/plugins/known_marketplaces.json"
    fi
}

# --- Container creation + workspace init (one-time) ---
if is_running "$NAME"; then
    echo "Instance '$NAME' already running — checking configuration..."
else
    echo "Creating new instance '$NAME'..."

    # Use branch from argument or host's current branch
    if [ -z "$BRANCH" ]; then
        BRANCH=$(git -C "$PROJECT_ROOT" rev-parse --abbrev-ref HEAD)
    fi
    REMOTE_URL=$(git -C "$PROJECT_ROOT" remote get-url origin | sed 's|://[^@]*@|://|')

    # Bundle the entire repo
    BUNDLE="/tmp/cspace-${NAME}.bundle"
    echo "Bundling repo (branch: $BRANCH)..."
    git -C "$PROJECT_ROOT" bundle create "$BUNDLE" --all

    # Ensure shared volumes exist (external: true means Compose won't create them)
    docker volume create "$(memory_volume)" 2>/dev/null || true
    docker volume create "$(logs_volume)" 2>/dev/null || true

    # Export firewall domains for the container environment
    local_domains=$(cfg_json '.firewall.domains' | jq -r 'if type == "array" then join(" ") else "" end' 2>/dev/null || true)
    export CSPACE_FIREWALL_DOMAINS="${local_domains:-}"

    # Export Claude model/effort for init-claude-plugins.sh
    export CSPACE_CLAUDE_MODEL="$(cfg '.claude.model')"
    export CSPACE_CLAUDE_EFFORT="$(cfg '.claude.effort')"

    # Export MCP server config (compact JSON object) for init-claude-plugins.sh
    export CSPACE_MCP_SERVERS="$(cfg_json '.mcpServers' | jq -c '.' 2>/dev/null || echo '{}')"

    # Resolve Dockerfile for build
    CSPACE_DOCKERFILE=$(resolve_template "Dockerfile")
    export CSPACE_DOCKERFILE

    # Start container
    dc_compose "$NAME" up -d

    # Wait for container to be ready
    echo "Waiting for container..."
    until dc_exec_root "$NAME" true 2>/dev/null; do sleep 0.5; done

    # Fix volume ownership
    dc_exec_root "$NAME" chown -R dev:dev /workspace /home/dev/.claude

    # Copy bundle into container and initialize workspace
    echo "Copying repo bundle into container..."
    dc "$NAME" cp "$BUNDLE" devcontainer:/tmp/repo.bundle
    rm -f "$BUNDLE"
    dc_exec_root "$NAME" chown dev:dev /tmp/repo.bundle

    echo "Initializing workspace..."
    dc_exec "$NAME" init-workspace.sh /tmp/repo.bundle "$BRANCH" "$REMOTE_URL"
    dc_exec_root "$NAME" rm -f /tmp/repo.bundle

    # Configure git identity from host
    GIT_NAME=$(git -C "$PROJECT_ROOT" config user.name)
    GIT_EMAIL=$(git -C "$PROJECT_ROOT" config user.email)
    dc_exec "$NAME" git config --global user.name "$GIT_NAME"
    dc_exec "$NAME" git config --global user.email "$GIT_EMAIL"

    # Copy .env files
    if [ -f "$PROJECT_ROOT/.env" ]; then
        dc "$NAME" cp "$PROJECT_ROOT/.env" devcontainer:/workspace/.env
        dc_exec_root "$NAME" chown dev:dev /workspace/.env
        echo "Copied .env"
    fi
    if [ -f "$PROJECT_ROOT/.env.local" ]; then
        dc "$NAME" cp "$PROJECT_ROOT/.env.local" devcontainer:/workspace/.env.local
        dc_exec_root "$NAME" chown dev:dev /workspace/.env.local
        echo "Copied .env.local"
    fi

    # Set up gh as git credential helper. GH_TOKEN comes from the host
    # .env via the env_file directive in docker-compose.core.yml.
    # Without it, agents cannot push/pull and will hang on credential
    # prompts — fail loudly rather than silently producing broken instances.
    if ! dc_exec "$NAME" bash -c \
        '[ -n "${GH_TOKEN:-}" ] && gh auth setup-git && echo "gh CLI configured for git push/pull"'; then
        echo "" >&2
        echo "ERROR: GH_TOKEN is not set in the container." >&2
        echo "" >&2
        echo "Agents in this instance will not be able to push, pull, or open PRs." >&2
        echo "" >&2
        echo "Fix:" >&2
        echo "  1. Create a GitHub token with scopes: repo, workflow, read:org" >&2
        echo "     https://github.com/settings/tokens/new?scopes=repo,workflow,read:org" >&2
        echo "  2. Add it to your project .env (or shell env):" >&2
        echo "     echo 'GH_TOKEN=ghp_...' >> $PROJECT_ROOT/.env" >&2
        echo "  3. Tear down and recreate this instance:" >&2
        echo "     cspace down $NAME && cspace up $NAME" >&2
        echo "" >&2
        echo "For SSO-protected org repos, also authorize the token for your org." >&2
        exit 1
    fi
fi

# --- Idempotent stages ---

# Ensure plugin marketplace
ensure_marketplace

# --- Project-specific post-setup hook ---
POST_SETUP=$(cfg '.post_setup')
if [ -n "$POST_SETUP" ] && [ -f "$PROJECT_ROOT/$POST_SETUP" ]; then
    MARKER_FILE="/workspace/.cspace-post-setup-done"
    if ! dc_exec "$NAME" test -f "$MARKER_FILE" 2>/dev/null; then
        echo "Running post-setup hook..."
        dc "$NAME" cp "$PROJECT_ROOT/$POST_SETUP" devcontainer:/tmp/post-setup.sh
        dc_exec_root "$NAME" chmod +x /tmp/post-setup.sh
        dc_exec "$NAME" bash /tmp/post-setup.sh
        dc_exec "$NAME" touch "$MARKER_FILE"
        echo "Post-setup complete."
    else
        echo "Post-setup already completed."
    fi
fi

echo "Setup complete."
