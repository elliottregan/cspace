---
title: project sidecars have no recovery path and are addressed by boot-baked IPs
date: 2026-08-28
kind: finding
status: resolved
category: bug
tags: sidecars, dns, addressing, recovery, daemon, compose, incident
---

## Summary
resume-redux's `convex-backend` sidecar stopped mid-session. The agent in the
sandbox could not restart it — there is no in-sandbox path — and when it was
started by hand it came back on a **different vmnet IP**, which broke tooling
until the agent diagnosed the stale address over several turns.

Both halves of this were fixed for the browser singleton in
[2026-07-17-sidecar-addressed-by-boot-baked-ip-no-recovery-path], where the
requirement was stated generally ("containers (agents) should at least have a
way of restarting the sidecar if it fails or they shut it down"). Project
sidecars from compose got neither fix.

Observed state during the incident:

```
running   cspace-resume-redux-mercury                    (sandbox)
running   cspace-resume-redux-browser
running   cspace-resume-redux-mercury-convex-dashboard
stopped   cspace-resume-redux-mercury-convex-backend
```

## Details

**No restart path.** `sidecars.Orchestration` exposes only `Up` and `Down`,
both reachable solely from inside `cspace up`'s boot flow. The daemon's only
restart route is `POST /browser/restart/{project}`. Nothing an agent can call
touches a project sidecar, and `cspace up` refuses a name a container already
holds, so the only recovery is `cspace down && cspace up` — destroying the
sandbox and its sessions to fix a container next to it.

**Addressing goes stale.** Project sidecars are reached through `/etc/hosts`
entries injected into the sandbox at boot (`sidecars/hosts.go`), holding the
IPs each sidecar had at that moment:

```
192.168.66.5 convex-backend
192.168.66.6 convex-dashboard
```

A restarted sidecar can draw a new IP, and nothing rewrites the file. Worse,
`/etc/hosts` is consulted *before* DNS, so a stale entry wins over any correct
answer a name service could give — the file has to be corrected or removed,
not merely supplemented.

The browser avoids all of this by resolving `browser.<project>.cspace.test`
through daemon DNS, which does a live `container inspect` per query.

**Host reachability is part of the requirement.** The backend must stay
reachable from the host too, not just from sandboxes — host tooling hits it
directly over vmnet today, by the same IP that changes on restart.

## Proposed shape
1. **DNS naming.** Serve `<service>.<sandbox>.<project>.cspace.test` from the
   daemon's DNS handler (a 3-label case alongside the existing 2-label
   `browser.<project>` / `<sandbox>.<project>` forms), resolving via the live
   `container inspect` path already used for sandboxes. The host resolves the
   same name through `/etc/resolver/cspace.test`, so one name works on both
   sides.
2. **Keep compose-style bare names working.** The sandbox's `/etc/resolv.conf`
   currently sets `search .`; setting `search <sandbox>.<project>.cspace.test`
   makes a bare `convex-backend` resolve dynamically through the daemon.
   Single-label names are the only ones the search list touches (ndots:1), so
   external lookups are unaffected.
3. **Drop the sidecar `/etc/hosts` entries** once DNS covers them, since a
   stale entry there beats a correct DNS answer. Self entries (`app`,
   `workspace`) stay.
4. **Agent-invocable restart.** Generalize the browser's escalation ladder
   (stop → SIGKILL → host-process teardown for Apple Container's split-brain
   state → start → verify) to any sidecar container, behind
   `POST /sidecar/restart/{project}/{sandbox}/{service}` and a
   `cspace sidecar list|status|restart` CLI that works host-side and
   in-sandbox, reusing the per-sandbox bearer-token auth the browser route
   already uses.

With (1)–(3) a restart becomes transparent to consumers, which is what makes
(4) safe to hand to an agent.

## Updates
### 2026-08-28 — status: open
Filed after the resume-redux convex-backend incident. Second occurrence of the
same gap in the same project; the browser-only fix did not generalize.

### 2026-08-28 — status: resolved
Shipped all four parts of the proposed shape, with one correction found while
implementing: the DNS half already existed. `daemonDNSHandler` has carried the
3-label `<service>.<sandbox>.<project>.cspace.test` case all along, and it
resolved correctly throughout the incident — verified mid-incident against the
live sandbox, returning the sidecar's *new* address (192.168.66.13) from both
the host resolver and inside the sandbox, while `/etc/hosts` still pinned the
boot-time 192.168.66.5. The bug was never missing DNS; it was `/etc/hosts`
shadowing it, since files are consulted first.

So the fix is: (a) the sandbox's `/etc/hosts` no longer carries sidecar
entries, only its own aliases (`sidecars/lifecycle.go`); sidecars keep the full
map, having no resolver of their own. (b) `/etc/resolv.conf` in the sandbox
gains `search <sandbox>.<project>.cspace.test`, so compose-style bare names
resolve through the daemon rather than a frozen file. (c) `restartProjectSidecar`
generalizes the browser's escalation ladder to any sidecar container, behind
`POST /sidecar/restart/{project}/{sandbox}/{service}` and
`cspace sidecar restart <service>`, host-side and in-sandbox, with the same
same-project token auth. It restarts but does not recreate — a removed
container needs `cspace up`, and the error says so.

Host reachability was a stated requirement and is satisfied by the same name:
`/etc/resolver/cspace.test` points the host at the daemon, so one hostname works
from both sides.
