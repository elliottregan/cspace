---
title: Architecture overview
description: How cspace v1 organizes microVMs, networking, persistence, and the agent supervisor.
sidebar:
  order: 1
---

cspace v1 spins up isolated dev environments as Apple Container microVMs, each running a long-lived Bun-based supervisor that drives Claude Code via the SDK. The host stays clean — no Docker, no Compose, no reverse proxy.

## Components

```
host
├── cspace CLI (Go binary, brew-installed)
├── cspace daemon — registry HTTP (:6280) + DNS (:5354 on loopback + gateway)
└── per-project state in ~/.cspace/
    ├── clones/<project>/<sandbox>/   — bind-mounted as /workspace
    ├── sessions/<project>/<sandbox>/ — supervisor events.ndjson + Claude SDK JSONLs
    └── secrets.env                    — credentials

microVM (Apple Container, image cspace:latest)
├── /workspace — host clone (read-write)
├── /home/dev — Claude state (sessions, plugins)
├── cspace supervisor (Bun TS, listens on :6201)
├── dnsmasq (forwards *.cspace2.local to gateway)
└── iptables PREROUTING DNAT (loopback → external IP)
```

## Networking

Every sandbox gets a routable IP on `192.168.64.0/24` (Apple Container's vmnet bridge). Friendly hostnames resolve through cspace's daemon:

- **Host browser** → `<sandbox>.<project>.cspace2.local` → resolver routes through daemon @ `127.0.0.1:5354` → microVM IP.
- **In-container playwright/e2e** → same hostname → in-VM dnsmasq forwards `.cspace2.local` to daemon @ `192.168.64.1:5354` → microVM IP.

Both vantage points see the same hostname-to-IP mapping, so a frontend URL like `https://api.<sandbox>.cspace2.local` works identically from a host browser AND a sandboxed playwright run.

Loopback NAT inside the microVM rewrites incoming TCP destinations to `127.0.0.1`, so dev servers bound to loopback (vite, next dev, CRA defaults) are reachable without project-side `--host=0.0.0.0` changes.

## Persistence

Two host-side directories survive sandbox tear-down:

- **`~/.cspace/clones/<project>/<sandbox>/`** — full git clone with the `cspace/<sandbox>` branch checked out. Bind-mounted as `/workspace`. Origin points at the host's upstream remote so `gh` works inside the sandbox; the host filesystem path is preserved as a `host` remote.
- **`~/.cspace/sessions/<project>/<sandbox>/`** — supervisor `events.ndjson` plus Claude SDK session JSONLs. Bind-mounted at `/sessions` and `/home/dev/.claude/projects/-workspace/`. Resume across `cspace down` + `cspace up` cycles works without any extra coordination because the supervisor reads its own `events.ndjson` at startup.

Containers themselves are cattle — each `cspace down` removes the container, each `cspace up` recreates it. State is on the host.

## Supervisor

The Bun TS supervisor wraps `@anthropic-ai/claude-agent-sdk` `query()` and serves:

- `POST /send` — inject a user turn (called by `cspace send`).
- `GET /health` — readiness signal for `cspace up`'s boot wait.

Events from the SDK stream into `events.ndjson` in real time, so the host can read agent activity even after `cspace down`. The supervisor self-resumes the latest session_id on startup, making cycle-to-cycle continuity transparent.

## Image

A single image `cspace:latest` carries Node, Bun, pnpm, Go, ripgrep, jq, plus Claude Code and the supervisor binary. Built locally from the embedded Dockerfile via `cspace image build`. Image distribution to ghcr.io is [issue #68](https://github.com/elliottregan/cspace/issues/68).

## Daemon

A single host-side process serves both registry HTTP (for in-sandbox `cspace send`) and DNS (for the friendly hostnames). Lazy-spawned by `cspace up`; idle-shuts down 30 minutes after the last sandbox exits.
