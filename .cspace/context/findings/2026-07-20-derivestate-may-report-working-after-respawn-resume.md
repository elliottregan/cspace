---
title: deriveState may report "working" forever on an idle agent after a respawn-resume
date: 2026-07-20
kind: finding
status: resolved
category: bug
tags: supervisor, status, steering, resume
---

## Summary
`deriveState` (lib/agent-supervisor-bun/src/status.ts) treats only `undefined` and `"result"` as idle. Suspected gap (flagged by the final whole-branch review, not yet verified live): after a supervisor respawn with resume, the SDK emits a `system`/`init` event before any user prompt — `lastEventType` becomes `"system"`, so an agent that is actually idle reports `working` until its first real `result`. A coordinator polling `cspace agent status` for idle would wait forever.

## Details
- Verification plan: in a live sandbox, kill -9 the supervisor, let the loop respawn+resume, then `cspace agent status` before sending anything — `working` confirms the bug.
- Fix direction if confirmed: classify `system` events (at least subtype `init`) as idle markers in `deriveState`; keep everything else working.

## Updates
### 2026-07-20T05:30:00Z — @agent — status: open
filed from the general-agent branch's final whole-branch review; to be verified during the rc.39 live check

### 2026-07-20T05:55:00Z — @agent — status: open
CONFIRMED live during the rc.39 verification, and broader than suspected: the
false `working` appears on BOTH triggers — a fresh boot (SDK init event lands
before any prompt; observed `state: working`, `lastEventType: system` on a
just-booted idle agent) and a kill-9 respawn-resume (same readings immediately
after `supervisor-resume`). The state self-corrects after the first completed
turn (`result` → `idle`). Fix as proposed: classify `system` events (at least
subtype `init`) as idle markers in `deriveState`.

### 2026-07-20T06:40:00Z — @agent — status: resolved
Fixed in d1d4a1d. `deriveState` now takes the system-event subtype and
classifies `system`/`init` (session start — fresh boot or respawn-resume,
per sdk.d.ts SDKSystemMessage) as idle; mid-turn system subtypes
(`api_retry`, `compact_boundary`) and subtype-less system events still read
working. main.ts tracks `lastEventSubtype`, feeds it to deriveState, and
reports it in GET /status; `cspace agent status` prints it. Takes effect in
sandboxes on the next `cspace image build` — existing containers keep the
old supervisor until recreated.
