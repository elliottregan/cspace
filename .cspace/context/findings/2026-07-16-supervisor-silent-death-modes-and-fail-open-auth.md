---
title: "supervisor: swallowed stream death, resume poisoning, fail-open auth, OOM treated as clean exit"
date: 2026-07-16
kind: finding
status: acknowledged
category: bug
tags: supervisor, bun, liveness, auth, oom, deferred, removal-candidate
---

## Summary
Four related defects in `lib/agent-supervisor-bun` and its restart loop:
1. **Swallowed stream death** — `main.ts:79-81` catches a dead SDK `query()` stream and only logs an `sdk-error` event. The process keeps running: `/health` stays 200, `/send` keeps queueing prompts into a dead agent, and because the process never exits, the restart loop never fires.
2. **Resume poisoning** — resume re-reads the last `init` session id from `events.ndjson` and passes it to `query({resume})`. If that session JSONL is gone (cleanup, mount change), the query fails at start → hits (1) → every restart re-reads the same stale id and re-fails. No fresh-session fallback.
3. **Fail-open auth** — the control server binds `0.0.0.0` (`main.ts:85`) and skips bearer-token auth entirely when `CSPACE_CONTROL_TOKEN` is empty (`main.ts:89-94`). Production sets a token (`cmd_up.go` randHex), but any launch path that omits it yields an unauthenticated prompt-injection port into a bypass-permissions agent — combined with the entrypoint DNAT, reachable from sibling sandboxes.
4. **OOM masquerades as shutdown** — `cspace-supervisor-loop.sh:29` treats exit 137 (SIGKILL, i.e. the OOM killer) as intentional shutdown, so an OOM-killed agent silently disappears instead of respawning.

Lower priority: unbounded synchronous `appendFileSync` event log with no rotation (ENOSPC double-faults in the catch path); whole-file read of `events.ndjson` at startup; unbounded prompt queue that acks `{ok:true}` after the consumer is dead.

## Details
- **Decision (Elliott, 2026-07-16): deferred.** The supervisor hasn't been used recently, and the whole layer is a **removal candidate**: it was originally built to orchestrate work across isolated environments so Playwright sessions wouldn't interfere with each other, and Playwright now provides per-connection browser isolation upstream. Decide remove-vs-harden before investing any fix effort here.
- If kept, minimum hardening set: exit non-zero on stream death (let the loop restart), retry without `resume` when a resume fails, restart on exit 137, refuse to serve without a token.

## Updates
### 2026-07-17T03:42:21Z — @agent — status: acknowledged
filed from the 2026-07-16 hardening survey; deferred pending a remove-vs-keep decision on the whole supervisor layer
