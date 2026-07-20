---
title: supervisor control-server auth gate has zero server-side test coverage; main.ts fetch handler is unextracted
date: 2026-07-20
kind: finding
status: open
category: observation
tags: supervisor, auth, tests, bun
---

## Summary
The token gate in `lib/agent-supervisor-bun/src/main.ts`'s `Bun.serve` fetch handler (guards /send, /interrupt, /status) is enforced in production but asserted by no test — main.ts has module-scope side effects (exit calls, serve bind) that make it untestable as-is. This is the same defect class as the rc.39-era query-param-token bug (Go client sent a token the server never checked; only an external security scan caught it): client-side tests can pass while server-side enforcement silently diverges.

## Details
- Direction: extract the fetch routing + auth check into `src/routes.ts` (which already hosts `handleInterrupt`) as a pure `route(req, deps)` function; bun-test it directly — wrong token → 401 on every route, missing header → 401, correct token → dispatched.
- Related: the CLI-side wrong-token regression test (`TestWaitSupervisorHealthWrongTokenFails`) covers the Go client half only.

## Updates
### 2026-07-20T05:30:00Z — @agent — status: open
filed from the general-agent branch's final whole-branch review
