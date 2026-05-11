# cspace sandbox image. Lean default: only what cspace's runtime overlay
# needs to function. Projects pin their own tooling via devcontainer.json
# (image:, dockerFile:, features:) — see docs/devcontainer-subset.md.

# ── Stage 1: compile the Bun TS supervisor ─────────────────────────────────
# Produces a single Linux/arm64 ELF at /build/dist/cspace-supervisor that
# the runtime stage copies into /usr/local/bin/cspace-supervisor.
#
# This stage runs only during image build — the resulting binary is the
# cspace-supervisor process running inside every cspace sandbox. Source
# of truth lives in lib/agent-supervisor-bun/src/; no compiled artifact
# is tracked in git.
#
# IMPORTANT: We use the Debian-flavored Bun image (glibc), not -alpine
# (musl). The runtime stage below is node:24-bookworm-slim (glibc). A
# musl-linked supervisor binary fails at exec time in the runtime stage
# with "cannot execute: required file not found" because
# libc.musl-aarch64.so.1 isn't present. Keep the libc family aligned
# between builder and runtime.
FROM docker.io/oven/bun:1.3-debian AS supervisor-builder
WORKDIR /build
COPY lib/agent-supervisor-bun/package.json lib/agent-supervisor-bun/bun.lock ./
RUN bun install --frozen-lockfile
# Glob form ('src/*.ts') rather than directory form ('src') because Apple
# Container's builder silently produces an empty target directory for
# COPY <dir> ./<dir> when the build context is a fresh temp tree (e.g.
# `cspace image build` from a brew install — assets extracted to a tmp
# dir). The source-tree path happens to work, the temp-extract path
# doesn't. Globbing files explicitly side-steps the inconsistency.
COPY lib/agent-supervisor-bun/src/*.ts ./src/
COPY lib/agent-supervisor-bun/build.ts ./
RUN bun run build
# At this point /build/dist/cspace-supervisor exists.

# ── Stage 2: cspace runtime (lean) ─────────────────────────────────────────
# node:24-bookworm-slim provides:
#   - glibc (supervisor binary requirement)
#   - Node 24 + npm + corepack (most agents need npx for MCP servers)
#   - Debian apt for the few cspace-required deps not baked
#   - ~250 MB base
# See docs/image-dependencies.md for the full image dependency contract.
FROM docker.io/library/node:24-bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive

# Runtime-only system deps:
#   - iptables / iproute2: loopback NAT for ports bound to 127.0.0.1
#   - dnsmasq: DNS forwarder for *.cspace.local + bare-name service DNS
#   - bash / tini / sudo / procps: entrypoint + PID 1 + privilege drop + ps tools
#   - ca-certificates / curl / unzip / git: plugin install + marketplace clone
#   - jq / ripgrep: agent essentials (Claude Code uses rg heavily; jq in shell tooling)
#   - python3: hook scripts + occasional agent use (slim, no build-essential)
#   - gh: GitHub CLI for agent ops
#
# DROPPED from previous fat image (now project's job via devcontainer features
# or their own image): Go toolchain, pnpm 10 global, build-essential,
# Playwright Chromium browsers (the --browser sidecar provides chromium),
# playwright-mcp and chrome-devtools-mcp npm packages.
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    dnsmasq \
    gh \
    git \
    iproute2 \
    iptables \
    jq \
    procps \
    python3 \
    ripgrep \
    sudo \
    tini \
    unzip \
 && rm -rf /var/lib/apt/lists/*

# Non-root user (Claude Code refuses --dangerously-skip-permissions as root).
# node:24-bookworm-slim ships a `node` user at UID 1000; remove it first so
# the cspace-conventional `dev` user can claim that UID (bind mounts assume
# dev=1000 on the host side).
RUN userdel -r node 2>/dev/null || true \
 && useradd -m -s /bin/bash -u 1000 dev \
 && echo 'dev ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/dev \
 && chmod 0440 /etc/sudoers.d/dev

# corepack ON — projects' package.json `packageManager:` field transparently
# fetches the right pnpm/yarn version. We do NOT install pnpm globally here;
# corepack handles per-project pinning.
RUN corepack enable

# Bun + Claude Code CLI as the dev user (install lands in /home/dev).
USER dev
ENV HOME=/home/dev
ENV PATH=/home/dev/.bun/bin:/home/dev/.local/bin:/usr/local/bin:/usr/bin:/bin

RUN curl -fsSL https://bun.sh/install | bash \
 && /home/dev/.bun/bin/bun --version

# Claude Code CLI. The npm package is a thin wrapper around a
# platform-specific subpackage (e.g. @anthropic-ai/claude-code-linux-arm64),
# and `bun install --global` already creates /home/dev/.bun/bin/claude as a
# symlink to that platform binary.
RUN /home/dev/.bun/bin/bun install --global @anthropic-ai/claude-code \
 && /home/dev/.bun/bin/claude --version

# Symlink so scripts that hardcode /usr/local/bin/claude or /usr/local/bin/bun work.
USER root
RUN ln -sf /home/dev/.bun/bin/bun /usr/local/bin/bun \
 && ln -sf /home/dev/.bun/bin/claude /usr/local/bin/claude

# Browser MCP servers — clients only (no chromium binary baked).
# The supervisor's claude-runner.ts always registers these as MCP servers
# and passes --cdp-endpoint / --browser-url when CSPACE_BROWSER_CDP_URL is
# set. Without these binaries on PATH, the MCP registration fails at boot
# even when the project doesn't intend to use them.
# Chromium itself comes from a project-declared Playwright sidecar (see
# resume-redux/.devcontainer/docker-compose.yml for the pattern) or
# `cspace up --browser` (cspace-managed Chromium sidecar).
RUN npm install -g @playwright/mcp@0.0.72 chrome-devtools-mcp@0.23.0 \
 && playwright-mcp --version

# Repair home ownership after root-USER global installs landed under HOME=/home/dev.
RUN chown -R dev:dev /home/dev

# cspace (linux/arm64 host CLI) for in-sandbox `cspace send`/`cspace exec`.
COPY bin/cspace-linux-arm64 /usr/local/bin/cspace

# Bun supervisor single binary — built in the supervisor-builder stage above.
COPY --from=supervisor-builder /build/dist/cspace-supervisor /usr/local/bin/cspace-supervisor

# Workspace + sessions dirs are owned by dev so the agent can write them.
# The Claude Code SDK writes session JSONLs under
# ~/.claude/projects/-workspace/sessions/<session_id>.jsonl. Pre-create the
# parent directory tree as dev so a runtime bind mount of the host's
# claude-projects-workspace dir lands on a known, dev-owned target.
RUN mkdir -p /workspace /sessions /home/dev/.claude/projects/-workspace \
 && chown -R dev:dev /workspace /sessions /home/dev/.claude \
 && chmod +x /usr/local/bin/cspace /usr/local/bin/cspace-supervisor

# cspace runtime scripts and feature installers. All baked into the image so
# the image is self-contained and tied to the cspace CLI version it was built
# from — `cspace up` compares this image's cspace.version label against the
# running CLI and warns if they drift, recommending `cspace image build`.
# Editing files on the host does NOT change behavior inside the sandbox; the
# only way to update is to rebuild the image.
USER root
COPY lib/runtime/scripts/cspace-entrypoint.sh /usr/local/bin/cspace-entrypoint.sh
COPY lib/runtime/scripts/cspace-supervisor-loop.sh /usr/local/bin/cspace-supervisor-loop.sh
COPY lib/runtime/scripts/cspace-install-plugins.sh /usr/local/bin/cspace-install-plugins.sh
COPY lib/runtime/scripts/statusline.sh /usr/local/bin/cspace-statusline.sh
RUN chmod +x /usr/local/bin/cspace-entrypoint.sh \
            /usr/local/bin/cspace-supervisor-loop.sh \
            /usr/local/bin/cspace-install-plugins.sh \
            /usr/local/bin/cspace-statusline.sh

# Devcontainer feature scripts (node, python, common-utils, git, github-cli,
# docker-in-docker). Lives at /opt/cspace/features/ so internal/features/
# runner.go can invoke them by path. Not yet wired into the boot flow; the
# scripts ship so the wiring lands as a pure additive change in a later
# release.
RUN mkdir -p /opt/cspace/features
COPY lib/runtime/features/*.sh /opt/cspace/features/
RUN chmod +x /opt/cspace/features/*.sh
USER dev

# Version label set at build time via `--build-arg CSPACE_VERSION=...`.
# `cspace up` reads this label and warns when it doesn't match the running
# CLI, prompting `cspace image build`. Defaults to "dev" for ad-hoc builds.
ARG CSPACE_VERSION=dev
LABEL cspace.version="${CSPACE_VERSION}"

WORKDIR /workspace

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/cspace-entrypoint.sh"]
CMD ["/usr/local/bin/cspace-supervisor-loop.sh"]
