#!/usr/bin/env bash
# Docker Compose file resolution and generation for cspace

# Build the compose file flags for an instance
# Returns: -f <core> [-f <project>] with proper resolution
compose_files() {
    local core
    core=$(resolve_template "docker-compose.core.yml")

    local args="-f $core"

    # Add project-specific services if configured
    local services_file
    services_file=$(cfg '.services')
    if [ -n "$services_file" ] && [ -f "$PROJECT_ROOT/$services_file" ]; then
        args="$args -f $PROJECT_ROOT/$services_file"
    fi

    echo "$args"
}

# Export environment variables needed by compose templates
export_compose_env() {
    local name="$1"
    local prefix
    prefix=$(project_prefix)

    export COMPOSE_PROJECT_NAME="$(compose_project "$name")"
    export CSPACE_CONTAINER_NAME="${name}"
    export CSPACE_PROJECT_NAME="$(project_name)"
    export CSPACE_PREFIX="$prefix"
    export CSPACE_IMAGE="$(image_name)"
    export CSPACE_MEMORY_VOLUME="$(memory_volume)"
    export CSPACE_LOGS_VOLUME="$(logs_volume)"
    export CSPACE_LABEL="$(instance_label)"
    export CSPACE_HOME

    # Export container environment from config
    local env_json
    env_json=$(cfg_json '.container.environment')
    if [ "$env_json" != "null" ] && [ "$env_json" != "{}" ]; then
        while IFS='=' read -r key value; do
            export "CSPACE_ENV_${key}=${value}"
        done < <(echo "$env_json" | jq -r 'to_entries[] | "\(.key)=\(.value)"')
    fi
}

# Run docker compose with proper file resolution for an instance
dc_compose() {
    local name="$1"
    shift
    export_compose_env "$name"
    assign_ports "$name"

    local files
    files=$(compose_files)
    # shellcheck disable=SC2086
    docker compose $files -p "$(compose_project "$name")" "$@"
}

