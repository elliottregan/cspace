#!/usr/bin/env bash
# Shared launcher for the agent-supervisor process. Both `cspace up
# --prompt-file` (implementer role) and `cspace coordinate` (coordinator
# role) route through launch_supervisor so they share one invocation
# shape: same env vars, same transcript-copy EXIT trap, same stream-status
# pipeline, same exit-code handling. This keeps both roles consistent and
# gives a future log viewer a single source of NDJSON events under
# /logs/events/{instance|_coordinator}/session-*.ndjson.
#
# Callers must have loaded config (load_config) and set $model before
# calling launch_supervisor — the helper reads $model from caller scope.

# Echo `--no-effort-max` when the configured effort is set and not "max".
# effort=max is the supervisor default, so we only pass the flag to opt
# out. Empty effort (no config) leaves the supervisor default in place.
build_effort_flag() {
    local effort="${1:-}"
    if [ -n "$effort" ] && [ "$effort" != "max" ]; then
        echo "--no-effort-max"
    fi
}

# Echo a --system-prompt-file flag if the project has a per-role override
# at /workspace/.cspace/agent-supervisor/<role>-system-prompt.txt. Role
# must be literally "agent" or "coordinator". The supervisor falls back
# to its bundled default prompt when the flag is absent.
build_system_prompt_flag() {
    local instance="$1"
    local role="$2"
    local override="/workspace/.cspace/agent-supervisor/${role}-system-prompt.txt"
    if dc_exec "$instance" test -f "$override" 2>/dev/null; then
        echo "--system-prompt-file $override"
    fi
}

# launch_supervisor <name> <role> <container_prompt_path> <stderr_log> <effort_flag> <system_prompt_flag>
#
# Runs the agent-supervisor inside the named instance and pipes its
# NDJSON stdout through stream-status.sh. Uses `docker compose exec`
# (NOT exec'd from the shell) so we can observe the exit code and
# surface a role-specific FAILED message. Treats exit codes 0 and 141
# as success — 141 is SIGPIPE from stream-status.sh on clean exit.
#
# Reads from caller scope:
#   $model — passed via --model (defaults to claude-opus-4-6 on empty)
launch_supervisor() {
    local name="$1"
    local role="$2"
    local container_prompt_path="$3"
    local stderr_log="$4"
    local effort_flag="$5"
    local system_prompt_flag="$6"

    local DC="docker compose -p $(compose_project "$name")"

    # Only the agent role takes --instance; the supervisor rejects it for
    # coordinators (see supervisor.mjs:241-244).
    local instance_flag=""
    if [ "$role" = "agent" ]; then
        instance_flag="--instance $name"
    fi

    local EXIT_CODE=0
    $DC exec -u dev -w /workspace \
        -e CLAUDE_AUTONOMOUS=1 \
        -e CLAUDE_INSTANCE="$name" \
        devcontainer \
        bash -c "set -o pipefail; trap '' PIPE; trap '[ -x /workspace/.cspace/hooks/copy-transcript-on-exit.sh ] && /workspace/.cspace/hooks/copy-transcript-on-exit.sh || [ -x /opt/cspace/lib/hooks/copy-transcript-on-exit.sh ] && /opt/cspace/lib/hooks/copy-transcript-on-exit.sh' EXIT; node /opt/cspace/lib/agent-supervisor/supervisor.mjs --role $role $instance_flag --prompt-file $container_prompt_path --model ${model:-claude-opus-4-6} $effort_flag $system_prompt_flag 2>$stderr_log \
            | /opt/cspace/lib/scripts/stream-status.sh" || EXIT_CODE=$?

    # 0   = clean exit
    # 2   = stream-status.sh exited after supervisor closed its pipe
    # 141 = SIGPIPE (bash default when pipe reader exits)
    if [ $EXIT_CODE -eq 0 ] || [ $EXIT_CODE -eq 2 ] || [ $EXIT_CODE -eq 141 ]; then
        return 0
    fi

    echo "" >&2
    echo "FAILED — $role exited with code $EXIT_CODE" >&2
    echo "  Shell:   cspace ssh $name" >&2
    if [ "$role" = "coordinator" ]; then
        echo "  Re-run:  cspace coordinate \"...\" --name $name (resumable)" >&2
    else
        echo "  Re-run:  cspace up $name --prompt-file <path>" >&2
    fi
    return $EXIT_CODE
}

# resolve_logs_volume_path
#
# Find the host-side mountpoint of the cspace-logs Docker volume so
# host-side scripts can read /logs/messages/ etc. Falls back to a
# direct path if it already exists (e.g., running inside a container).
resolve_logs_volume_path() {
    if [ -d "/logs/messages" ]; then
        echo "/logs/messages"
        return
    fi
    local vol="${CSPACE_LOGS_VOLUME:-cspace-logs}"
    local mp
    mp=$(docker volume inspect "$vol" --format '{{ .Mountpoint }}' 2>/dev/null) || true
    if [ -n "$mp" ] && [ -d "$mp" ]; then
        echo "$mp/messages"
        return
    fi
    echo ""
}

# relaunch_supervisor_detached <name> <prompt_path> <stderr_log> <effort_flag> <system_prompt_flag> [ignore_inbox_before_ms]
#
# Launch a fresh agent supervisor in the same container in DETACHED mode.
# Used by restart_supervisor after the old supervisor has exited. Key
# differences from launch_supervisor:
#   - Uses -d (detached) and -T (no TTY) — returns immediately
#   - No stream-status.sh pipe (no terminal to render to)
#   - No EXIT trap (transcript copy fires via SessionEnd hook when
#     CLAUDE_AUTONOMOUS=1 is set)
#
# Reads from caller scope:
#   $model — passed via --model (defaults to claude-opus-4-6 on empty)
relaunch_supervisor_detached() {
    local name="$1"
    local container_prompt_path="$2"
    local stderr_log="$3"
    local effort_flag="$4"
    local system_prompt_flag="$5"
    local ignore_inbox_before="${6:-}"

    local inbox_flag=""
    [ -n "$ignore_inbox_before" ] && inbox_flag="--ignore-inbox-before $ignore_inbox_before"

    docker compose -p "$(compose_project "$name")" exec -d -T -u dev -w /workspace \
        -e CLAUDE_AUTONOMOUS=1 \
        -e CLAUDE_INSTANCE="$name" \
        devcontainer \
        bash -c "node /opt/cspace/lib/agent-supervisor/supervisor.mjs \
            --role agent --instance $name \
            --prompt-file $container_prompt_path \
            --model ${model:-claude-opus-4-6} \
            $effort_flag $system_prompt_flag $inbox_flag \
            2>$stderr_log"
}

# restart_supervisor <name> [reason]
#
# Restart an agent's supervisor inside its existing container. Sends an
# interrupt to the old supervisor, waits for it to exit cleanly (by
# watching for its completion notification in _coordinator/inbox/), then
# launches a fresh supervisor with the same prompt file. If reason is
# given, a restart marker is prepended to the prompt.
#
# Reads from caller scope:
#   $model — passed via --model (defaults to claude-opus-4-6 on empty)
restart_supervisor() {
    local name="$1"
    local reason="${2:-}"

    local MSG_DIR
    MSG_DIR=$(resolve_logs_volume_path)
    if [ -z "$MSG_DIR" ]; then
        echo "ERROR: cannot resolve cspace-logs volume path" >&2
        return 1
    fi

    local start_ms
    start_ms=$(date +%s%3N)

    # Create a reference timestamp file for the completion-poll find
    local ref_file="/tmp/.cspace-restart-ref-${name}"
    touch "$ref_file"

    # Interrupt the old supervisor via the existing socket dispatch
    echo "Interrupting supervisor for $name..."
    supervisor_dispatch interrupt "$name" 2>/dev/null || true

    # Wait for the completion notification that signals the old supervisor
    # has cleaned up its socket and is about to exit. The supervisor writes
    # the completion AFTER cleanup() (socket released) so this is safe.
    local waited=0
    local found=""
    while [ $waited -lt 30000 ]; do
        found=$(find "$MSG_DIR/_coordinator/inbox/" -name "completion-${name}-*" \
            -newer "$ref_file" 2>/dev/null | head -1)
        if [ -n "$found" ]; then
            break
        fi
        sleep 0.25
        waited=$((waited + 250))
    done
    rm -f "$ref_file"

    if [ -z "$found" ]; then
        echo "WARNING: timed out waiting for old supervisor to exit (30s)" >&2
    else
        echo "Old supervisor exited cleanly."
    fi

    # If reason given, prepend a restart marker to the prompt
    local prompt_path="/tmp/claude-prompt.txt"
    if [ -n "$reason" ]; then
        local DC="docker compose -p $(compose_project "$name")"
        $DC exec -T -u dev devcontainer bash -c "
            { echo '[This session was restarted by the coordinator. Reason: $reason. Your workspace is preserved — all files, branches, and uncommitted changes are intact. Re-establish any external state (browser sessions, test servers, etc.) as needed, then continue your task.]'
              echo ''
              cat /tmp/claude-prompt.txt
            } > /tmp/restart-prompt.txt
        " </dev/null
        prompt_path="/tmp/restart-prompt.txt"
    fi

    # Build flags from config
    local effort_flag system_prompt_flag
    effort_flag=$(build_effort_flag "$(cfg '.claude.effort')")
    system_prompt_flag=$(build_system_prompt_flag "$name" agent)

    # Launch new supervisor detached with inbox filter
    relaunch_supervisor_detached "$name" "$prompt_path" "/tmp/agent-stderr.log" \
        "$effort_flag" "$system_prompt_flag" "$start_ms"

    echo "Restarted supervisor for $name (detached)."
}
