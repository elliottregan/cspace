---
title: Containers can't resolve *.cspace2.local — frontend URLs baked at build time only work from one vantage point
date: 2026-05-04
kind: finding
status: resolved
category: refactor
tags: substrate, dns, rc-blocker
---

## Summary
In v1, only the macOS host's resolver knows about *.cspace2.local. Containers ship with --dns 1.1.1.1 --dns 8.8.8.8 (per the substrate adapter's default), so a browser running inside a sandbox (e.g. playwright e2e) gets NXDOMAIN for the same hostname the host browser resolves cleanly. v0 sidestepped this with Traefik's reverse-proxy model: Traefik handled both host-header rewriting and same-hostname-everywhere resolution implicitly. v1 needs to handle the two concerns separately. This finding tracks the same-hostname-everywhere half — the half a host-rewriting HTTP proxy does NOT fix.

## Details


## Updates
### 2026-05-04T00:59:05Z — @agent — status: open
filed

### 2026-05-04T01:26:39Z — @agent — status: resolved
Resolved by aaac318 + 5f9ddb4 + 1f68512 (Option I from the writeup).

- aaac318: cspace daemon now binds DNS on `192.168.64.1:5354` (the Apple Container vmnet gateway) in addition to `127.0.0.1:5354`. Best-effort — bind failure logs a warning but the loopback path keeps working.
- 5f9ddb4: sandbox image ships dnsmasq; entrypoint configures it at boot to listen on `127.0.0.1:53`, forward `*.cspace2.local` to `192.168.64.1#5354`, and fall through to `1.1.1.1`/`8.8.8.8` for everything else. `/etc/resolv.conf` is rewritten to point at `127.0.0.1` so glibc's nss-dns picks it up transparently.
- 1f68512: substrate adapter no longer defaults `--dns 1.1.1.1 8.8.8.8`; dnsmasq handles it.

Verified end-to-end: a host's `dig` AND a sandbox's `getent hosts mercury.space-game-demo.cspace2.local` both return `192.168.64.198`. External hostnames (github.com etc.) still resolve via the dnsmasq fallback. v0's Traefik gave us this behavior implicitly via reverse-proxy hostname unification; v1 reaches parity without the proxy.
