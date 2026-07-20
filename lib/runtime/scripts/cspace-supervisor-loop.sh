#!/usr/bin/env bash
# Restart loop around cspace-supervisor. If the supervisor exits with code 0
# (clean shutdown — typically triggered by container stop) or 143 (SIGTERM,
# container stop), the loop exits and the container shuts down. On any other
# non-zero exit (crash, OOM SIGKILL, panic, sdk-error exit), the wrapper
# logs the failure and respawns after a brief backoff.
#
# Resume continuity: at startup the supervisor scans its own event log
# (/sessions/primary/events.ndjson, on a host bind mount) for the last SDK
# init session id and resumes it. So a respawned supervisor picks up the
# same Claude conversation thread without external glue.
#
# This wrapper is invoked by /usr/local/bin/cspace-entrypoint.sh via exec.

# NOTE: -u (treat unset vars as errors), but NOT -e — we want to handle
# the supervisor's exit code manually, not propagate it.
set -u

SUPERVISOR=/usr/local/bin/cspace-supervisor
BACKOFF_SECONDS=2

while true; do
    "$SUPERVISOR"
    code=$?

    # Treat clean exit and SIGTERM (143 = 128 + 15) as intentional shutdown.
    # 137 (SIGKILL) is NOT clean — it's how the OOM killer reaps the supervisor
    # and must respawn (cs-finding 2026-07-16-supervisor-silent-death-modes-and-fail-open-auth).
    if [ "$code" = "0" ] || [ "$code" = "143" ]; then
        echo "[supervisor-loop] supervisor exited cleanly (code=$code); shutting down container"
        break
    fi

    echo "[supervisor-loop] supervisor exited with code $code; restarting in ${BACKOFF_SECONDS}s"
    sleep "$BACKOFF_SECONDS"
done
