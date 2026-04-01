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

# Execute the original command (sshd)
exec "$@"
