---
title: Containers can't resolve *.cspace2.local — frontend URLs baked at build time only work from one vantage point
date: 2026-05-04
kind: finding
status: open
category: refactor
tags: substrate, dns, rc-blocker
---

## Summary
In v1, only the macOS host's resolver knows about *.cspace2.local. Containers ship with --dns 1.1.1.1 --dns 8.8.8.8 (per the substrate adapter's default), so a browser running inside a sandbox (e.g. playwright e2e) gets NXDOMAIN for the same hostname the host browser resolves cleanly. v0 sidestepped this with Traefik's reverse-proxy model: Traefik handled both host-header rewriting and same-hostname-everywhere resolution implicitly. v1 needs to handle the two concerns separately. This finding tracks the same-hostname-everywhere half — the half a host-rewriting HTTP proxy does NOT fix.

## Details
## Why this matters

A frontend's environment-baked URLs are the same string in the dev-server build no matter who renders them. Common patterns that hit this:

- `VITE_CONVEX_URL=http://convex.<sandbox>.cspace2.local` — frontend opens a websocket. Host browser: works. In-container playwright: NXDOMAIN.
- `VITE_API_URL=http://api.<sandbox>.cspace2.local` — same shape, same break.
- HMR websocket from vite (`http://<sandbox>.cspace2.local:5174/__vite_hmr`). The browser is told to connect via the dev server's hostname; if the e2e harness opens a different page, that page's HMR socket can't resolve.

In resume-redux, this was the load-bearing reason for choosing Traefik — host-side e2e and in-container e2e both needed the same `convex.<...>` URL to reach the same backend. Without it, you end up maintaining two parallel URL configs (one for "browser-on-host", one for "browser-in-container") and tests drift between them.

## Reproduction sketch (current state)

```
host$ dig +short mercury.space-game-demo.cspace2.local      # → 192.168.64.198 (works)
host$ container exec cspace-space-game-demo-mercury \
        bash -c 'getent hosts mercury.space-game-demo.cspace2.local'
                                                             # → no answer (NXDOMAIN)
```

The container's /etc/resolv.conf has `nameserver 1.1.1.1; nameserver 8.8.8.8` — public resolvers, no knowledge of *.cspace2.local.

## Three implementation paths

### Option I — DNS forwarding via the gateway (recommended)

Have the cspace daemon's DNS server bind on the host's gateway IP `192.168.64.1:5354` (in addition to its current `127.0.0.1:5354`). Pass `--dns 192.168.64.1` to every `container run` so containers query the daemon directly for any name. The daemon already speaks the `*.cspace2.local` answers correctly (verified for the host's resolver path).

Drawbacks:
- 192.168.64.1 may not be the right address on every Apple Container install — substrate.IP() implies it but it's worth empirical confirmation.
- Apple Container's `--dns` flag is per-container; we can't set a default outside our adapter.
- Port 5354 isn't in the standard /etc/resolv.conf format (which assumes :53). We'd need either: (a) bind on :53, (b) run the daemon's resolver via a small dnsmasq/Unbound forwarder in each container.

### Option II — Tiny per-container resolver shim

Run dnsmasq inside each sandbox at `127.0.0.1:53`, configured to forward `*.cspace2.local` queries to the gateway and fall through to public resolvers for everything else. Containers point /etc/resolv.conf at 127.0.0.1.

Drawbacks:
- New runtime dep inside the sandbox image.
- Forwarder process has to start before any agent network activity — wires into entrypoint timing.

### Option III — Static /etc/hosts injection

cspace up reads the registry and writes `<sandbox>.<project>.cspace2.local <ip>` lines into `/etc/hosts` of every running sandbox whenever the registry changes. No DNS at all — just hosts file.

Drawbacks:
- Stale on multi-host scenarios (one container booted, second container booted later — first container's /etc/hosts wouldn't auto-update).
- Doesn't generalize to wildcard names (e.g. arbitrary `*.cspace2.local` from a project's own setup).

## Recommendation

Start with **Option I** but bind the daemon on :53 of the gateway (in addition to :5354 on loopback). Pass `--dns 192.168.64.1` to substrate Run. Containers get exactly the same name resolution as the host, no per-container init, no extra processes, no /etc/hosts churn.

If :53 binding turns out to be impossible (port-conflict on the macOS host), fall back to Option II.

## Cross-references

- Substrate Run defaults: internal/substrate/applecontainer/adapter.go (--dns flags)
- Daemon DNS handler: internal/cli/cmd_daemon.go (daemonDNSListenAddr)
- Host resolver routing: internal/cli/cmd_dns.go (/etc/resolver/cspace2.local with port 5354)

## Updates
### 2026-05-04T00:59:05Z — @agent — status: open
filed
