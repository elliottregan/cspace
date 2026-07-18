---
title: daemon /lookup and /list serve registry entries including control tokens to any vmnet peer
date: 2026-07-17
kind: finding
status: open
category: bug
tags: daemon, security, tokens, vmnet
---

## Summary
The daemon's HTTP API binds 0.0.0.0 so sandboxes can reach it via the gateway, and `GET /lookup/<project>/<name>` / `GET /list` return full registry entries **including each sandbox's control token**, unauthenticated. Any sandbox (or any process on the vmnet) can fetch any sandbox's supervisor control token — and, since the browser-restart endpoint authenticates by those same tokens, its Bearer gate is only as strong as this exposure. This is pre-existing behavior that in-sandbox `cspace send` and `cspace browser restart` legitimately rely on for self-lookup.

## Details
- Tightening sketch: keep self-lookup working without handing out every token — e.g. sandboxes authenticate /lookup with their own token delivered via env at boot, responses redact tokens for non-matching requesters, or a gateway-facing variant of the API omits Token fields.
- When this is tightened, re-audit `POST /browser/restart/{project}` (its same-project Bearer check becomes meaningful) and consider `crypto/subtle` constant-time compare for the token gates at the same time (currently plain `==`; moot while tokens are served openly, deferred by the 2026-07-17 final review).
- In-sandbox `cspace send` (cmd_send.go resolveEntry) and `cspace browser restart` are the two clients to migrate.

## Updates
### 2026-07-18T14:00:00Z — @agent — status: open
filed from the browser-sidecar branch's final whole-branch review (documented pre-existing exposure; restart endpoint noted for joint re-audit)
