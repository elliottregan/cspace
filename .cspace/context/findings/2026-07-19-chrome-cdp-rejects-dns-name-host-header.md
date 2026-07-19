---
title: Chrome CDP rejects the sidecar DNS name via Host-header rebinding protection; CDP env vars are broken in rc.37 sandboxes
date: 2026-07-19
kind: finding
status: resolved
category: bug
tags: browser-sidecar, cdp, dns, chrome, rc37
---

## Summary
rc.37 switched sandbox env to the restart-stable name (`CSPACE_BROWSER_CDP_URL` / `PLAYWRIGHT_MCP_CDP_ENDPOINT` = `http://browser.<project>.cspace.test:9222`). Verified in a live sandbox (2026-07-19): `GET /json/version` via the DNS name returns **HTTP 500 in ~30ms** while the same request via raw IP returns 200 — Chrome's DevTools HTTP endpoint rejects any request whose `Host` header is not an IP address or `localhost` (DNS-rebinding protection; socat forwards the Host header through). Consequences:
- `cspace browser status` **in-sandbox** falsely reports `CDP :9222 FAIL` against a healthy sidecar (host-side status probes by IP and passes).
- CDP-over-HTTP consumers pointed at the name — notably `@playwright/mcp` / browser MCP, which discover the ws URL via `/json/version` — are likely broken in rc.37 sandboxes. The Playwright run-server path (`PW_TEST_CONNECT_WS_ENDPOINT`, raw WS) is unaffected: a full in-sandbox e2e run passed through it.

## Details
- Evidence: in-sandbox `curl http://browser.resume-redux.cspace.test:9222/json/version` → 500 (0.03s); `curl http://192.168.64.107:9222/json/version` → 200; `getent hosts` resolves the name correctly; no proxy env. Host-side `cspace browser status` simultaneously reported CDP ok.
- Fix direction (preferred): a sandbox-local loopback forwarder — entrypoint runs a tiny TCP relay `127.0.0.1:9222 → browser.<project>.cspace.test:9222` (per-connection dial resolves the name freshly, so sidecar restarts stay transparent), and the CDP env vars point at `http://127.0.0.1:9222` (Host: 127.0.0.1 passes Chrome's check). Run-server env keeps the DNS name. Relay candidates: socat (needs adding to the sandbox image) or a ~15-line Bun script (Bun is already in the image).
- Also fix `cspace browser status` to probe CDP in a Host-compatible way regardless (resolve the name to an IP first, or connect with an IP Host header) so status matches truth in both contexts.
- Non-fix options considered: Chrome has no flag to relax the Host check (`--remote-allow-origins` governs WS Origin, not HTTP Host); reverting CDP env to raw IPs reinstates the original restart-stranding bug this replaced.

## Updates
### 2026-07-19T17:20:00Z — @agent — status: open
filed from the first full in-sandbox e2e verification run (e2e-check sandbox); discovered by the sandbox agent's `cspace browser status` pre-check disagreeing with a passing run-server e2e

### 2026-07-19T18:30:00Z — @agent — status: resolved
Implemented the loopback-relay fix: the entrypoint writes and supervises a
~15-line Node relay (`127.0.0.1:9222` → `$CSPACE_BROWSER_HOST:9222`, fresh
dial per connection so sidecar restarts stay transparent — Node not socat,
which resolves its target only once). `browserEnvURLs` now emits
`http://127.0.0.1:9222` for `CSPACE_BROWSER_CDP_URL` /
`PLAYWRIGHT_MCP_CDP_ENDPOINT` (WS keeps the DNS name), cmd_up injects
`CSPACE_BROWSER_HOST`, and in-sandbox `cspace browser status` probes the
relay (the real consumer path). Requires an image rebuild to take effect
(entrypoint change). Live-verified in a fresh sandbox post-release.
