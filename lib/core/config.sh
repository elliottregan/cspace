#!/usr/bin/env bash
# Config loading and merging for cspace
# Sources: defaults.json → .cspace.json → .cspace.local.json → env vars

# Global config variable (set by load_config)
CONFIG=""

# Find the project root (nearest parent with .git/)
find_project_root() {
    local dir="$PWD"
    while [ "$dir" != "/" ]; do
        if [ -d "$dir/.git" ]; then
            echo "$dir"
            return 0
        fi
        dir="$(dirname "$dir")"
    done
    return 1
}

# Load and merge config from all sources
load_config() {
    local project_root
    project_root=$(find_project_root) || {
        echo "ERROR: Not in a git repository." >&2
        return 1
    }
    export PROJECT_ROOT="$project_root"

    local defaults="$CSPACE_HOME/lib/defaults.json"
    local project="$project_root/.cspace.json"
    local local_overrides="$project_root/.cspace.local.json"

    # Start with defaults
    CONFIG=$(cat "$defaults")

    # Merge project config if it exists
    if [ -f "$project" ]; then
        CONFIG=$(echo "$CONFIG" | jq --slurpfile proj "$project" '. * $proj[0]')
    fi

    # Merge local overrides if they exist
    if [ -f "$local_overrides" ]; then
        CONFIG=$(echo "$CONFIG" | jq --slurpfile loc "$local_overrides" '. * $loc[0]')
    fi

    # Apply env var overrides
    [ -n "${CSPACE_PROJECT_NAME:-}" ] && CONFIG=$(echo "$CONFIG" | jq --arg v "$CSPACE_PROJECT_NAME" '.project.name = $v')
    [ -n "${CSPACE_PROJECT_REPO:-}" ] && CONFIG=$(echo "$CONFIG" | jq --arg v "$CSPACE_PROJECT_REPO" '.project.repo = $v')

    # Auto-detect project name from directory if not set
    local name
    name=$(cfg '.project.name')
    if [ -z "$name" ]; then
        name=$(basename "$project_root")
        CONFIG=$(echo "$CONFIG" | jq --arg v "$name" '.project.name = $v')
    fi

    # Auto-detect repo from git remote if not set
    local repo
    repo=$(cfg '.project.repo')
    if [ -z "$repo" ]; then
        repo=$(git -C "$project_root" remote get-url origin 2>/dev/null | sed 's|.*github.com[:/]||; s|\.git$||')
        [ -n "$repo" ] && CONFIG=$(echo "$CONFIG" | jq --arg v "$repo" '.project.repo = $v')
    fi

    # Auto-derive prefix if not set
    local prefix
    prefix=$(cfg '.project.prefix')
    if [ -z "$prefix" ]; then
        prefix=$(cfg '.project.name' | head -c 2)
        CONFIG=$(echo "$CONFIG" | jq --arg v "$prefix" '.project.prefix = $v')
    fi

    export CONFIG
}

# Query a config value (returns empty string for null/missing)
cfg() {
    echo "$CONFIG" | jq -r "$1 // empty" 2>/dev/null
}

# Query a config value as raw JSON (for arrays/objects)
cfg_json() {
    echo "$CONFIG" | jq "$1" 2>/dev/null
}

# Derived names from config
project_name() { cfg '.project.name'; }
project_prefix() { cfg '.project.prefix'; }
project_repo() { cfg '.project.repo'; }
shared_network() { echo "cspace-$(project_name)-shared"; }
image_name() { echo "cspace-$(project_name)"; }
memory_volume() { echo "cspace-$(project_name)-memory"; }
logs_volume() { echo "cspace-$(project_name)-logs"; }
instance_label() { echo "cspace.project=$(project_name)"; }

# Resolve a template file (project override takes precedence)
resolve_template() {
    local name="$1"
    local project_override="$PROJECT_ROOT/.cspace/$name"
    local default="$CSPACE_HOME/lib/templates/$name"

    if [ -f "$project_override" ]; then
        echo "$project_override"
    elif [ -f "$default" ]; then
        echo "$default"
    else
        echo "ERROR: Template not found: $name" >&2
        return 1
    fi
}

# Resolve a script file (project override takes precedence)
resolve_script() {
    local name="$1"
    local project_override="$PROJECT_ROOT/.cspace/scripts/$name"
    local default="$CSPACE_HOME/lib/scripts/$name"

    if [ -f "$project_override" ]; then
        echo "$project_override"
    else
        echo "$default"
    fi
}

# Resolve an agent prompt (project override takes precedence)
resolve_agent() {
    local name="$1"
    local project_override="$PROJECT_ROOT/.cspace/agents/$name"
    local default="$CSPACE_HOME/lib/agents/$name"

    if [ -f "$project_override" ]; then
        echo "$project_override"
    else
        echo "$default"
    fi
}

# Check if config file exists (i.e., project is initialized)
is_initialized() {
    [ -f "$PROJECT_ROOT/.cspace.json" ]
}

require_initialized() {
    if ! is_initialized; then
        echo "ERROR: No .cspace.json found. Run 'cspace init' first." >&2
        exit 1
    fi
}
