#!/usr/bin/env bash
# Runs once at sandbox startup, then exec's the supervisor.
#
# - Configures git's HTTPS credential helper when GH_TOKEN is set so git
#   operations against github.com auto-use the token (via gh CLI's helper).
#   Without this, `git push` prompts interactively.
set -euo pipefail

# Auto-configure git credential helper for github.com when gh is authed.
# `gh auth setup-git` writes:
#   credential.helper = !/usr/bin/gh auth git-credential
# to ~/.gitconfig. Idempotent.
if [ -n "${GH_TOKEN:-}" ] && command -v gh >/dev/null 2>&1; then
    gh auth setup-git 2>/dev/null || true
fi

# Hand off to the actual command (cspace-supervisor by default).
exec "$@"
