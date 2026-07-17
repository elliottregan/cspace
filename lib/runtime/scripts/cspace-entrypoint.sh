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

# /usr/sbin is where iptables/dnsmasq live on debian-family bases but
# isn't in dev's PATH by default. Add it so `command -v` lookups work
# inside ensure_dep.
case ":$PATH:" in
    *:/usr/sbin:*) ;;
    *) PATH="/usr/sbin:/sbin:$PATH" ;;
esac
export PATH

# Auto-install runtime deps that some non-default base images may lack.
# Default cspace:latest already has them, but if the project picks its
# own image (devcontainer.image), iptables and dnsmasq might be absent.
# Best-effort: only attempt apt when running as root with apt-get
# present. cspace:latest entrypoint runs as `dev` (USER dev in the
# Dockerfile), so the install fallback is mostly relevant for project-
# image scenarios where the image author wires their own root entry.
ensure_dep() {
    local pkg=$1 cmd=$2
    command -v "$cmd" >/dev/null 2>&1 && return 0
    if [ "$(id -u)" = "0" ] && command -v apt-get >/dev/null 2>&1; then
        echo "[cspace-entrypoint] installing $pkg (missing $cmd)..."
        apt-get update -qq 2>/dev/null && \
            apt-get install -y -qq --no-install-recommends "$pkg" >/dev/null 2>&1 || \
            echo "[cspace-entrypoint] WARN: failed to install $pkg"
    else
        echo "[cspace-entrypoint] WARN: $cmd missing; install in your image (see docs/image-dependencies.md)"
    fi
}

ensure_dep iptables iptables
ensure_dep dnsmasq dnsmasq

# Source extracted.env if present — written by cspace up after compose sidecars
# start and credential extraction runs. Contains KEY='value' lines (shell-safe
# single-quote escaped) so credential strings are imported before any
# user-visible env consumption (git helper, claude.json pre-seed, etc.).
if [ -f /sessions/extracted.env ]; then
    set -a
    # shellcheck source=/dev/null
    . /sessions/extracted.env
    set +a
fi

# Auto-configure git credential helper for github.com when gh is authed.
# `gh auth setup-git` writes:
#   credential.helper = !/usr/bin/gh auth git-credential
# to ~/.gitconfig. Idempotent.
if [ -n "${GH_TOKEN:-}" ] && command -v gh >/dev/null 2>&1; then
    gh auth setup-git 2>/dev/null || true
fi

# First-run gate suppression for interactive `claude` lives in two places now
# (Claude Code v2.1.119+ migrated config out of ~/.claude.json):
#   - durable config keys → ~/.claude/settings.json (written just below)
#   - onboarding/trust state → ~/.claude.json (merged AFTER `claude update`
#     further down, so the one-time schema migration doesn't strip it)
# The old ~/.claude.json pre-seed here was written in the pre-2.1.x schema and
# is silently ignored by current Claude Code, so it has been removed. See the
# "seed interactive onboarding state" block after the claude-update step.

# Write ~/.claude/settings.json so the cspace statusline shows up in
# interactive `claude`, and pre-accept the first-run gates that live in this
# (post-2.1.119) settings file. The statusline is baked into the image at
# /usr/local/bin/cspace-statusline.sh (its version is tied to the image,
# which `cspace up` keeps aligned with the running CLI). Projects can
# override with their own at /workspace/.cspace/scripts/statusline.sh.
#
# Gate-suppression keys (verified against Claude Code v2.1.183):
#   - theme: skips the "choose a text style" picker (paired with
#     hasCompletedOnboarding in ~/.claude.json, merged after claude update).
#   - permissions.defaultMode=bypassPermissions: cspace sandboxes are
#     disposable microVMs, so the agent runs without per-command approval
#     prompts. NOTE: the firewall.* config is parsed but egress filtering
#     is NOT implemented (see finding
#     2026-07-16-firewall-config-parsed-but-never-enforced) — do not lean
#     on a network allowlist as a safety layer here.
#   - skipDangerousModePermissionPrompt: pre-accepts the one-time
#     "running in Bypass Permissions mode" warning (the v2.1.x replacement
#     for the old bypassPermissionsModeAccepted key).
#   - enableAllProjectMcpServers: auto-trusts the project's own .mcp.json
#     servers (e.g. a convex sidecar) so no "New MCP server found" prompt.
#
# Rewritten every boot so changes to the resolution propagate without
# requiring the user to delete the file. enabledPlugins is re-added by
# cspace-install-plugins.sh after this, and claude merges its own keys.
SETTINGS_JSON="$HOME/.claude/settings.json"
mkdir -p "$HOME/.claude"
statusline_cmd="/usr/local/bin/cspace-statusline.sh"
[ -x "/workspace/.cspace/scripts/statusline.sh" ] && statusline_cmd="/workspace/.cspace/scripts/statusline.sh"
cat > "$SETTINGS_JSON" <<JSON
{
  "statusLine": {
    "type": "command",
    "command": "${statusline_cmd}"
  },
  "theme": "dark",
  "permissions": { "defaultMode": "bypassPermissions" },
  "skipDangerousModePermissionPrompt": true,
  "enableAllProjectMcpServers": true
}
JSON

# Inherit the host's git identity (user.name / user.email) so the agent's
# commits/rebases attribute correctly. cspace injects these as env vars at
# `cspace up` time (read via `git config --global --get` on the host).
# Signing is explicitly disabled — the microVM has no GPG or SSH agent, so
# a host `commit.gpgsign = true` would make every commit fail. Rewritten
# every boot to pick up host config changes.
if [ -n "${CSPACE_GIT_USER_NAME:-}" ]; then
    git config --global user.name "$CSPACE_GIT_USER_NAME"
fi
if [ -n "${CSPACE_GIT_USER_EMAIL:-}" ]; then
    git config --global user.email "$CSPACE_GIT_USER_EMAIL"
fi
git config --global commit.gpgsign false
git config --global tag.gpgsign false

# NAT loopback-bound listeners onto the microVM's external IP.
#
# Many dev servers default to binding 127.0.0.1 (vite, next dev,
# create-react-app, …). Inside a microVM, loopback is unreachable
# from outside — packets to <vmip>:<port> would normally be dropped
# by the martian-source filter when the destination is rewritten to
# 127.0.0.1. Two settings make this work without any project-side
# config change:
#
#   1. route_localnet=1  — allow packets routed to 127.0.0.1 from a
#      non-loopback interface (off by default).
#   2. PREROUTING DNAT   — rewrite all incoming TCP destinations to
#      127.0.0.1 (port preserved). 0.0.0.0 listeners still receive
#      because 0.0.0.0 means "any local address" including 127.0.0.1.
#
# The result: a service listening on 127.0.0.1:5174 inside the
# microVM is reachable from the host browser at <vmip>:5174 with no
# change to the dev server's bind address.
#
# Best-effort: failures here don't block the supervisor. Apple
# Container microVMs ship with iptables and CAP_NET_ADMIN; if a
# future runtime stripped them, sandboxes would just lose this
# convenience and need --host=0.0.0.0 in the dev server.
# Start dnsmasq forwarder so hostname resolution inside the sandbox
# matches the host. *.cspace.test queries → cspace daemon on the
# gateway (192.168.64.1:5354). Everything else → public resolvers.
# /etc/resolv.conf points at 127.0.0.1 so glibc's nss-dns sees a
# standard :53 nameserver and the port-mapping detail stays internal.
#
# Best-effort: if dnsmasq fails to start, leave /etc/resolv.conf as
# the substrate adapter set it (--dns 1.1.1.1 etc.). External name
# resolution still works; only in-sandbox *.cspace.test lookups
# break, which surfaces as NXDOMAIN — caller can investigate via
# /var/log/dnsmasq.log if it exists.
DNSMASQ=/usr/sbin/dnsmasq
if [ -x "$DNSMASQ" ]; then
    sudo install -m 0644 /dev/stdin /etc/dnsmasq.d/cspace.conf <<'EOF'
listen-address=127.0.0.1
port=53
no-resolv
no-hosts
bind-interfaces
server=/cspace.test/192.168.64.1#5354
server=1.1.1.1
server=8.8.8.8
EOF
    # --keep-in-foreground=no to background it; remove default config
    # path inheritance via --conf-file= so we use ours exclusively.
    sudo "$DNSMASQ" --conf-file=/etc/dnsmasq.d/cspace.conf 2>/dev/null || true
    # Repoint glibc's resolver. Apple Container's network init wrote
    # the original resolv.conf earlier (with the --dns flags from
    # substrate Run); we replace it with a single localhost entry.
    sudo install -m 0644 /dev/stdin /etc/resolv.conf <<'EOF'
nameserver 127.0.0.1
options edns0 trust-ad
search .
EOF
fi

IPTABLES=/usr/sbin/iptables
SYSCTL=/usr/sbin/sysctl
if [ -x "$IPTABLES" ] && [ -x "$SYSCTL" ]; then
    # Pick the primary external interface (eth0 on Apple Container).
    ext_if=$(ip -o route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="dev") print $(i+1)}')
    if [ -n "$ext_if" ]; then
        sudo "$SYSCTL" -w "net.ipv4.conf.${ext_if}.route_localnet=1" >/dev/null 2>&1 || true
        sudo "$SYSCTL" -w "net.ipv4.conf.all.route_localnet=1" >/dev/null 2>&1 || true
        # Idempotency: check whether the rule already exists before
        # appending — useful if the entrypoint is re-run for any reason.
        if ! sudo "$IPTABLES" -t nat -C PREROUTING -i "$ext_if" -p tcp -j DNAT --to-destination 127.0.0.1 2>/dev/null; then
            sudo "$IPTABLES" -t nat -A PREROUTING -i "$ext_if" -p tcp -j DNAT --to-destination 127.0.0.1 2>/dev/null || true
        fi
    fi
fi

# Boot status file (read by the host overlay to advance phases). The
# /sessions mount is bind-mounted from the host's
# ~/.cspace/sessions/<project>/<sandbox>/, so writes here are visible
# to the parent cspace up immediately. Single-word state machine:
# 'plugins' → 'supervisor' → done (file removed by supervisor at
# /health-ready time).
STATUS_FILE=/sessions/cspace-init.status
mkdir -p /sessions 2>/dev/null || true

# Keep Claude Code current. cspace drives Claude through the Agent SDK in
# headless mode, which never triggers the CLI's built-in auto-updater, and the
# binary baked into the image is frozen at build time. Self-update on every
# boot so a fresh `cspace up` always starts on the latest release regardless of
# image age. Runs before plugin install so plugins resolve against the updated
# CLI. Best-effort and time-boxed: offline boots, a hung network, or an update
# failure must not block the supervisor — we fall back to the baked binary.
# Set CSPACE_SKIP_CLAUDE_UPDATE=1 to skip (air-gapped hosts / faster boots).
if [ -z "${CSPACE_SKIP_CLAUDE_UPDATE:-}" ] && command -v claude >/dev/null 2>&1; then
    echo "[cspace-entrypoint] updating Claude Code (best-effort)..."
    if timeout 120 claude update >/dev/null 2>&1; then
        echo "[cspace-entrypoint] Claude Code now at $(claude --version 2>/dev/null || echo '?')"
    else
        echo "[cspace-entrypoint] claude update skipped (offline, timed out, or failed); using baked version"
    fi
fi

# Install Claude Code plugins declared in /workspace/.claude/settings.json.
# Idempotent and best-effort — failures here don't block the supervisor.
# Marketplaces are added on first install via `claude plugins marketplace
# add <owner/repo>`, which clones from GitHub at runtime; pre-baking the
# marketplace trees into the image is a future optimization.
if [ -x /usr/local/bin/cspace-install-plugins.sh ]; then
    echo plugins > "$STATUS_FILE" 2>/dev/null || true
    /usr/local/bin/cspace-install-plugins.sh || true
fi

# Seed interactive onboarding/trust state into ~/.claude.json. This runs LATE
# — after `claude update` and the plugin install, both of which invoke claude
# and trigger its one-time v2.1.x config migration. Seeding earlier (as cspace
# did historically) gets stripped by that migration, which is exactly why the
# first-run screens came back. We merge (not overwrite) so claude's own runtime
# keys are preserved, and re-merge every boot (idempotent). hasCompletedOnboarding
# skips the theme/welcome flow; the per-project keys skip the "trust this folder"
# dialog for /workspace. Auth rides CLAUDE_CODE_OAUTH_TOKEN / ANTHROPIC_API_KEY
# from the env and is applied silently once onboarding is marked complete. The
# bypass-mode + project-MCP gates are handled in ~/.claude/settings.json above.
CLAUDE_JSON="$HOME/.claude.json"
if command -v jq >/dev/null 2>&1; then
    [ -f "$CLAUDE_JSON" ] || echo '{}' > "$CLAUDE_JSON"
    _cj_tmp=$(mktemp)
    if jq '. + {hasCompletedOnboarding: true, theme: "dark"}
            | .projects = (.projects // {})
            | .projects["/workspace"] = ((.projects["/workspace"] // {}) + {
                hasTrustDialogAccepted: true,
                projectOnboardingSeenCount: 1,
                hasClaudeMdExternalIncludesApproved: true,
                hasClaudeMdExternalIncludesWarningShown: true
              })' "$CLAUDE_JSON" > "$_cj_tmp" 2>/dev/null; then
        mv "$_cj_tmp" "$CLAUDE_JSON"
    else
        rm -f "$_cj_tmp"
        echo "[cspace-entrypoint] WARN: failed to seed onboarding state in $CLAUDE_JSON"
    fi
fi

echo supervisor > "$STATUS_FILE" 2>/dev/null || true

# Devcontainer postCreateCommand — once per sandbox lifetime, run in
# the BACKGROUND so the supervisor can come up and answer /health
# while the slow first-boot work (pnpm install, schema push, seeds)
# finishes. The agent attaching via cspace attach may briefly see a
# half-prepared workspace; that's the same trade-off VS Code makes.
# Output streams to /sessions/postcreate.log for `cspace logs`-style
# inspection.
if [ -n "${CSPACE_POSTCREATE_CMD:-}" ] && [ ! -f /workspace/.cspace-postcreate-done ]; then
    (
        # Wait briefly for cspace up's orchestrator to finish writing
        # /sessions/extracted.env (compose sidecars must be healthy +
        # extractCredentials must have run). Re-source it so postCreate
        # sees the captured admin keys / tokens, even though the early
        # source at boot may have raced ahead of the file's appearance.
        if [ -n "${CSPACE_EXTRACT_CREDENTIALS_EXPECTED:-}" ]; then
            for _ in $(seq 1 60); do
                [ -f /sessions/extracted.env ] && break
                sleep 1
            done
        fi
        if [ -f /sessions/extracted.env ]; then
            set -a
            # shellcheck source=/dev/null
            . /sessions/extracted.env
            set +a
        fi
        echo "[postcreate] starting at $(date -Is)"
        # The entrypoint runs as the remoteUser (dev by default for
        # cspace:latest); su would prompt for a password. Run directly
        # via bash -c. If a future image runs the entrypoint as root,
        # use `runuser -u dev -- bash -c "..."` instead.
        if (cd /workspace && bash -c "${CSPACE_POSTCREATE_CMD}"); then
            touch /workspace/.cspace-postcreate-done
            echo "[postcreate] done at $(date -Is)"
        else
            echo "[postcreate] failed (continuing)"
        fi
    ) </dev/null >/sessions/postcreate.log 2>&1 &
fi

# Devcontainer postStartCommand — every boot, non-fatal. Also background.
if [ -n "${CSPACE_POSTSTART_CMD:-}" ]; then
    (
        echo "[poststart] starting at $(date -Is)"
        (cd /workspace && bash -c "${CSPACE_POSTSTART_CMD}") || true
        echo "[poststart] done at $(date -Is)"
    ) </dev/null >/sessions/poststart.log 2>&1 &
fi

# Project init hook. If the project ships /workspace/.cspace/init.sh,
# run it once per sandbox after the workspace is mounted but before
# the supervisor starts. Use cases: bootstrap a local backend (e.g.
# convex-init), seed local dev databases, run pnpm install eagerly so
# the agent's first `bun run dev` doesn't pause to install.
#
# Idempotency is the script's responsibility — we don't track a
# marker on cspace's side because container recreation wipes it
# anyway. The script should self-skip if already done (check for a
# marker file inside /workspace, etc.).
#
# Best-effort: failures don't block the supervisor. Output is
# captured to ~/.claude/cspace-init.log so the user can inspect.
INIT_SCRIPT=/workspace/.cspace/init.sh
if [ -x "$INIT_SCRIPT" ]; then
    INIT_LOG="$HOME/.claude/cspace-init.log"
    mkdir -p "$(dirname "$INIT_LOG")"
    echo "[$(date -Iseconds)] cspace-init: running $INIT_SCRIPT" >> "$INIT_LOG"
    "$INIT_SCRIPT" >> "$INIT_LOG" 2>&1 || \
        echo "[$(date -Iseconds)] cspace-init: exit $?" >> "$INIT_LOG"
fi

# Hand off to the actual command (cspace-supervisor by default).
exec "$@"
