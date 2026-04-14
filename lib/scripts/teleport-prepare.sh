#!/usr/bin/env bash
# Prepare a teleport: bundle workspace + transcript, then invoke
# `cspace up <target> --teleport-from <dir>` on the host CLI.
#
# Usage: teleport-prepare.sh <target-instance>
#
# Env vars (defaults match the in-container deployment):
#   CSPACE_TELEPORT_WORKSPACE   path to the repo to bundle (default: /workspace)
#   CSPACE_TELEPORT_DIR         path to the shared teleport transfer dir
#                                 (default: /teleport)
#   CSPACE_INSTANCE_NAME        name of the source instance, recorded in the
#                                 manifest. Injected by docker-compose.core.yml.
#   HOME                        used to find ~/.claude/projects/-workspace
#
# On success: writes <dir>/<session>/{workspace.bundle,session.jsonl,manifest.json},
# invokes `cspace up`, prints the reconnect message, exits 0.
#
# On failure: exits non-zero with a message. The source container is never
# modified by this script beyond creating files under CSPACE_TELEPORT_DIR.
set -euo pipefail

TARGET="${1:?Usage: teleport-prepare.sh <target-instance>}"

WORKSPACE="${CSPACE_TELEPORT_WORKSPACE:-/workspace}"
TELEPORT_DIR="${CSPACE_TELEPORT_DIR:-/teleport}"
SOURCE_NAME="${CSPACE_INSTANCE_NAME:-unknown}"
PROJECTS_DIR="${HOME}/.claude/projects/-workspace"

# 1. Find the active session: the most recently modified .jsonl transcript.
if [ ! -d "$PROJECTS_DIR" ]; then
    echo "teleport: no live Claude session — $PROJECTS_DIR missing" >&2
    exit 1
fi

LATEST_TRANSCRIPT=$(ls -1t "$PROJECTS_DIR"/*.jsonl 2>/dev/null | head -n1 || true)

if [ -z "$LATEST_TRANSCRIPT" ]; then
    echo "teleport: no live Claude session — no transcripts in $PROJECTS_DIR" >&2
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

# 2. Bundle the workspace.
echo "teleport: bundling workspace..."
git -C "$WORKSPACE" bundle create "$SESSION_DIR/workspace.bundle" --all

# 3. Copy the transcript.
cp "$LATEST_TRANSCRIPT" "$SESSION_DIR/session.jsonl"

# 4. Write the manifest.
SOURCE_HEAD=$(git -C "$WORKSPACE" rev-parse HEAD 2>/dev/null || echo "")
SOURCE_BRANCH=$(git -C "$WORKSPACE" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "")
CREATED_AT=$(date -u +%Y-%m-%dT%H:%M:%SZ)

cat > "$SESSION_DIR/manifest.json" <<JSON
{
  "source": "$SOURCE_NAME",
  "target": "$TARGET",
  "session_id": "$SESSION_ID",
  "created_at": "$CREATED_AT",
  "source_head": "$SOURCE_HEAD",
  "source_branch": "$SOURCE_BRANCH"
}
JSON

# 5. Invoke the host CLI. We run `cspace up` synchronously so any failure
#    surfaces in the script's exit code and the source stays functional.
echo "teleport: provisioning $TARGET..."
cspace up "$TARGET" --teleport-from "$SESSION_DIR"

# 6. Stop the source container (volumes survive; user can `cspace start` or
#    `cspace rm` at their leisure). Skip when SOURCE_NAME is "unknown" —
#    that means we're running outside a real cspace instance (e.g., test
#    harness), and there's nothing to stop.
if [ "$SOURCE_NAME" != "unknown" ]; then
    echo "teleport: stopping source ($SOURCE_NAME)..."
    cspace stop "$SOURCE_NAME" || echo "teleport: warning — could not stop source; leaving it running" >&2
fi

echo ""
echo "Teleport complete. Reconnect with: cspace resume $TARGET"
