---
title: DNS stale-registry-IP fallback can serve another sandbox's address
date: 2026-07-16
kind: finding
status: open
category: bug
tags: dns, daemon, cache, correctness
---

## Summary
`liveSandboxIP` (`internal/cli/cmd_daemon.go:583-616`) memoizes per-container inspect results for the DNS TTL and negative-caches failed/timed-out inspects; while an inspect failure is cached, the DNS handler falls back to the registry-recorded IP. The code's own CAVEAT notes vmnet reassigns freed IPs — so during an inspect-failure window (hung apiserver, slow `container inspect`), the registry fallback can return an IP that now belongs to a **different** live container. DNS handing out the wrong sandbox's address is a correctness trap: requests (including credentialed ones) silently reach the wrong workspace.

## Details
- The interaction of three mechanisms — TTL memo, negative cache (added in the rc.36 hardening pass to bound re-inspects against a hung apiserver), and registry-IP fallback — is individually reasonable but jointly subtle.
- Suggested direction: when inspect fails, prefer SERVFAIL/NXDOMAIN over a fallback IP unless the registry entry is fresh (registered within some bound, e.g. the current boot's resolution gate); or verify the fallback IP still maps to the expected container name before serving it. Needs a failure-injection test around the memo (the seam for faking inspect results already exists from the negative-cache tests).

## Updates
### 2026-07-17T03:42:21Z — @agent — status: open
filed from the 2026-07-16 hardening survey
