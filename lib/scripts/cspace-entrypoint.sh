#!/usr/bin/env bash
# Runs once at sandbox startup, then exec's the supervisor.
#
# - Configures git's HTTPS credential helper when GH_TOKEN is set so git
#   operations against github.com auto-use the token (via gh CLI's helper).
#   Without this, `git push` prompts interactively.
# - Pre-accepts Claude Code's first-run gates (theme picker, trust dialog,
#   custom-API-key approval, onboarding tips) so `cspace attach` drops
#   straight into a usable claude REPL instead of a setup wizard.
set -euo pipefail

# Auto-configure git credential helper for github.com when gh is authed.
# `gh auth setup-git` writes:
#   credential.helper = !/usr/bin/gh auth git-credential
# to ~/.gitconfig. Idempotent.
if [ -n "${GH_TOKEN:-}" ] && command -v gh >/dev/null 2>&1; then
    gh auth setup-git 2>/dev/null || true
fi

# Pre-create ~/.claude.json with the first-run prompts already accepted.
# Without this, interactive `claude` walks the user through theme
# selection, trust-this-folder, "Detected a custom API key…" approval,
# and a stack of tips before becoming usable. We approve all of them up
# front. Idempotent: skip if the file already exists (so user-made
# changes inside the sandbox persist across restarts).
#
# IMPORTANT: hasTrustDialogAccepted is a PER-DIRECTORY flag stored under
# projects[<cwd>] — the global key alone doesn't suppress the prompt.
# Pre-seed projects["/workspace"] (where claude is launched from) so the
# trust dialog never fires.
CLAUDE_JSON="$HOME/.claude.json"
if [ ! -f "$CLAUDE_JSON" ]; then
    mkdir -p "$HOME/.claude"
    # The "approved" array carries the env-var value claude was offered
    # at first run; populate it with whichever carrier is set so the
    # custom-API-key prompt is pre-answered.
    APPROVED='[]'
    if [ -n "${ANTHROPIC_API_KEY:-}" ]; then
        APPROVED="[\"${ANTHROPIC_API_KEY}\"]"
    elif [ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]; then
        APPROVED="[\"${CLAUDE_CODE_OAUTH_TOKEN}\"]"
    fi
    cat > "$CLAUDE_JSON" <<JSON
{
  "hasCompletedOnboarding": true,
  "hasTrustDialogAccepted": true,
  "bypassPermissionsModeAccepted": true,
  "theme": "dark",
  "customApiKeyResponses": { "approved": ${APPROVED}, "rejected": [] },
  "projects": {
    "/workspace": {
      "hasTrustDialogAccepted": true,
      "projectOnboardingSeenCount": 1,
      "hasClaudeMdExternalIncludesApproved": true,
      "hasClaudeMdExternalIncludesWarningShown": true
    }
  }
}
JSON
fi

# Pre-create ~/.claude/settings.json so the cspace statusline shows up
# in interactive `claude`. The shipped script is at
# /usr/local/bin/cspace-statusline.sh; project overrides at
# /workspace/.cspace/scripts/statusline.sh take precedence so projects
# can ship their own. Idempotent.
SETTINGS_JSON="$HOME/.claude/settings.json"
if [ ! -f "$SETTINGS_JSON" ]; then
    mkdir -p "$HOME/.claude"
    statusline_cmd="/usr/local/bin/cspace-statusline.sh"
    [ -x "/workspace/.cspace/scripts/statusline.sh" ] && statusline_cmd="/workspace/.cspace/scripts/statusline.sh"
    cat > "$SETTINGS_JSON" <<JSON
{
  "statusLine": {
    "type": "command",
    "command": "${statusline_cmd}"
  }
}
JSON
fi

# Install Claude Code plugins declared in /workspace/.claude/settings.json.
# Idempotent and best-effort — failures here don't block the supervisor.
# Marketplaces are pre-baked under /opt/cspace/marketplaces/ so first
# boot doesn't have to clone anything from GitHub.
if [ -x /usr/local/bin/cspace-install-plugins.sh ]; then
    /usr/local/bin/cspace-install-plugins.sh || true
fi

# Hand off to the actual command (cspace-supervisor by default).
exec "$@"
