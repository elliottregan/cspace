---
title: Apple Container default DNS is broken; sandboxes can't resolve hostnames
date: 2026-05-01
kind: finding
status: open
category: bug
tags: networking, apple-container, dns, p1-blocker
related: docs/superpowers/spikes/2026-05-01-github-access-spike.md, docs/superpowers/spikes/2026-05-01-playwright-spike.md, docs/superpowers/spikes/2026-04-30-apple-container-spike.md
---

## Summary
Containers launched by Apple Container 0.12.3 ship with `/etc/resolv.conf` pointing at the host gateway `192.168.64.1`, but that gateway does not answer port 53 for any name (external or sibling). Both Phase 0 extension spikes (GitHub access and Playwright) hit this independently. Symptom is failures across the board — `gh` reports "invalid token" because requests never reach GitHub, `npm install` fails to resolve registries, Chromium navigation returns `net::ERR_NETWORK_CHANGED`. HTTPS itself works fine when an IP is given directly (`curl --resolve api.github.com:443:140.82.121.6` succeeds), so this is purely a name-resolution problem. P1 must address before any cspace2 sandbox is shipped to users.

## Details
## Reproduction

```bash
container run --rm docker.io/library/alpine:latest sh -c '
  apk add -q bind-tools curl
  cat /etc/resolv.conf
  nslookup api.github.com 192.168.64.1
  nslookup api.github.com 8.8.8.8
'
```

Expected output: `nameserver 192.168.64.1` in resolv.conf; `nslookup ... 192.168.64.1` times out; `nslookup ... 8.8.8.8` succeeds.

## Impact (verified in spikes)

- **gh CLI auth.** Reports "The token in GH_TOKEN is invalid" when the actual failure is name resolution. Cost ~5 min of debugging time on the first encounter; will burn agent token budget if not fixed before P1.
- **git clone over HTTPS.** Fails to resolve `github.com`.
- **npm install / package fetch.** Fails to resolve registry.npmjs.org.
- **Chromium / Playwright navigation.** Returns `net::ERR_NETWORK_CHANGED` for any URL.
- **MCP servers reaching external APIs** (any HTTP-based MCP server hitting the public internet by name).

## Root cause (suspected)

Either:
1. Apple Container's `container-network-vmnet` plugin doesn't set up DNS forwarding through the gateway, OR
2. The host macOS doesn't run a DNS forwarder bound to `192.168.64.1:53`, so the IP is reachable for ICMP/TCP but not for DNS specifically, OR
3. Some OS-level firewall (macOS application firewall, security policy) is blocking inbound :53 to the bridge interface.

Confirming the root cause requires deeper investigation that's out of scope for the spike — fixing the symptom is the P1 priority.

## Mitigations (priority order, all viable for P1)

1. **Bake static nameservers into the cspace2 sandbox image** at the end of `Dockerfile.cspace2`:
   ```dockerfile
   RUN echo 'nameserver 1.1.1.1' > /etc/resolv.conf \
    && echo 'nameserver 8.8.8.8' >> /etc/resolv.conf
   ```
   Simplest fix; works without touching the substrate adapter. Downside: hard-coded resolvers may not be ideal for users on networks where these are blocked or slower than local resolvers.

2. **Pass `--dns 1.1.1.1 --dns 8.8.8.8` via `container run`** at sandbox-create time. Apple Container 0.12.3 supports `--dns` per `container run --help`. Substrate adapter (`internal/substrate/applecontainer/adapter.go:Run`) appends these to the args. This keeps the resolver choice configurable in `RunSpec` rather than baked into the image.
   - Recommended approach: extend `RunSpec` with a `DNS []string` field, default to `["1.1.1.1","8.8.8.8"]` if empty, allow override via `.cspace.json` for projects on restricted networks.

3. **Run a DNS forwarder on the host** at `192.168.64.1:53` so the gateway IP "just works" as the spec was assumed to. Most invasive (modifies host system state) and least portable; investigate but don't ship as the default.

## Recommendation

Implement (2) — extend `RunSpec` with `DNS []string`. Land this change in P1 Task 8 (substrate adapter hardening) since it's a substrate-shape change. Default to Cloudflare + Google public DNS; document that users on restricted networks should set `dns` in `.cspace.json`.

## Tracking

- Watch apple/container issue tracker for native fix; if/when one ships, the explicit `--dns` flag becomes redundant but harmless.
- After fix lands, verify each Phase 0 spike repro path works without manual `--dns` injection, then close.

## Related code

- Spec section to update in P1: "Substrate" in `docs/superpowers/specs/2026-04-30-sandbox-architecture-design.md` — note the DNS requirement.
- Adapter: `internal/substrate/applecontainer/adapter.go:Run` (where `--dns` flags would be appended).
- Run spec: `internal/substrate/substrate.go:RunSpec` (where `DNS []string` field would land).

## Updates
### 2026-05-01T03:08:53Z — @agent — status: open
filed
