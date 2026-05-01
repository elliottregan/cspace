#!/usr/bin/env bash
# Restart loop around cspace-supervisor. If the supervisor exits with code 0
# (clean shutdown — typically triggered by container stop), the loop exits
# and the container shuts down. Signal-killed exits (SIGTERM=143, SIGKILL=137)
# also indicate intentional shutdown — the container is being stopped, so
# don't respawn. On any other non-zero exit (crash, OOM, panic), the wrapper
# logs the failure and respawns after a brief backoff.
#
# Resume continuity: the supervisor reads CSPACE_RESUME_SESSION_ID from env
# at startup. cspace2-up injects this value once at sandbox-create time;
# it persists across restarts inside the container. So the new supervisor
# picks up the same Claude conversation thread without external glue.
#
# This wrapper is invoked by /usr/local/bin/cspace2-entrypoint.sh via exec.

# NOTE: -u (treat unset vars as errors), but NOT -e — we want to handle
# the supervisor's exit code manually, not propagate it.
set -u

SUPERVISOR=/usr/local/bin/cspace-supervisor
BACKOFF_SECONDS=2

while true; do
    "$SUPERVISOR"
    code=$?

    # Treat clean exit and signal-killed exits as intentional shutdown.
    # 143 = 128 + 15 (SIGTERM), 137 = 128 + 9 (SIGKILL).
    if [ "$code" = "0" ] || [ "$code" = "143" ] || [ "$code" = "137" ]; then
        echo "[supervisor-loop] supervisor exited cleanly (code=$code); shutting down container"
        break
    fi

    echo "[supervisor-loop] supervisor exited with code $code; restarting in ${BACKOFF_SECONDS}s"
    sleep "$BACKOFF_SECONDS"
done
