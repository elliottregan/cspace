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

    local DC="docker compose -p $name"

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

    if [ $EXIT_CODE -eq 0 ] || [ $EXIT_CODE -eq 141 ]; then
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
    exit $EXIT_CODE
}
