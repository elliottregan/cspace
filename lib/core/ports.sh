#!/usr/bin/env bash
# Planet name assignment and deterministic port mapping for cspace

PLANETS=(mercury venus earth mars jupiter saturn uranus neptune)

# Base ports for deterministic assignment
PORT_BASE_DEV=5173
PORT_BASE_PREVIEW=4173

# Get the next available planet name
next_planet() {
    local running
    running=$(docker ps --filter "label=$(instance_label)" \
        --format '{{.Label "com.docker.compose.project"}}' 2>/dev/null || true)

    local prefix
    prefix="$(project_prefix)-"
    for planet in "${PLANETS[@]}"; do
        if ! echo "$running" | grep -qx "${prefix}${planet}"; then
            echo "$planet"
            return 0
        fi
    done

    echo "ERROR: All planet names are in use! Pass an explicit name." >&2
    return 1
}

# Assign deterministic ports for planet names, 0 (Docker-assigned) for custom names
assign_ports() {
    local name="$1"

    export HOST_PORT_DEV=0
    export HOST_PORT_PREVIEW=0

    for i in "${!PLANETS[@]}"; do
        if [ "${PLANETS[$i]}" = "$name" ]; then
            export HOST_PORT_DEV=$((PORT_BASE_DEV + i))
            export HOST_PORT_PREVIEW=$((PORT_BASE_PREVIEW + i))
            return 0
        fi
    done

    # Custom name — Docker assigns random ports
    return 0
}

# Get host port for a service port in an instance
get_host_port() {
    local name="$1"
    local service="$2"
    local port="$3"
    docker compose -p "$(compose_project "$name")" port "$service" "$port" 2>/dev/null | sed 's/0.0.0.0://'
}
