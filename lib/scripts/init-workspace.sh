#!/usr/bin/env bash
# Initialize workspace from a git bundle.
# Usage: init-workspace.sh <bundle-path> <branch> <remote-url>
set -euo pipefail

BUNDLE="${1:?Usage: init-workspace.sh <bundle> <branch> <remote-url>}"
BRANCH="${2:?}"
REMOTE_URL="${3-}"
WORKSPACE="/workspace"

# Skip if already initialized
if [ -d "$WORKSPACE/.git" ]; then
    echo "Workspace already initialized."
    exit 0
fi

echo "Cloning from bundle (branch: $BRANCH)..."
git clone --branch "$BRANCH" "$BUNDLE" /tmp/_clone

# Move into workspace (handles volume mounts where workspace may already exist)
cp -a /tmp/_clone/. "$WORKSPACE/"
rm -rf /tmp/_clone

cd "$WORKSPACE"
if [ -n "$REMOTE_URL" ]; then
    git remote set-url origin "$REMOTE_URL"
fi

# Install dependencies (skip postinstall — may need env vars not yet available)
if [ -f package.json ]; then
    if command -v pnpm &>/dev/null; then
        pnpm install --ignore-scripts
    elif command -v yarn &>/dev/null; then
        yarn install --ignore-scripts
    else
        npm install --ignore-scripts
    fi
fi

echo "Workspace initialized."
