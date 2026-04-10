---
title: Architecture Overview
description: How cspace organizes containers, volumes, networking, and the Docker-outside-Docker pattern into isolated development environments.
sidebar:
  order: 1
---

cspace runs each task in its own devcontainer instance — a Docker container with Claude Code, SSH, a network firewall, and browser sidecars pre-installed. Instances are fully isolated from each other but share a few project-wide resources (agent memory and session logs) through Docker volumes.

## Instance model

Every `cspace up` or `cspace issue` invocation creates one devcontainer instance. Each instance gets:

- Its own **workspace volume** — a fresh clone of the project repository
- Its own **Claude Code state** — settings, conversation history, session data
- Its own **GitHub CLI config** — credentials and auth state
- Its own **browser sidecars** — a Playwright run-server and a headless Chromium CDP container
- Its own **Docker network** — services within an instance communicate by hostname

Instances are named after planets by default (`mercury`, `venus`, `earth`, `mars`, …) with deterministic port mappings. Custom names get Docker-assigned random ports.

```
cspace up              # → mercury (ports 5173, 4173)
cspace up              # → venus   (ports 5174, 4174)
cspace up my-task      # → my-task (random ports)
```

This model means multiple agents can work on different issues simultaneously without interfering with each other — each has its own branch, its own file system, and its own test environment.

## Shared services and volumes

While instances are isolated, two Docker volumes are shared across all instances in a project:

| Volume | Mount path | Purpose |
|--------|-----------|---------|
| `cspace-{project}-memory` | `/home/dev/.claude/projects/-workspace/memory` | Claude agent memory — lets agents learn from each other's work |
| `cspace-{project}-logs` | `/logs` | Session transcripts, structured event logs, and inter-agent messages |

These volumes are created as Docker external volumes the first time an instance is provisioned and persist across instance lifecycles.

:::tip
Browser sidecars can also run in **shared mode** instead of per-instance mode. Shared sidecars serve all instances from a single container pair on a dedicated `cspace-shared` bridge network, reducing resource usage for projects with many parallel agents:
:::

```bash
cspace shared up    # Start shared browser sidecars
cspace shared down  # Stop them
```

## Container image composition

All instances use the same Docker image, built from the cspace Dockerfile (`lib/templates/Dockerfile`). The image layers:

**Base**: `node:alpine` — lightweight Node.js runtime on Alpine Linux.

**System packages**:
- **VCS & GitHub**: `git`, `github-cli`
- **Search**: `ripgrep`
- **Firewall**: `iptables`, `ipset`, `bind-tools`, `jq`
- **Docker CLI**: `docker-cli`, `docker-cli-compose` (for the Docker-outside-Docker pattern)
- **SSH**: `openssh` with password auth enabled
- **Task runner**: `just`
- **Package managers**: `pnpm`

**Claude Code**: Installed as the `dev` user from the official install script. Git is configured to use HTTPS (containers have no access to the host SSH agent).

**Initialization scripts**: Copied from `lib/scripts/` into the image:
- `entrypoint.sh` — container startup orchestration
- `init-firewall.sh` — iptables allowlist setup
- `init-workspace.sh` — git clone, dependency install, credential setup
- `init-claude-plugins.sh` — Claude Code config, MCP server registration, hook setup

**Bundled cspace CLI**: The entire cspace repository is copied to `/opt/cspace` in the image with `CSPACE_HOME=/opt/cspace` set as an environment variable. This gives agents running inside containers access to the same `cspace` command the host has — enabling them to spawn sibling containers via the Docker socket.

:::note
Projects can override the Dockerfile by placing a custom one at `.cspace/Dockerfile`. See [Template Overrides](/configuration/template-overrides/) for the full list of overridable files.
:::

## Networking model

Each instance runs on its own Docker Compose network. Services within an instance (devcontainer, playwright, chromium-cdp, and any project-specific services like databases) can reach each other by hostname:

```
Instance network (e.g., mercury)
├── devcontainer     ← main workspace container
├── playwright       ← ws://playwright:3000/
├── chromium-cdp     ← chromium-cdp:9222
└── postgres         ← postgres:5432 (if configured)
```

The devcontainer exposes two host-mapped ports:
- **Port 5173** — development server (e.g., Vite)
- **Port 4173** — preview/production build server

An iptables firewall inside each devcontainer restricts outbound traffic to an allowlist of approved domains. See [Firewall & Security](/architecture/firewall-and-security/) for details.

## Docker-outside-Docker pattern

cspace uses **Docker-outside-Docker** (DooD), not Docker-in-Docker (DinD). The host's Docker socket is mounted into each container:

```yaml
volumes:
  - /var/run/docker.sock:/var/run/docker.sock
```

This means the Docker CLI inside a container talks to the **host Docker daemon**. Containers spawned from inside a devcontainer are siblings on the host, not nested children.

The entrypoint script detects the Docker socket's group ID at startup and adds the `dev` user to the matching group, ensuring `docker` commands work without `sudo`.

This pattern enables:
- **Agent-spawned containers** — a coordinator agent running inside a devcontainer can launch additional devcontainer instances via `cspace up`
- **Project services** — databases and other services defined in `.cspace/docker-compose.yml` run as sibling containers on the same network
- **No nested virtualization** — avoids the performance and complexity costs of running Docker inside Docker

:::note
The devcontainer requires `CAP_NET_ADMIN` and `CAP_NET_RAW` Linux capabilities for the iptables firewall, but no other elevated privileges.
:::

## Architecture diagram

```
Host machine
├── cspace CLI (installed globally)
│
├── Instance: mercury
│   ├── devcontainer (Claude Code, SSH, firewall)
│   │   ├── /workspace          ← project clone (instance-local volume)
│   │   ├── /home/dev/.claude   ← Claude Code state (instance-local volume)
│   │   ├── /logs               ← shared logs volume
│   │   └── /var/run/docker.sock ← host Docker socket (DooD)
│   ├── playwright (ws://playwright:3000)
│   ├── chromium-cdp (CDP on port 9222)
│   └── [project services: postgres, redis, etc.]
│
├── Instance: venus
│   ├── devcontainer
│   ├── playwright
│   ├── chromium-cdp
│   └── [project services]
│
└── Shared volumes (Docker external volumes)
    ├── cspace-{project}-memory  ← Claude agent memory (all instances)
    └── cspace-{project}-logs    ← transcripts, events, messages (all instances)
```

## Container lifecycle

1. **`cspace up [name]`** — provisions a new instance:
   - Assigns a name and port mappings
   - Creates shared volumes if they don't exist
   - Bundles the project repo (`git bundle`) and transfers it into the container
   - Starts the container via `docker compose up -d`
   - Runs workspace initialization (clone, install deps, credential setup)
   - Runs the project's post-setup hook (`.cspace/post-setup.sh`) if configured
   - Launches Claude Code or an autonomous agent session

2. **`cspace ssh [name]`** — connects to a running instance via SSH

3. **`cspace down [name]`** — destroys the instance and its local volumes (shared volumes persist)

4. **`cspace warm [names...]`** — pre-provisions containers without launching Claude, useful for the coordinator to prepare instances before assigning work
