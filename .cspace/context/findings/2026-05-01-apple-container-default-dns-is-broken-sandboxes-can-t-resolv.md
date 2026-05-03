---
title: Apple Container default DNS is broken; sandboxes can't resolve hostnames
date: 2026-05-01
kind: finding
status: resolved
category: bug
tags: networking, apple-container, dns, p1-blocker
related: docs/superpowers/spikes/2026-05-01-github-access-spike.md, docs/superpowers/spikes/2026-05-01-playwright-spike.md, docs/superpowers/spikes/2026-04-30-apple-container-spike.md
---

## Summary
Containers launched by Apple Container 0.12.3 ship with `/etc/resolv.conf` pointing at the host gateway `192.168.64.1`, but that gateway does not answer port 53 for any name (external or sibling). Both Phase 0 extension spikes (GitHub access and Playwright) hit this independently. Symptom is failures across the board — `gh` reports "invalid token" because requests never reach GitHub, `npm install` fails to resolve registries, Chromium navigation returns `net::ERR_NETWORK_CHANGED`. HTTPS itself works fine when an IP is given directly (`curl --resolve api.github.com:443:140.82.121.6` succeeds), so this is purely a name-resolution problem. P1 must address before any cspace2 sandbox is shipped to users.

## Details


## Updates
### 2026-05-01T03:08:53Z — @agent — status: open
filed

### 2026-05-01T03:17:53Z — @agent — status: acknowledged

### 2026-05-01T04:44:53Z — @agent — status: resolved
## Landed in P0 extension branch (early)

Originally scheduled for P1 Task 8. Promoted to P0 extension because Claude auth is fully blocked without it (api.anthropic.com fails to resolve), and Claude auth is the gate for every subsequent real-Claude spike.

Implementation:
- `internal/substrate/substrate.go` — `RunSpec.DNS []string` field added.
- `internal/substrate/applecontainer/adapter.go:Run` — appends `--dns <ns>` per entry; defaults `["1.1.1.1", "8.8.8.8"]` when empty.

Verified end-to-end via `scripts/spikes/2026-05-01-claude-auth.sh` (PASS, real Claude response with `is_error: false`). Status moves to `resolved`.

P1 still owns adding `Sandbox.DNS []string` to project config so users on networks where public resolvers are blocked can override via `.cspace.json` — that wiring is not in this commit but the substrate layer is ready for it.
