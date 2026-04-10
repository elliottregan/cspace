#!/usr/bin/env bash
# Instance lifecycle helpers for cspace

# Get all running instance names for this project.
# Returns bare instance names (without the project prefix).
get_instances() {
    local prefix
    prefix="$(project_prefix)."
    docker ps --filter "label=$(instance_label)" \
        --format '{{.Label "com.docker.compose.project"}}' 2>/dev/null \
        | sed "s/^${prefix}//" | sort -u
}

# Get every running cspace instance across all projects.
# Output: tab-separated "instance<TAB>project"
get_all_instances() {
    docker ps --filter "label=cspace.instance=true" \
        --format '{{.Label "com.docker.compose.project"}}	{{.Label "cspace.project"}}' \
        2>/dev/null | sort -u
}

# Get instance details as "name branch age" lines
get_instance_details() {
    local instances
    instances=$(get_instances)
    [ -z "$instances" ] && return

    while read -r name; do
        [ -z "$name" ] && continue
        local cp
        cp=$(compose_project "$name")
        local branch=$(dc_exec "$name" git branch --show-current 2>/dev/null || echo "?")
        local age=$(docker ps --filter "label=com.docker.compose.project=$cp" \
            --filter "label=$(instance_label)" \
            --format '{{.RunningFor}}' 2>/dev/null | head -1)
        printf "%-16s %-30s %s\n" "$name" "$branch" "$age"
    done <<< "$instances"
}

# Like get_instance_details but spans all projects, with a project column.
get_all_instance_details() {
    local rows
    rows=$(get_all_instances)
    [ -z "$rows" ] && return

    while IFS=$'\t' read -r cp project; do
        [ -z "$cp" ] && continue
        local branch=$(docker compose -p "$cp" exec -T -u dev -w /workspace devcontainer git branch --show-current 2>/dev/null || echo "?")
        local age=$(docker ps --filter "label=com.docker.compose.project=$cp" \
            --filter "label=cspace.instance=true" \
            --format '{{.RunningFor}}' 2>/dev/null | head -1)
        printf "%-16s %-20s %-30s %s\n" "$cp" "${project:-?}" "$branch" "$age"
    done <<< "$rows"
}

# Check if an instance is running
is_running() {
    local name="$1"
    docker compose -p "$(compose_project "$name")" ps --status running -q 2>/dev/null | grep -q .
}

# Require an instance to be running
require_running() {
    local name="$1"
    if ! is_running "$name"; then
        echo "ERROR: Instance '$name' is not running." >&2
        echo "Use 'cspace list' to see running instances." >&2
        exit 1
    fi
}

# Docker compose command for an instance
dc() {
    docker compose -p "$(compose_project "$1")" "${@:2}"
}

# Execute a command inside the devcontainer
dc_exec() {
    local name="$1"
    shift
    docker compose -p "$(compose_project "$name")" exec -T -u dev -w /workspace devcontainer "$@" </dev/null
}

# Execute as root inside the devcontainer
dc_exec_root() {
    local name="$1"
    shift
    docker compose -p "$(compose_project "$name")" exec -T devcontainer "$@" </dev/null
}

# Skip Claude onboarding (auth is via CLAUDE_CODE_OAUTH_TOKEN env var)
skip_onboarding() {
    local name="$1"
    dc_exec "$name" node -e "
      const fs = require('fs'), f = '/home/dev/.claude.json';
      const d = fs.existsSync(f) ? JSON.parse(fs.readFileSync(f)) : {};
      d.hasCompletedOnboarding = true;
      fs.writeFileSync(f, JSON.stringify(d));
    "
}

# Show port mappings for an instance
show_ports() {
    local name="$1"
    local prefix=$(project_prefix)

    echo "Ports for $name:"

    # Always show the devcontainer ports from config
    local ports_json
    ports_json=$(cfg_json '.container.ports')
    if [ "$ports_json" != "null" ] && [ "$ports_json" != "{}" ]; then
        echo "$ports_json" | jq -r 'to_entries[] | "\(.key) \(.value)"' | while read -r port label; do
            local host_port
            host_port=$(get_host_port "$name" devcontainer "$port")
            [ -n "$host_port" ] && echo "  $label: http://localhost:${host_port}"
        done
    fi

    # Show any additional service ports
    local services
    services=$(dc "$name" ps --format '{{.Service}}' 2>/dev/null | grep -v devcontainer || true)
    if [ -n "$services" ]; then
        while read -r svc; do
            [ -z "$svc" ] && continue
            local svc_ports
            svc_ports=$(dc "$name" port "$svc" 2>/dev/null | sed 's/0.0.0.0://' || true)
            [ -n "$svc_ports" ] && echo "  $svc: http://localhost:${svc_ports}"
        done <<< "$services" || true
    fi
}
