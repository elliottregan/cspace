---
title: Browser Sidecar & MCP Servers
description: How cspace provides browser access for agents, MCP tools, E2E tests, and host development
---

## Overview

Every cspace instance includes a **browser sidecar** — a container running headless Chrome and Playwright alongside the agent container. This sidecar powers three key capabilities:

- **MCP tools** — Playwright MCP and Chrome DevTools MCP give Claude browser automation and page inspection
- **E2E testing** — Playwright test runner connects to the sidecar for end-to-end tests
- **Host access** — developers access the app and database through `cspace.local` hostnames in their browser

The browser sidecar has **unrestricted network access** (no firewall), so it can reach external URLs (deployed sites, documentation) and container-hosted sites — while the agent container stays firewalled.

## Architecture

```
┌─────────────────────────────────────────────────────┐
│ Host (macOS)                                        │
│                                                     │
│  Browser ──► mercury.resume-redux.cspace.local      │
│              ↓ (CoreDNS → 127.0.0.1)               │
│              ↓ (Traefik routes by hostname)          │
│                                                     │
├─────────────────────────────────────────────────────┤
│ Docker                                              │
│                                                     │
│  ┌──────────────────┐    ┌────────────────────┐     │
│  │ cspace container  │    │ browser sidecar    │     │
│  │ (firewalled)      │    │ (unrestricted)     │     │
│  │                   │    │                    │     │
│  │ Claude Code ──────────► Chrome CDP (9222)   │     │
│  │  ├─ Playwright MCP│    │ Playwright (3000)  │     │
│  │  └─ DevTools MCP  │    │                    │     │
│  │                   │    │ Can reach:         │     │
│  │ App servers ◄─────────── - App via Traefik  │     │
│  │  ├─ Dev (3000)    │    │  - External URLs   │     │
│  │  └─ Preview (4173)│    │  - Convex DB       │     │
│  └──────────────────┘    └────────────────────┘     │
│                                                     │
│  ┌──────────────────┐                               │
│  │ convex-backend   │                               │
│  │  API (3210)      │                               │
│  └──────────────────┘                               │
└─────────────────────────────────────────────────────┘
```

### Browser sidecar

A single container (`${CSPACE_CONTAINER_NAME}.browser`) based on `mcr.microsoft.com/playwright` runs two services:

- **Headless Chrome** on port 9222 — Chrome DevTools Protocol (CDP) endpoint used by both MCP servers
- **Playwright run-server** on port 3000 — WebSocket server for E2E test execution

The sidecar joins both the instance network (for direct container communication) and the project network (for Traefik routing and hosts injection).

### MCP servers

Both browser MCP servers are **automatically registered** during container provisioning — no project configuration needed. They run inside the browser sidecar via `docker exec`, communicating with Claude Code over stdio.

**Playwright MCP** (`@playwright/mcp`) — browser automation for taking screenshots, clicking elements, filling forms, and navigating pages. Connects to Chrome at `localhost:9222` inside the sidecar.

**Chrome DevTools MCP** (`chrome-devtools-mcp`) — page inspection for reading DOM state, network requests, console logs, and performance data. Also connects to Chrome at `localhost:9222`.

Since both run inside the unrestricted browser sidecar, they can navigate to:
- Container-hosted sites (`mercury.resume-redux.cspace.local`)
- External URLs (`https://example.com`)
- Project services (`convex.mercury.resume-redux.cspace.local`)

## Host access

Developers access container-hosted sites from their browser using `cspace.local` hostnames. The [reverse proxy](/architecture/reverse-proxy/) handles DNS resolution and HTTP routing.

| Service | URL |
|---------|-----|
| Dev server | `http://mercury.resume-redux.cspace.local` |
| Preview server | `http://preview.mercury.resume-redux.cspace.local` |
| Convex backend | `http://convex.mercury.resume-redux.cspace.local` |

These same URLs work from:
- **Host browser** — via CoreDNS (127.0.0.1) → Traefik → container
- **Browser sidecar** — via `/etc/hosts` injection → Traefik → container
- **E2E tests** — Playwright browser in the sidecar reaches apps via the same URLs

### How container URLs work everywhere

The challenge: `cspace.local` resolves to `127.0.0.1` on the host (via CoreDNS), but `127.0.0.1` inside a container is not Traefik. cspace solves this by injecting `/etc/hosts` entries into all containers during provisioning, mapping `cspace.local` hostnames to Traefik's Docker network IP.

This means the same URL (e.g., `http://convex.mercury.resume-redux.cspace.local`) works from the host browser AND from inside any container — including the Playwright browser during E2E tests.

## E2E testing

Playwright E2E tests connect to the browser sidecar via the `PW_TEST_CONNECT_WS_ENDPOINT` environment variable (`ws://browser:3000/`). The test runner launches in the agent container, but the browser executes in the sidecar.

This separation means:
- Tests run with the agent's firewall restrictions (can only reach allowed domains)
- The browser has unrestricted access to render pages that make external API calls
- Multiple instances can run E2E tests concurrently without browser conflicts

## Exposing project services

To make a project service (like a database) accessible from the host and browser sidecar, add Traefik labels in the project's `.devcontainer/docker-compose.yml`. For example, a Convex backend:

```yaml
convex-backend:
    labels:
      - "traefik.enable=true"
      - "traefik.docker.network=${CSPACE_PROJECT_NETWORK}"
      - "traefik.http.routers.${CSPACE_CONTAINER_NAME}-convex.rule=Host(`convex.${CSPACE_INSTANCE_NAME}.${CSPACE_PROJECT_NAME}.cspace.local`)"
      - "traefik.http.routers.${CSPACE_CONTAINER_NAME}-convex.entrypoints=web"
      - "traefik.http.routers.${CSPACE_CONTAINER_NAME}-convex.service=${CSPACE_CONTAINER_NAME}-convex"
      - "traefik.http.services.${CSPACE_CONTAINER_NAME}-convex.loadbalancer.server.port=3210"
```

This routes `convex.mercury.resume-redux.cspace.local` to the Convex backend on port 3210. The `convex-nuxt` project template includes these labels out of the box.
