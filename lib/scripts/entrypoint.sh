#!/bin/bash
# Container entrypoint — runs initialization scripts before main command

# Docker-outside-of-Docker: grant the dev user access to the host's Docker socket.
# The socket GID varies across runtimes (OrbStack, Colima, Docker Desktop),
# so we detect it at startup and create a matching group.
if [ -S /var/run/docker.sock ]; then
    SOCK_GID=$(stat -c '%g' /var/run/docker.sock)
    if ! getent group "$SOCK_GID" >/dev/null 2>&1; then
        addgroup -g "$SOCK_GID" -S docker 2>/dev/null || true
    fi
    DOCKER_GROUP=$(getent group "$SOCK_GID" | cut -d: -f1)
    adduser dev "$DOCKER_GROUP" 2>/dev/null || true
fi

# Initialize firewall (iptables rules are kernel state — must re-apply on every start)
/usr/local/bin/init-firewall.sh

# Initialize Claude plugins (runs as root, script handles permissions)
/usr/local/bin/init-claude-plugins.sh

# Symlink bundled cspace skills into the dev user's claude skills dir.
# Symlinks created inside the volume-backed ~/.claude path persist across
# restarts. The targets resolve through /opt/cspace/lib/skills/ so a
# `cspace rebuild` automatically refreshes the content.
if [ -d /opt/cspace/lib/skills ]; then
    mkdir -p /home/dev/.claude/skills
    for skill_dir in /opt/cspace/lib/skills/*/; do
        [ -d "$skill_dir" ] || continue
        name=$(basename "$skill_dir")
        target="/home/dev/.claude/skills/$name"
        if [ ! -L "$target" ] && [ ! -e "$target" ]; then
            ln -s "$skill_dir" "$target"
        fi
    done
    chown -R dev:dev /home/dev/.claude/skills 2>/dev/null || true
fi

# Execute the original command (sshd)
exec "$@"
