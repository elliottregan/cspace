---
title: DNS suffix on .local hits macOS mDNS reservation; switched to .test
date: 2026-05-11
kind: finding
status: resolved
category: observation
tags: dns, macos, mdns, rename, resolver
---

## Summary
cspace's sandbox DNS suffix (cspace2.local, briefly cspace.local) sat under the .local TLD, which RFC 6762 reserves for Multicast DNS. macOS's per-domain resolver routing under .local works inconsistently — specifically, the negative cache in mDNSResponder is sticky in ways `killall -HUP mDNSResponder` (config reload) doesn't evict. After the cspace2 → cspace rename, the daemon kept serving the old suffix briefly while macOS was already routing the new one, producing NXDOMAINs that cached and persisted even after the daemon was corrected. Renamed to cspace.test (RFC 6761 reserved-for-testing TLD) in rc.24 to exit the .local reservation entirely.

## Details
**Failure mode observed (rc.21→rc.23 era, cspace.local suffix)**:
- `sudo cspace dns install` correctly wrote `/etc/resolver/cspace.local` (validated via `scutil --dns` — resolver routing showed cspace.local → 127.0.0.1:5354)
- `dig @127.0.0.1 -p 5354 mercury.<project>.cspace.local` returned the correct vmnet IP
- But `host`, `curl`, and the browser all returned NXDOMAIN for the same name
- `sudo killall -HUP mDNSResponder` did not fix it
- Working URL stayed `http://<vmip>:5173/` (direct IP, bypassing DNS)

**Root cause**: macOS treats .local as mDNS-priority. When a query under .local goes uncached, the per-domain resolver routing fires correctly. When it's negative-cached (NXDOMAIN), mDNSResponder serves the cache and skips the resolver. `-HUP` reloads config but preserves the negative cache.

**Reliable cache flush (still useful to know):**
```
sudo dscacheutil -flushcache
sudo killall mDNSResponder
```
NOT `-HUP` — that's a config reload. A full restart (no signal) is what evicts the cache. As of rc.24, `cspace dns install` runs both automatically after writing the resolver file, so renames don't strand users in this state.

**Why .test is safer than .local**:
- RFC 6761: .test is explicitly reserved for testing/development use, guaranteed never to be delegated as a public TLD
- No mDNS special-case — treated as plain unicast DNS by every resolver
- Widely used by local-dev tooling (Rails, Laravel Valet, dnsmasq templates)
- Same length as .local (5 chars)
- Keeps "cspace" branding intact (`cspace.test`)

**Alternatives considered and rejected**:
- `.cspace` bare TLD: works today, but unreserved. ICANN could theoretically delegate it; some browsers prepend search for unknown TLDs.
- `.cspace.internal`: less widely reserved; some VPN/MDM tools intercept .internal.
- `.cspace.dev`: .dev is a real ICANN TLD owned by Google with HSTS enforcement — won't load HTTP at all.

**Migration cost**: ~10 files touched in cspace (Go constants, dnsmasq forwarder in cspace-entrypoint.sh, statusline FQDN construction, browser sidecar resolv config, docs) plus one line in each consumer's `vite.config.ts` allowedHosts. `cspace dns install` carries a `legacyResolverFiles` list that auto-removes `/etc/resolver/cspace2.local` and `/etc/resolver/cspace.local` on next install after upgrade, so users don't have to clean up by hand.

**Lesson for future suffix changes**: avoid .local entirely. .test is the right home for service DNS on a developer's machine; the mDNS reservation is a recurring foot-gun, not a one-time issue.

## Updates
### 2026-05-11T22:31:19Z — @agent — status: resolved
filed
