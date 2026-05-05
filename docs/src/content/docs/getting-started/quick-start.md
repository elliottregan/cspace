---
title: Quick start
description: Boot your first sandbox and hit it from a browser in 5 minutes.
sidebar:
  order: 2
---

Assumes you've finished [installation](../installation/).

## 1. Set credentials

cspace needs an Anthropic credential to drive Claude Code:

```bash
cspace keychain init
```

Paste a long-lived API key (`sk-ant-api-...`) from <https://console.anthropic.com/settings/keys>. Stable across sessions; recommended for any multi-day work.

A short-lived OAuth token from `claude /login` is auto-discovered as a fallback. Convenient for first-run, but expires within hours.

## 2. Boot a sandbox

From inside any git project directory:

```bash
cspace up
```

cspace auto-picks a planet name (`mercury`, `venus`, …), provisions a per-sandbox git clone, boots the microVM, installs Claude Code plugins declared in your project's `.claude/settings.json`, and drops you into an interactive `claude` session.

To skip the auto-attach:

```bash
cspace up --no-attach
```

## 3. Talk to the agent

If you exited the interactive session, you can still send turns from the host:

```bash
cspace send mercury "your prompt here"
```

Or re-attach:

```bash
cspace attach mercury
```

## 4. Reach the dev server from your browser

If your project's dev server is listening on (say) port 5174 inside the sandbox, your host browser can hit it at:

```
http://mercury.<project>.cspace2.local:5174/
```

cspace handles loopback NAT — dev servers bound to `127.0.0.1` (vite, next dev, CRA defaults) are reachable from the host without any project-side `--host=0.0.0.0` change.

If you see vite's "Blocked request" page, add `.cspace2.local` to vite's allowedHosts:

```ts
// vite.config.ts
server: { allowedHosts: [".cspace2.local"] }
```

## 5. Tear down

```bash
cspace down mercury        # one sandbox
cspace down --all          # every sandbox in this project
```

## Common follow-ups

- [Configuration reference](../../configuration/configuration-reference/) — `.cspace.json` schema (resources, plugins, credentials).
- [CLI overview](../../cli-reference/overview/) — every subcommand.
- [Architecture](../../architecture/architecture-overview/) — DNS, hostnames, multi-project routing.
