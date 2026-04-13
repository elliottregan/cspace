FROM node:alpine

# Install system dependencies for Claude Code, SSH, and GitHub CLI
RUN apk add --no-cache \
    git git-init-template curl ca-certificates openssh sudo github-cli bash \
    # Claude Code dependencies for native install
    libgcc libstdc++ ripgrep \
    # glibc compat layer (Alpine uses musl; some tools need glibc)
    gcompat \
    # Firewall dependencies
    iptables ipset bind-tools jq iproute2 \
    # Docker-outside-of-Docker: CLI talks to host daemon via mounted socket
    docker-cli docker-cli-compose \
    # Go toolchain
    go \
    # Task runner
    just && \
    npm install -g pnpm

# Configure SSH
RUN mkdir -p /var/run/sshd && \
    ssh-keygen -A && \
    echo 'PermitRootLogin no' >> /etc/ssh/sshd_config && \
    echo 'PasswordAuthentication yes' >> /etc/ssh/sshd_config && \
    echo 'PubkeyAuthentication yes' >> /etc/ssh/sshd_config

# Create non-root user
RUN adduser -D -s /bin/bash dev && \
    adduser dev wheel && \
    echo 'dev ALL=(ALL) NOPASSWD:ALL' >> /etc/sudoers.d/dev

# Set default password for dev user
RUN echo 'dev:devpass' | chpasswd

# Install Claude Code as dev user
USER dev
ENV PATH="/home/dev/.local/bin:$PATH"
ENV COLORTERM=truecolor
ENV LANG=C.UTF-8
# BASH_ENV is sourced by every bash invocation (including non-interactive
# `bash -c` from `docker exec`), giving us a hook to derive container env
# vars from others. /etc/cspace-bash-env aliases GITHUB_TOKEN -> GH_TOKEN
# so tooling that expects either name works.
ENV BASH_ENV=/etc/cspace-bash-env
RUN curl -fsSL https://claude.ai/install.sh | bash
# Use HTTPS for GitHub (container doesn't have SSH agent access)
RUN git config --global url."https://github.com/".insteadOf "git@github.com:"
USER root

# Readline key bindings for iTerm2 over SSH
RUN cat <<'EOF' > /home/dev/.inputrc
# Option+Delete: backward kill word
"\e\x7f": backward-kill-word
# Option+Left: move back one word
"\e[1;3D": backward-word
"\eb": backward-word
# Option+Right: move forward one word
"\e[1;3C": forward-word
"\ef": forward-word
# Home/End
"\e[H": beginning-of-line
"\e[F": end-of-line
EOF
RUN chown dev:dev /home/dev/.inputrc

# Workspace setup
RUN mkdir -p /workspace && chown dev:dev /workspace
RUN mkdir -p /home/dev/.claude && chown dev:dev /home/dev/.claude
RUN mkdir -p /logs && chown dev:dev /logs

# BASH_ENV hook: aliases GITHUB_TOKEN <-> GH_TOKEN so tooling expecting
# either name works. Sourced on every bash invocation (interactive or `-c`).
RUN cat > /etc/cspace-bash-env <<'EOF'
# Sourced by every bash invocation via BASH_ENV.
# Keep this file fast and side-effect-free.
if [ -n "${GH_TOKEN:-}" ] && [ -z "${GITHUB_TOKEN:-}" ]; then
    export GITHUB_TOKEN="$GH_TOKEN"
elif [ -n "${GITHUB_TOKEN:-}" ] && [ -z "${GH_TOKEN:-}" ]; then
    export GH_TOKEN="$GITHUB_TOKEN"
fi
EOF
RUN chmod 644 /etc/cspace-bash-env

# Copy initialization scripts (build context is the cspace repo root)
COPY lib/scripts/init-workspace.sh /usr/local/bin/init-workspace.sh
COPY lib/scripts/init-firewall.sh /usr/local/bin/init-firewall.sh
COPY lib/scripts/init-claude-plugins.sh /usr/local/bin/init-claude-plugins.sh
COPY lib/scripts/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/init-workspace.sh \
    /usr/local/bin/init-firewall.sh \
    /usr/local/bin/init-claude-plugins.sh \
    /usr/local/bin/entrypoint.sh

# Bake the entire cspace CLI + lib into the image at /opt/cspace.
# This gives agents inside containers the same `cspace` command the host has,
# letting them spawn sibling containers via the docker socket (DooD).
# The agent-supervisor's node_modules is installed at this canonical path,
# so /opt/cspace/lib/agent-supervisor/supervisor.mjs runs out of the box.
COPY lib/agent-supervisor/package.json lib/agent-supervisor/pnpm-lock.yaml /opt/cspace/lib/agent-supervisor/
RUN cd /opt/cspace/lib/agent-supervisor && pnpm install --frozen-lockfile --prod
COPY lib/ /opt/cspace/lib/
# bin/cspace-linux is a statically-linked Go binary compiled for linux.
# It is staged in the build context by `cspace rebuild` before docker build.
# Named cspace-linux to avoid overwriting the host's darwin binary at bin/cspace.
COPY bin/cspace-linux /opt/cspace/bin/cspace
RUN chmod +x /opt/cspace/bin/cspace && \
    ln -sf /opt/cspace/bin/cspace /usr/local/bin/cspace && \
    chown -R dev:dev /opt/cspace

# CSPACE_HOME tells the in-container cspace CLI and init scripts where the
# bundled lib lives. Set via ENV (not passed from host) so it always matches
# the in-image path regardless of where the host installed cspace.
ENV CSPACE_HOME=/opt/cspace

WORKDIR /workspace
EXPOSE 22

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["/usr/sbin/sshd", "-D"]
