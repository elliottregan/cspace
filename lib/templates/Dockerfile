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

# Copy initialization scripts
COPY scripts/init-workspace.sh /usr/local/bin/init-workspace.sh
COPY scripts/init-firewall.sh /usr/local/bin/init-firewall.sh
COPY scripts/init-claude-plugins.sh /usr/local/bin/init-claude-plugins.sh
COPY scripts/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/init-workspace.sh \
    /usr/local/bin/init-firewall.sh \
    /usr/local/bin/init-claude-plugins.sh \
    /usr/local/bin/entrypoint.sh

WORKDIR /workspace
EXPOSE 22

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["/usr/sbin/sshd", "-D"]
