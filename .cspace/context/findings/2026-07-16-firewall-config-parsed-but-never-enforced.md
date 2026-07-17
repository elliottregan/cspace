---
title: firewall.* config is parsed and merged but no egress filtering exists
date: 2026-07-16
kind: finding
status: acknowledged
category: bug
tags: firewall, security, config, entrypoint, deferred
---

## Summary
`defaults.json` ships `firewall.enabled=true` and `firewall.domains`; `internal/config/config.go:85-89` parses them and `cmd_up.go:402-403` appends devcontainer `firewallDomains` — but nothing ever reads `Firewall.Enabled` or turns the domain list into a rule. There is no iptables/nftables egress filtering anywhere in the repo (the only iptables use is the entrypoint's inbound DNAT). Until 2026-07-16 the entrypoint comment justified `bypassPermissions` partly on "disposable microVMs with a firewall" — that comment has been corrected; docs no longer claim a firewall exists.

## Details
- **Decision (Elliott, 2026-07-16): tabled.** Unrestricted web access is currently useful for agents; egress allowlisting should be implemented later, deliberately — not as a quick patch.
- When picked up, design questions to settle: sandbox-side iptables allowlist built from `firewall.domains` at entrypoint (needs a DNS-resolution strategy for domain→IP, and re-resolution on TTL) vs. filtering at the vmnet/host layer; and whether `firewall.enabled=false` should also relax `bypassPermissions`.
- Related exposure to revisit at the same time: the entrypoint sets `route_localnet=1` and a PREROUTING DNAT that forwards **all** inbound TCP to 127.0.0.1 (`cspace-entrypoint.sh:185-198`), which exposes loopback-bound services (supervisor control port :6201, dnsmasq :53, debug ports) to every peer on the vmnet. Sibling sandboxes can reach each other's "loopback-only" services.
- Until resolved: never describe cspace as firewalled in docs, prompts, or agent-facing text.

## Updates
### 2026-07-17T03:42:21Z — @agent — status: acknowledged
filed from the 2026-07-16 hardening survey; implementation deliberately tabled by Elliott — agents currently benefit from web access. Docs/comments corrected in the same commit so nothing claims enforcement exists.
