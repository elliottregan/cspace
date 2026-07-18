---
title: TCP-connect-level probes pass wedged services; sidecar health checks need protocol-level asserts
date: 2026-07-17
kind: finding
status: open
category: observation
tags: browser-sidecar, health-check, probes, playwright
---

## Summary
During the 2026-07-17 sidecar OOM, the guest kernel kept accepting TCP on :3000 and :9222 while userspace was dead. Every TCP-connect-level check reported green (including resume-redux's `scripts/ensure-env.sh`, which printed "Playwright run-server reachable" and "Browser CDP reachable" immediately before the consumer hung forever in `browserType.connect` with no timeout output). A listening-but-wedged service is indistinguishable from a healthy one at the TCP layer.

## Details
- Protocol-level probes that do distinguish (both verified during the incident): CDP — `GET /json/version` expecting HTTP 200 with JSON; run-server — a real WebSocket handshake expecting `HTTP/1.1 101 Switching Protocols`.
- Apply wherever cspace gates on sidecar readiness (browser sidecar startup/reuse checks in `internal/cli/browser.go`, any orchestrator healthcheck for it), and recommend the same for project-side preflight scripts (resume-redux `ensure-env.sh`).
- Also worth a client-side note: Playwright's `browserType.connect` against a wedged endpoint hung well past its nominal 180s timeout with no reporter output — don't rely on the client to fail loudly.

## Updates
### 2026-07-18T01:55:00Z — @agent — status: open
filed during the 2026-07-17 sidecar OOM incident (resume-redux)
