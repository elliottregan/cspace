#!/usr/bin/env bash
# Prepare a teleport: bundle workspace state, then invoke
# `cspace up <target> --teleport-from <dir>` on the host CLI.
#
# Usage: teleport-prepare.sh <target-instance>
#
# Env vars (defaults match the in-container deployment):
#   CSPACE_TELEPORT_WORKSPACE   path to the repo to bundle (default: /workspace)
#   CSPACE_TELEPORT_DIR         OVERRIDE for the transfer directory. In
#                                 production this is unset — docker-compose.core.yml
#                                 no longer exports it into the container, so
#                                 macOS host paths can't leak in. The script
#                                 falls back to /teleport (the in-container
#                                 bind-mount destination). Tests set this to
#                                 a tmpdir to exercise the script on the host.
#   CSPACE_INSTANCE_NAME        name of the source instance, recorded in the
#                                 manifest. Injected by docker-compose.core.yml.
#   HOME                        used to find ~/.claude/projects/-workspace
#   - The session JSONL no longer travels via the teleport bundle. Sessions
#     live in the project's shared $HOME/.cspace/sessions/<project>/ on the
#     host, which every cspace container in the project bind-mounts. The
#     target already sees the source's JSONL at the right path — resume by
#     session ID "just works."
#
# Requires on PATH: cspace, git, jq
#
# On success: writes /teleport/<session>/{workspace.bundle,manifest.json},
# invokes `cspace up`, prints the reconnect message, exits 0.
set -euo pipefail

TARGET="${1:?Usage: teleport-prepare.sh <target-instance>}"

WORKSPACE="${CSPACE_TELEPORT_WORKSPACE:-/workspace}"
# Default to the in-container bind-mount destination. Tests may override
# with CSPACE_TELEPORT_DIR; production containers never have that var set
# (see docker-compose.core.yml for the deliberate omission).
TELEPORT_DIR="${CSPACE_TELEPORT_DIR:-/teleport}"
SOURCE_NAME="${CSPACE_INSTANCE_NAME:-unknown}"
PROJECTS_DIR="${HOME}/.claude/projects/-workspace"

# 1. Find the active session: the most recently modified .jsonl transcript.
if [ ! -d "$PROJECTS_DIR" ]; then
    echo "teleport: no live Claude session — $PROJECTS_DIR missing" >&2
    exit 1
fi
if [ ! -r "$PROJECTS_DIR" ]; then
    echo "teleport: cannot read $PROJECTS_DIR (permission denied)" >&2
    exit 1
fi

# nullglob: if no matches, the array is empty instead of containing the
# literal glob. Toggle the option around the assignment so it doesn't leak.
shopt -s nullglob
TRANSCRIPTS=( "$PROJECTS_DIR"/*.jsonl )
shopt -u nullglob

if [ "${#TRANSCRIPTS[@]}" -eq 0 ]; then
    echo "teleport: no live Claude session — no transcripts in $PROJECTS_DIR" >&2
    exit 1
fi

# Pick the newest by mtime without depending on -printf (BusyBox-compatible).
LATEST_TRANSCRIPT=""
LATEST_MTIME=0
for t in "${TRANSCRIPTS[@]}"; do
    # stat -c on GNU, stat -f on BSD. BusyBox stat supports -c.
    m=$(stat -c %Y "$t" 2>/dev/null || stat -f %m "$t" 2>/dev/null || echo 0)
    if [ "$m" -gt "$LATEST_MTIME" ]; then
        LATEST_MTIME="$m"
        LATEST_TRANSCRIPT="$t"
    fi
done

if [ -z "$LATEST_TRANSCRIPT" ]; then
    echo "teleport: could not stat any transcript in $PROJECTS_DIR" >&2
    exit 1
fi

SESSION_ID=$(basename "$LATEST_TRANSCRIPT" .jsonl)
SESSION_DIR="$TELEPORT_DIR/$SESSION_ID"
mkdir -p "$SESSION_DIR"

if [ -f "$SESSION_DIR/manifest.json" ]; then
    echo "teleport: session $SESSION_ID is already staged at $SESSION_DIR" >&2
    echo "teleport: if a prior teleport is still in progress, wait for it to finish;" >&2
    echo "teleport: otherwise remove $SESSION_DIR and retry" >&2
    exit 1
fi

# 2. Bundle the workspace. This is the only thing that needs to ride along
#    in the teleport transfer — the session JSONL is already visible to the
#    target via the shared sessions bind mount.
echo "teleport: bundling workspace..."
git -C "$WORKSPACE" bundle create "$SESSION_DIR/workspace.bundle" --all

# 3. Write the manifest. Use jq --arg to safely serialize values that may
# contain characters requiring JSON escaping (branch names, URLs, etc.).
SOURCE_HEAD=$(git -C "$WORKSPACE" rev-parse HEAD 2>/dev/null || echo "")
SOURCE_BRANCH=$(git -C "$WORKSPACE" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "")
SOURCE_REMOTE_URL=$(git -C "$WORKSPACE" remote get-url origin 2>/dev/null || echo "")
CREATED_AT=$(date -u +%Y-%m-%dT%H:%M:%SZ)

jq -n \
    --arg source "$SOURCE_NAME" \
    --arg target "$TARGET" \
    --arg session_id "$SESSION_ID" \
    --arg created_at "$CREATED_AT" \
    --arg source_head "$SOURCE_HEAD" \
    --arg source_branch "$SOURCE_BRANCH" \
    --arg source_remote_url "$SOURCE_REMOTE_URL" \
    '{
        source: $source,
        target: $target,
        session_id: $session_id,
        created_at: $created_at,
        source_head: $source_head,
        source_branch: $source_branch,
        source_remote_url: $source_remote_url,
    }' > "$SESSION_DIR/manifest.json"

# 4. Invoke the host CLI. We run `cspace up` synchronously so any failure
#    surfaces in the script's exit code and the source stays functional.
echo "teleport: provisioning $TARGET..."
cspace up "$TARGET" --teleport-from "$SESSION_DIR"

# 5. Print the success message BEFORE stopping the source. The `cspace stop`
#    below tells docker to stop this very container (we're running inside
#    the source), which may race with bash's stdout flush and swallow the
#    reconnect instructions. Print first, stop last.
echo ""
echo "Teleport complete. Reconnect with: cspace resume $TARGET"

# 6. Stop the source container (volumes survive; user can `cspace start` or
#    `cspace rm` at their leisure). Skip when SOURCE_NAME is "unknown" —
#    that means we're running outside a real cspace instance (e.g., test
#    harness), and there's nothing to stop.
if [ "$SOURCE_NAME" != "unknown" ]; then
    echo "teleport: stopping source ($SOURCE_NAME)..."
    cspace stop "$SOURCE_NAME" || echo "teleport: warning — could not stop source; leaving it running" >&2
fi
