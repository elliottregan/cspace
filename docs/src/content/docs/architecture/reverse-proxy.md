---
title: Reverse Proxy
description: How cspace provides per-instance hostnames via Traefik and CoreDNS
sidebar:
  order: 7
---

## Overview

cspace runs a global Traefik reverse proxy alongside a CoreDNS sidecar to give every instance a stable, human-readable hostname. This replaces the need for OrbStack-specific DNS or remembering dynamic `localhost:PORT` mappings.

## Hostnames

Each service gets a hostname following the pattern:

```
<label>.<instance>.<project>.cspace.local
```

For example, an instance named `mercury` in a project called `resume-redux`:

| Service | URL |
|---------|-----|
| Dev server (port 3000) | `http://dev.mercury.resume-redux.cspace.local` |
| Preview server (port 4173) | `http://preview.mercury.resume-redux.cspace.local` |
| Project service (Convex API) | `http://convex.mercury.resume-redux.cspace.local` |

Multiple instances can all serve on the same internal port (e.g., 3000) without conflicts — each gets its own hostname. All labels follow the same pattern so URLs stay parallel between dev, preview, and deployed environments.

## Architecture

Two containers run as a global shared service on the host:

- **Traefik** (`cspace-proxy`) — listens on host port 80, auto-discovers instance containers via Docker labels, and routes HTTP requests by hostname to the correct container and port.
- **CoreDNS** (`cspace-dns`) — listens on host port 53, resolves all `*.cspace.local` queries to `127.0.0.1`.

Both are defined in a single compose stack. The source templates live at `lib/templates/proxy/`, but at runtime they are **copied to `~/.cspace/proxy/`** and docker compose runs from there. This ensures the bind-mounted Corefile is under `$HOME`, which is always in Docker Desktop's default shared file paths on macOS. See [Directory Structure](/architecture/directory-structure/#proxy) for details.

## How it works

1. **DNS resolution:** The host's `/etc/resolver/cspace.local` file directs `*.cspace.local` queries to `127.0.0.1:53` where CoreDNS listens. CoreDNS returns `127.0.0.1` for all queries.

2. **HTTP routing:** The browser sends an HTTP request to `127.0.0.1:80` with a `Host` header like `dev.mercury.resume-redux.cspace.local`. Traefik matches this against Docker labels on running containers and forwards the request to the correct container's internal port.

3. **Auto-discovery:** Instance containers in `docker-compose.core.yml` have Traefik labels that define routing rules. When a container starts or stops, Traefik updates its routing table automatically via the Docker socket.

4. **Network connectivity:** During `cspace up`, the proxy container is connected to the project's Docker network (`cspace-<project>`) so it can reach all instance containers.

## Setup

The DNS resolver is configured once per machine via `cspace init`, which creates `/etc/resolver/cspace.local` (requires sudo). The proxy containers start automatically on the first `cspace up`.

## Fallback

If the proxy is not running (e.g., first use before `cspace up`), the Claude Code statusline falls back to `localhost:PORT` URLs using `docker port` lookups.
