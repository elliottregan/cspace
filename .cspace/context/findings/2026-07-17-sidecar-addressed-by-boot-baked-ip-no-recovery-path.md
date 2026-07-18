---
title: sidecar addressed by boot-baked raw IP; restarting it strands every sandbox, and agents have no recovery path
date: 2026-07-17
kind: finding
status: open
category: bug
tags: browser-sidecar, dns, addressing, recovery, daemon, incident
---

## Summary
Sandboxes reach the browser sidecar via **raw IPs baked into env vars at sandbox boot** — observed in a live sandbox: `PW_TEST_CONNECT_WS_ENDPOINT=ws://192.168.64.103:3000/`, `CSPACE_BROWSER_CDP_URL=http://192.168.64.103:9222`, `PLAYWRIGHT_MCP_CDP_ENDPOINT=http://192.168.64.103:9222`. No hostname, no `/etc/hosts` entry, no DNS record (`getent hosts playwright` fails). When the wedged sidecar was restarted on 2026-07-17 it came back on a new vmnet IP (.103 → .104), which meant a *successful* restart still left all three running sandboxes pointing at a dead address — env of running containers cannot be updated from outside. Stopgap used: an in-sandbox `iptables -t nat OUTPUT` DNAT from the old IP to the new one.

Compounding it: agents inside sandboxes have **no recovery path at all** for a failed sidecar (no docker CLI, no substrate access, no cspace command that works in-sandbox) — a wedged sidecar strands every agent in the project until a human intervenes on the host. Elliott explicitly wants this fixed: "containers (agents) should at least have a way of restarting the sidecar if it fails or they shut it down."

## Details
Two complementary fixes:
1. **Stable addressing.** Give the sidecar a name that survives restarts, and point all env at the name instead of the IP. Options: register the sidecar in the daemon registry and serve `browser.<project>.cspace.test` from daemon DNS (the live-inspect IP path already handles per-query freshness); or inject/refresh an `/etc/hosts` alias in every project sandbox when the sidecar's IP changes (worse: requires touching N sandboxes on every change — DNS is the better fit since the daemon already resolves per-query).
2. **Agent-invocable restart.** A daemon HTTP endpoint (sandboxes already carry `CSPACE_REGISTRY_URL` to the gateway and a per-sandbox token) that stops/starts the project's sidecar, surfaced as `cspace browser restart` working both host-side and in-sandbox (sandboxmode detection exists). With stable addressing (1), a restart becomes transparent to consumers.

Incident evidence for why restart-by-hand is not enough: Apple Container's state went split-brain on the wedged guest — `container stop` failed (`deleteProcess … does not exist`, tripping on a hung exec session), `container kill` claimed "container is not running" while `container ls` said running; recovery required killing the host-side `container-runtime-linux` process to force reconciliation, then `container start`. Any automated restart path must handle this (kill host process as escalation, verify with protocol-level probes after).

## Updates
### 2026-07-18T01:55:00Z — @agent — status: open
filed during the 2026-07-17 sidecar OOM incident (resume-redux); requirement stated by Elliott mid-incident
