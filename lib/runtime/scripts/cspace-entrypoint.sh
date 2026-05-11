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

# Write ~/.claude/settings.json so the cspace statusline shows up in
# interactive `claude`. The statusline is baked into the image at
# /usr/local/bin/cspace-statusline.sh (its version is tied to the image,
# which `cspace up` keeps aligned with the running CLI). Projects can
# override with their own at /workspace/.cspace/scripts/statusline.sh.
#
# Rewritten every boot so changes to the resolution propagate without
# requiring the user to delete the file.
SETTINGS_JSON="$HOME/.claude/settings.json"
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
# matches the host. *.cspace2.local queries → cspace daemon on the
# gateway (192.168.64.1:5354). Everything else → public resolvers.
# /etc/resolv.conf points at 127.0.0.1 so glibc's nss-dns sees a
# standard :53 nameserver and the port-mapping detail stays internal.
#
# Best-effort: if dnsmasq fails to start, leave /etc/resolv.conf as
# the substrate adapter set it (--dns 1.1.1.1 etc.). External name
# resolution still works; only in-sandbox *.cspace2.local lookups
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
server=/cspace2.local/192.168.64.1#5354
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

# Install Claude Code plugins declared in /workspace/.claude/settings.json.
# Idempotent and best-effort — failures here don't block the supervisor.
# Marketplaces are added on first install via `claude plugins marketplace
# add <owner/repo>`, which clones from GitHub at runtime; pre-baking the
# marketplace trees into the image is a future optimization.
if [ -x /usr/local/bin/cspace-install-plugins.sh ]; then
    echo plugins > "$STATUS_FILE" 2>/dev/null || true
    /usr/local/bin/cspace-install-plugins.sh || true
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
