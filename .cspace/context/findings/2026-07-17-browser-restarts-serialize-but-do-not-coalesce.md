---
title: concurrent browser restarts serialize but don't coalesce; queued caller cancellation can strand a stopped sidecar
date: 2026-07-17
kind: finding
status: open
category: observation
tags: browser-sidecar, restart, daemon, concurrency
---

## Summary
`POST /browser/restart/{project}` serializes restarts per project with a mutex, but N wedged agents requesting restart produce N *sequential full restarts* — each killing the browser sessions the previous one just restored. Separately, the CLI's client timeout (165s) covers one handler run (150s ctx) but not mutex-queue wait + run: a queued caller whose client disconnects cancels its `req.Context()` mid-ladder, which can leave the sidecar stopped while that caller reports failure (the next restart heals it).

## Details
- Coalescing sketch: after acquiring the project mutex, re-probe sidecar health first and return success without restarting if it now passes protocol probes (the first restart in the queue already fixed it); or share one in-flight restart's result with all queued waiters (singleflight pattern).
- Detach the ladder from the request context (run under a server-owned context; the request just awaits the result) so client disconnects can't half-stop the sidecar.
- Identified by the 2026-07-17 final whole-branch review; not blocking (retry heals), but worth fixing before multi-agent projects lean on restart heavily.

## Updates
### 2026-07-18T14:00:00Z — @agent — status: open
filed from the browser-sidecar branch's final whole-branch review
