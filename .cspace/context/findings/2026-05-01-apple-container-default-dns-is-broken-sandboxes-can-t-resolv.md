---
title: Apple Container default DNS is broken; sandboxes can't resolve hostnames
date: 2026-05-01
kind: finding
status: acknowledged
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
## Decision

User reviewed the three mitigation options and locked in **Option A** (extend `RunSpec` with `DNS []string`, pass via `--dns` flags from the substrate adapter, default to `["1.1.1.1","8.8.8.8"]`, project override via `.cspace.json`).

## Verification

Before committing the plan change, verified that Apple Container 0.12.3 actually supports `--dns` and that the injected resolvers solve the symptom:

```
$ container run --help | grep -i dns
  --dns <ip>              DNS nameserver IP address
  --dns-domain <domain>   Default DNS domain
  --dns-option <option>   DNS options
  --dns-search <domain>   DNS search domains
  --no-dns                Do not configure DNS in the container

$ container run --rm --dns 1.1.1.1 --dns 8.8.8.8 docker.io/library/alpine:latest \
    sh -c 'cat /etc/resolv.conf; echo ---; nslookup -timeout=3 api.github.com'
nameserver 1.1.1.1
nameserver 8.8.8.8
---
Non-authoritative answer:
Name:    api.github.com
Address: 140.82.114.6
```

Confirms: `--dns` flags overwrite `/etc/resolv.conf` inside the sandbox, and DNS resolution succeeds against the public resolvers. Same flow that was previously hanging on `192.168.64.1:53` now works.

## Implementation

P1 plan Task 8 updated with the concrete steps:

- Add `DNS []string` to `substrate.RunSpec`.
- Adapter `Run`: append `--dns <ns>` for each entry; default to `["1.1.1.1","8.8.8.8"]` when `spec.DNS` is empty.
- Add `Sandbox.DNS []string` to project config so users on networks where public resolvers are blocked can override via `.cspace.json`.
- Smoke test: `container exec ... cat /etc/resolv.conf` shows injected resolvers; `getent hosts api.github.com` returns a real IP.

Status moved from `open` to `acknowledged` — fix is designed and verified, awaiting P1 implementation. Will move to `resolved` once the change lands and a fresh `cspace2-up` shows working DNS without manual intervention.
